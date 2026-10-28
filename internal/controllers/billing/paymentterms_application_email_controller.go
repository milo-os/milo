// Package billing holds milo's controllers for billing.miloapis.com
// resources, which billing's own operator serves through milo's API.
package billing

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// organizationNamespacePrefix is how an organization's namespace is named
// from the organization.
const organizationNamespacePrefix = "organization-"

// applicationEmailEvent is one email the controller sends about an
// application. It names the Email resource, so each is sent at most once.
type applicationEmailEvent string

const (
	// applicationReceived confirms the application to the applicant.
	applicationReceived applicationEmailEvent = "received"
	// applicationSubmitted tells the support mailbox, where Help Scout opens
	// a conversation for it.
	applicationSubmitted applicationEmailEvent = "submitted"
	// applicationApproved tells the applicant their terms were granted.
	applicationApproved applicationEmailEvent = "approved"
	// applicationDeclined gives the applicant the reviewer's message.
	applicationDeclined applicationEmailEvent = "declined"
)

// PaymentTermsApplicationEmailController emails people about payment terms
// applications (billing.miloapis.com BillingArrangementApplication):
//
//   - when one is submitted, a confirmation to the applicant and a notice to
//     the support mailbox;
//   - once billing settles it as Approved or Declined, the decision to the
//     applicant.
//
// Decisions are read from the application's status, which billing's
// controller sets once the approving terms or the decline take effect, so a
// decline that races an approval never sends a "declined" email.
//
// Each email's Email resource is named from the application's UID and the
// event, so re-running a reconcile never sends twice. Events older than
// MaxEventAge are skipped, so a first rollout, or catching up after an
// outage, doesn't mail people about applications decided long ago.
type PaymentTermsApplicationEmailController struct {
	Client client.Client

	// EmailNamespace is where Email resources are created, kept as a
	// central audit trail.
	EmailNamespace string

	ReceivedEmailTemplate  string
	SubmittedEmailTemplate string
	ApprovedEmailTemplate  string
	DeclinedEmailTemplate  string

	// SupportEmailAddress receives the notice of each new application.
	// Empty sends none.
	SupportEmailAddress string

	// PortalURL is the cloud portal's base URL, for the applicant's links.
	PortalURL string
	// StaffPortalURL is the staff portal's base URL, for the review link.
	StaffPortalURL string

	// MaxEventAge is how long after an application is submitted or decided
	// its email may still be sent.
	MaxEventAge time.Duration

	// now is time.Now, overridden in tests.
	now func() time.Time
}

// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangementapplications,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingarrangements,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.miloapis.com,resources=billingaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=emails,verbs=get;list;watch;create

// Reconcile sends whichever of the application's emails are due and not yet
// sent.
func (r *PaymentTermsApplicationEmailController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("application", req.NamespacedName)

	application := &billingv1alpha1.BillingArrangementApplication{}
	if err := r.Client.Get(ctx, req.NamespacedName, application); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !application.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	events := r.dueEvents(application)
	if len(events) == 0 {
		return ctrl.Result{}, nil
	}

	details, err := r.applicationDetails(ctx, application)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, event := range events {
		email, ok, err := r.buildEmail(ctx, application, details, event)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("building %s email: %w", event, err)
		}
		if !ok {
			continue
		}
		if err := r.ensureEmail(ctx, email); err != nil {
			return ctrl.Result{}, fmt.Errorf("sending %s email: %w", event, err)
		}
		logger.Info("Sent payment terms application email", "event", event, "email", email.Name)
	}

	return ctrl.Result{}, nil
}

// dueEvents lists the emails the application's current state calls for.
func (r *PaymentTermsApplicationEmailController) dueEvents(application *billingv1alpha1.BillingArrangementApplication) []applicationEmailEvent {
	phase := application.Status.Phase
	if phase == "" && application.Spec.Withdrawn {
		phase = billingv1alpha1.BillingArrangementApplicationPhaseWithdrawn
	}

	var events []applicationEmailEvent
	// An application withdrawn before we saw it gets no confirmation.
	if phase != billingv1alpha1.BillingArrangementApplicationPhaseWithdrawn &&
		r.recent(application.CreationTimestamp) {
		events = append(events, applicationReceived)
		if r.SupportEmailAddress != "" {
			events = append(events, applicationSubmitted)
		}
	}

	decided := application.Status.DecidedAt != nil && r.recent(*application.Status.DecidedAt)
	switch {
	case decided && phase == billingv1alpha1.BillingArrangementApplicationPhaseApproved:
		events = append(events, applicationApproved)
	case decided && phase == billingv1alpha1.BillingArrangementApplicationPhaseDeclined:
		events = append(events, applicationDeclined)
	}
	return events
}

func (r *PaymentTermsApplicationEmailController) recent(t metav1.Time) bool {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	return now().Sub(t.Time) <= r.MaxEventAge
}

// applicationDetails are the values the application's emails share.
type applicationDetails struct {
	orgName          string
	orgDisplayName   string
	applicantName    string
	applicantEmail   string
	accountCurrency  string
	applicationLink  string
	reviewLink       string
	applicantUserRef string
}

func (r *PaymentTermsApplicationEmailController) applicationDetails(
	ctx context.Context,
	application *billingv1alpha1.BillingArrangementApplication,
) (applicationDetails, error) {
	orgName := strings.TrimPrefix(application.Namespace, organizationNamespacePrefix)
	details := applicationDetails{
		orgName:        orgName,
		orgDisplayName: orgName,
		applicationLink: fmt.Sprintf("%s/onboarding/payment-terms?orgId=%s",
			strings.TrimRight(r.PortalURL, "/"), url.QueryEscape(orgName)),
		reviewLink: fmt.Sprintf("%s/customers/billing-accounts/applications/%s/%s",
			strings.TrimRight(r.StaffPortalURL, "/"), url.PathEscape(orgName), url.PathEscape(application.Name)),
	}

	org := &resourcemanagerv1alpha1.Organization{}
	switch err := r.Client.Get(ctx, types.NamespacedName{Name: orgName}, org); {
	case err == nil:
		if displayName := org.Annotations["kubernetes.io/display-name"]; displayName != "" {
			details.orgDisplayName = displayName
		}
	case !apierrors.IsNotFound(err):
		return details, fmt.Errorf("getting organization %q: %w", orgName, err)
	}

	if applicant := application.Spec.Applicant; applicant != nil {
		details.applicantUserRef = applicant.UID
		details.applicantName = applicant.Username
		details.applicantEmail = applicant.Username
		if applicant.UID != "" {
			user := &iamv1alpha1.User{}
			switch err := r.Client.Get(ctx, types.NamespacedName{Name: applicant.UID}, user); {
			case err == nil:
				if user.Spec.GivenName != "" {
					details.applicantName = user.Spec.GivenName
				}
				if user.Spec.Email != "" {
					details.applicantEmail = user.Spec.Email
				}
			case !apierrors.IsNotFound(err):
				return details, fmt.Errorf("getting applicant %q: %w", applicant.UID, err)
			}
		}
	}

	account := &billingv1alpha1.BillingAccount{}
	switch err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: application.Namespace,
		Name:      application.Spec.BillingAccountRef.Name,
	}, account); {
	case err == nil:
		details.accountCurrency = account.Spec.CurrencyCode
	case !apierrors.IsNotFound(err):
		return details, fmt.Errorf("getting billing account %q: %w", application.Spec.BillingAccountRef.Name, err)
	}

	return details, nil
}

// buildEmail builds the Email for one event. It reports false when the
// email can't be sent yet, or at all: an application with no applicant has
// no one to confirm or tell about the decision, and an approval is held
// until billing names the approving terms.
func (r *PaymentTermsApplicationEmailController) buildEmail(
	ctx context.Context,
	application *billingv1alpha1.BillingArrangementApplication,
	details applicationDetails,
	event applicationEmailEvent,
) (*notificationv1alpha1.Email, bool, error) {
	email := &notificationv1alpha1.Email{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("payment-terms-application-%s-%s", application.UID, event),
			Namespace: r.EmailNamespace,
		},
		Spec: notificationv1alpha1.EmailSpec{
			Priority: notificationv1alpha1.EmailPriorityNormal,
		},
	}
	toApplicant := notificationv1alpha1.EmailRecipient{
		UserRef: notificationv1alpha1.EmailUserReference{Name: details.applicantUserRef},
	}
	// Email variables can't be empty, so blank values are sent as a dash.
	variable := func(name, value string) notificationv1alpha1.EmailVariable {
		return notificationv1alpha1.EmailVariable{Name: name, Value: orDash(value)}
	}

	if event != applicationSubmitted && details.applicantUserRef == "" {
		return nil, false, nil
	}

	switch event {
	case applicationReceived:
		email.Spec.TemplateRef.Name = r.ReceivedEmailTemplate
		email.Spec.Recipient = toApplicant
		email.Spec.Variables = []notificationv1alpha1.EmailVariable{
			variable("UserName", details.applicantName),
			variable("OrganizationName", details.orgDisplayName),
			variable("ActionUrl", details.applicationLink),
		}

	case applicationSubmitted:
		email.Spec.TemplateRef.Name = r.SubmittedEmailTemplate
		email.Spec.Recipient = notificationv1alpha1.EmailRecipient{EmailAddress: r.SupportEmailAddress}
		spend := "Not given"
		if application.Spec.Invoice != nil {
			spend = spendLabel(application.Spec.Invoice.EstimatedMonthlySpend, details.accountCurrency)
		}
		email.Spec.Variables = []notificationv1alpha1.EmailVariable{
			variable("OrganizationName", details.orgDisplayName),
			variable("LegalName", application.Spec.Organization.LegalName),
			variable("ApplicantEmail", details.applicantEmail),
			variable("PaymentOption", paymentOptionLabel(application.Spec.PaymentOption)),
			variable("EstimatedMonthlySpend", spend),
			variable("Situation", application.Spec.Situation),
			variable("ReviewUrl", details.reviewLink),
		}

	case applicationApproved:
		ref := application.Status.ArrangementRef
		if ref == nil || ref.Name == "" {
			return nil, false, nil
		}
		arrangement := &billingv1alpha1.BillingArrangement{}
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: application.Namespace, Name: ref.Name}, arrangement); err != nil {
			return nil, false, fmt.Errorf("getting billing arrangement %q: %w", ref.Name, err)
		}
		netDays := billingv1alpha1.StandardPaymentTermsNetDays
		if arrangement.Spec.Invoice != nil && arrangement.Spec.Invoice.NetDays > 0 {
			netDays = arrangement.Spec.Invoice.NetDays
		}
		startsAt := arrangement.CreationTimestamp
		if arrangement.Spec.StartsAt != nil {
			startsAt = *arrangement.Spec.StartsAt
		}
		email.Spec.TemplateRef.Name = r.ApprovedEmailTemplate
		email.Spec.Recipient = toApplicant
		email.Spec.Variables = []notificationv1alpha1.EmailVariable{
			variable("UserName", details.applicantName),
			variable("OrganizationName", details.orgDisplayName),
			variable("NetDays", strconv.Itoa(int(netDays))),
			variable("StartDate", startsAt.UTC().Format("January 2, 2006")),
			variable("ActionUrl", details.applicationLink),
		}

	case applicationDeclined:
		if application.Status.DeclineMessage == "" {
			return nil, false, nil
		}
		email.Spec.TemplateRef.Name = r.DeclinedEmailTemplate
		email.Spec.Recipient = toApplicant
		email.Spec.Variables = []notificationv1alpha1.EmailVariable{
			variable("UserName", details.applicantName),
			variable("OrganizationName", details.orgDisplayName),
			variable("DeclineMessage", application.Status.DeclineMessage),
			variable("ActionUrl", details.applicationLink),
		}
	}

	return email, true, nil
}

// ensureEmail creates the Email unless an earlier reconcile already did.
func (r *PaymentTermsApplicationEmailController) ensureEmail(ctx context.Context, email *notificationv1alpha1.Email) error {
	existing := &notificationv1alpha1.Email{}
	switch err := r.Client.Get(ctx, client.ObjectKeyFromObject(email), existing); {
	case err == nil:
		return nil
	case !apierrors.IsNotFound(err):
		return err
	}
	if err := r.Client.Create(ctx, email); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func paymentOptionLabel(option billingv1alpha1.ApplicationPaymentOption) string {
	switch option {
	case billingv1alpha1.ApplicationPaymentOptionInvoice:
		return "Pay by invoice"
	case billingv1alpha1.ApplicationPaymentOptionNotSure:
		return "Not sure yet"
	}
	return string(option)
}

// spendLabel is the applicant's estimate as a range in the account's
// currency, such as "2,500–10,000 USD".
func spendLabel(band billingv1alpha1.EstimatedMonthlySpend, currency string) string {
	var amount string
	switch band {
	case billingv1alpha1.EstimatedMonthlySpendUnder500:
		amount = "Under 500"
	case billingv1alpha1.EstimatedMonthlySpendFrom500To2500:
		amount = "500–2,500"
	case billingv1alpha1.EstimatedMonthlySpendFrom2500To10000:
		amount = "2,500–10,000"
	case billingv1alpha1.EstimatedMonthlySpendOver10000:
		amount = "Over 10,000"
	case billingv1alpha1.EstimatedMonthlySpendNotSure:
		return "Not sure yet"
	default:
		return string(band)
	}
	if currency == "" {
		return amount
	}
	return amount + " " + currency
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// SetupWithManager wires the controller into the manager. Billing installs
// the application API, so a control plane without billing skips the
// controller instead of failing to start: watching an API that isn't served
// would stop the whole manager. Installing billing later needs a restart to
// turn the emails on.
func (r *PaymentTermsApplicationEmailController) SetupWithManager(mgr ctrl.Manager) error {
	if r.MaxEventAge <= 0 {
		return fmt.Errorf("max event age must be positive, got %s", r.MaxEventAge)
	}
	gvk := billingv1alpha1.GroupVersion.WithKind("BillingArrangementApplication")
	if _, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
		if apimeta.IsNoMatchError(err) {
			mgr.GetLogger().Info("Billing's application API isn't served, so payment terms application emails are off", "kind", gvk.String())
			return nil
		}
		return fmt.Errorf("failed to look up %s: %w", gvk, err)
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.BillingArrangementApplication{}).
		Named("payment-terms-application-email").
		Complete(r)
}

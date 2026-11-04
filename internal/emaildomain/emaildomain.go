// Package emaildomain decides whether an organization may offer membership to
// everyone at an email domain. An organization may do so only for a domain
// that a verified networking.datumapis.com Domain in one of its projects
// covers.
package emaildomain

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	// JoinMethodLabel marks a UserInvitation the email domain joining
	// controller created. Its value is JoinMethodEmailDomain.
	JoinMethodLabel = "iam.miloapis.com/join-method"
	// JoinMethodEmailDomain is the JoinMethodLabel value of an email domain
	// offer.
	JoinMethodEmailDomain = "EmailDomain"
	// EmailDomainLabel records the email domain an offer was made for.
	EmailDomainLabel = "iam.miloapis.com/email-domain"
)

// DomainGVR is the network-services-operator Domain resource, served in each
// project control plane.
var DomainGVR = schema.GroupVersionResource{
	Group:    "networking.datumapis.com",
	Version:  "v1alpha",
	Resource: "domains",
}

const domainConditionVerified = "Verified"

// Of returns the lowercased domain part of an email address, or "" when the
// address has none.
func Of(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// Covers reports whether one of the verified domain names is emailDomain or a
// parent of it. A verified subdomain never covers its parent.
func Covers(verified []string, emailDomain string) bool {
	emailDomain = strings.ToLower(strings.TrimSuffix(emailDomain, "."))
	for _, v := range verified {
		v = strings.ToLower(strings.TrimSuffix(v, "."))
		if v == "" {
			continue
		}
		if emailDomain == v || strings.HasSuffix(emailDomain, "."+v) {
			return true
		}
	}
	return false
}

// Instructions tells an owner how to verify the given email domains.
func Instructions(domains []string) string {
	return fmt.Sprintf(
		"no project in this organization has a verified Domain for %s. "+
			"Add the domain as a Domain in any project in this organization, "+
			"verify it with the DNS TXT record or HTTP token shown on that Domain, "+
			"and turn on email domain joining once the Domain shows as Verified",
		strings.Join(domains, ", "),
	)
}

// DomainLister lists the Domains in one project's control plane.
type DomainLister func(ctx context.Context, project string) ([]unstructured.Unstructured, error)

// Verifier finds the verified Domains in an organization's projects.
type Verifier struct {
	// Client reads Projects from the core control plane.
	Client client.Reader
	// ListDomains lists the Domains in a project's control plane.
	ListDomains DomainLister
}

// NewVerifier returns a Verifier that reads Projects with c and Domains through
// the core control plane at cfg.
func NewVerifier(c client.Reader, cfg *rest.Config) *Verifier {
	return &Verifier{Client: c, ListDomains: NewProjectDomainLister(cfg)}
}

// NewProjectDomainLister lists Domains through each project's control plane
// endpoint on the core control plane at base.
func NewProjectDomainLister(base *rest.Config) DomainLister {
	return func(ctx context.Context, project string) ([]unstructured.Unstructured, error) {
		cfg := rest.CopyConfig(base)
		host, err := url.Parse(cfg.Host)
		if err != nil {
			return nil, fmt.Errorf("parse API host %q: %w", cfg.Host, err)
		}
		host.Path = fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", project)
		cfg.Host = host.String()

		dc, err := dynamic.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("create client for project %q: %w", project, err)
		}
		list, err := dc.Resource(DomainGVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}
}

// VerifiedDomains returns the names of the verified Domains in the
// organization's ready projects. A project without the Domain API has none.
// A project that cannot be read is skipped, and its error is returned only
// when no verified Domain was found, so one broken project can't hide another
// project's verified Domain.
func (v *Verifier) VerifiedDomains(ctx context.Context, organization string) ([]string, error) {
	var projects resourcemanagerv1alpha1.ProjectList
	if err := v.Client.List(ctx, &projects, client.MatchingLabels{
		resourcemanagerv1alpha1.OrganizationNameLabel: organization,
	}); err != nil {
		return nil, fmt.Errorf("list projects of organization %q: %w", organization, err)
	}

	var verified []string
	var errs []error
	for i := range projects.Items {
		project := &projects.Items[i]
		if project.DeletionTimestamp != nil ||
			project.Spec.OwnerRef.Kind != "Organization" ||
			project.Spec.OwnerRef.Name != organization ||
			!meta.IsStatusConditionTrue(project.Status.Conditions, resourcemanagerv1alpha1.ProjectReady) {
			continue
		}

		domains, err := v.ListDomains(ctx, project.Name)
		if err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("list Domains in project %q: %w", project.Name, err))
			continue
		}
		for _, d := range domains {
			if d.GetDeletionTimestamp() != nil || !isVerified(d) {
				continue
			}
			if name, _, _ := unstructured.NestedString(d.Object, "spec", "domainName"); name != "" {
				verified = append(verified, strings.ToLower(name))
			}
		}
	}

	if len(verified) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return verified, nil
}

func isVerified(d unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(d.Object, "status", "conditions")
	for _, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["type"] == domainConditionVerified {
			return cm["status"] == string(metav1.ConditionTrue)
		}
	}
	return false
}

package v1alpha1

import (
	"context"
	"fmt"
	"slices"

	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	identityv1alpha1 "go.miloapis.com/milo/pkg/apis/identity/v1alpha1"
)

// log is for logging in this package.
var userlog = logf.Log.WithName("user-resource")

// +kubebuilder:webhook:path=/validate-iam-miloapis-com-v1alpha1-user,mutating=false,failurePolicy=fail,sideEffects=NoneOnDryRun,groups=iam.miloapis.com,resources=users,verbs=create;update,versions=v1alpha1,name=vuser.iam.miloapis.com,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// SetupWebhooksWithManager sets up all iam.miloapis.com webhooks
func SetupUserWebhooksWithManager(mgr ctrl.Manager, systemNamespace string, userSelfManageRoleName string) error {
	userlog.Info("Setting up iam.miloapis.com user webhooks")

	return ctrl.NewWebhookManagedBy(mgr).
		For(&iamv1alpha1.User{}).
		WithValidator(&UserValidator{
			client:                 mgr.GetClient(),
			restConfig:             mgr.GetConfig(),
			scheme:                 mgr.GetScheme(),
			systemNamespace:        systemNamespace,
			userSelfManageRoleName: userSelfManageRoleName,
		}).
		Complete()
}

// UserValidator validates Users
type UserValidator struct {
	client                 client.Client
	restConfig             *rest.Config
	scheme                 *runtime.Scheme
	decoder                admission.Decoder
	systemNamespace        string
	userSelfManageRoleName string
}

func (v *UserValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	user := obj.(*iamv1alpha1.User)
	userlog.Info("Validating User", "name", user.Name)

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get request from context: %w", err)
	}

	if req.DryRun != nil && *req.DryRun {
		return nil, nil
	}

	if err := v.createSelfManagePolicyBinding(ctx, user); err != nil {
		userlog.Error(err, "Failed to create owner policy binding")
		return nil, fmt.Errorf("failed to create owner policy binding: %w", err)
	}

	userPreferences, err := v.createUserPreference(ctx, user)
	if err != nil {
		userlog.Error(err, "Failed to create user preference")
		return nil, fmt.Errorf("failed to create user preference: %w", err)
	}

	if err := v.createUserPreferencePolicyBinding(ctx, user, userPreferences); err != nil {
		userlog.Error(err, "Failed to create user preference policy binding")
		return nil, fmt.Errorf("failed to create user preference policy binding: %w", err)
	}

	return nil, nil
}

func (v *UserValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldUser := oldObj.(*iamv1alpha1.User)
	newUser := newObj.(*iamv1alpha1.User)

	// If email hasn't changed, allow the update
	if oldUser.Spec.Email == newUser.Spec.Email {
		return nil, nil
	}

	userlog.Info("Email change detected",
		"user", newUser.Name,
		"oldEmail", oldUser.Spec.Email,
		"newEmail", newUser.Spec.Email)

	// Get the requesting user from admission request
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		userlog.Error(err, "Failed to get request from context")
		return nil, errors.NewInternalError(fmt.Errorf("failed to get request from context: %w", err))
	}

	// Allow system administrators to bypass validation
	// This allows automated tests and system components to manage User resources directly
	if slices.Contains(req.UserInfo.Groups, "system:masters") {
		userlog.Info("Allowing email update for system administrator", "user", req.UserInfo.Username)
		return nil, nil
	}

	// Only allow users to update their own email (self-service only)
	// This ensures that the UserIdentity list will be scoped to the correct user
	// Note: req.UserInfo.UID is the Milo user ID, which matches the User resource name
	if req.UserInfo.UID != newUser.Name {
		userlog.Info("Rejecting email update from different user",
			"requestingUser", req.UserInfo.UID,
			"targetUser", newUser.Name)
		return nil, errors.NewForbidden(
			schema.GroupResource{Group: iamv1alpha1.SchemeGroupVersion.Group, Resource: "users"},
			newUser.Name,
			fmt.Errorf("cannot update email address of another user. Email updates are restricted to self-service only"))
	}

	// Get UserIdentities for the requesting user using impersonated client
	// UserIdentity is a dynamic REST API that requires proper user context
	impersonatedClient, err := v.createImpersonatedClient(req.UserInfo)
	if err != nil {
		userlog.Error(err, "Failed to create impersonated client", "user", newUser.Name)
		return nil, errors.NewInternalError(fmt.Errorf("failed to create impersonated client: %w", err))
	}

	identityList := &identityv1alpha1.UserIdentityList{}
	if err := impersonatedClient.List(ctx, identityList); err != nil {
		userlog.Error(err, "Failed to list user identities", "user", newUser.Name)
		return nil, errors.NewInternalError(fmt.Errorf("failed to list user identities: %w", err))
	}

	// If no identities are linked, reject the change
	if len(identityList.Items) == 0 {
		userlog.Info("User has no linked identities, rejecting email change", "user", newUser.Name)
		return nil, errors.NewBadRequest(
			"cannot change email: no verified identity providers linked to this account. " +
				"Please link an identity provider (GitHub, Google, etc.) first")
	}

	// Validate that the new email exists in any of the linked identities
	// The email is stored in the Username field of UserIdentity
	for _, identity := range identityList.Items {
		if identity.Status.Username == newUser.Spec.Email {
			userlog.Info("Email validated against identity provider",
				"email", newUser.Spec.Email,
				"provider", identity.Status.ProviderName,
				"providerID", identity.Status.ProviderID)
			return nil, nil // Email is valid
		}
	}

	// Build list of available emails for error message
	availableEmails := make([]string, 0, len(identityList.Items))
	for _, identity := range identityList.Items {
		if identity.Status.Username != "" {
			availableEmails = append(availableEmails, identity.Status.Username)
		}
	}

	// Email not found in any linked identity
	userlog.Info("Email not found in linked identities",
		"requestedEmail", newUser.Spec.Email,
		"availableEmails", availableEmails)

	return nil, errors.NewBadRequest(
		fmt.Sprintf(
			"email %q is not linked to any verified identity provider. "+
				"Available verified emails: %v",
			newUser.Spec.Email,
			availableEmails))
}

func (v *UserValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// createImpersonatedClient creates a client that impersonates the requesting user
// This is necessary for UserIdentity API calls which require proper user context
func (v *UserValidator) createImpersonatedClient(userInfo authenticationv1.UserInfo) (client.Client, error) {
	// Convert Extra from authenticationv1.ExtraValue to map[string][]string
	extra := make(map[string][]string)
	for k, v := range userInfo.Extra {
		extra[k] = []string(v)
	}

	// Create a copy of the REST config with impersonation
	impersonatedConfig := rest.CopyConfig(v.restConfig)
	impersonatedConfig.Impersonate = rest.ImpersonationConfig{
		UserName: userInfo.Username,
		UID:      userInfo.UID,
		Groups:   userInfo.Groups,
		Extra:    extra,
	}

	// Create a new client with the impersonated config
	return client.New(impersonatedConfig, client.Options{
		Scheme: v.scheme,
	})
}

// createSelfManagePolicyBinding creates a PolicyBinding for the organization owner
func (v *UserValidator) createSelfManagePolicyBinding(ctx context.Context, user *iamv1alpha1.User) error {
	userlog.Info("Attempting to create PolicyBinding for new user", "user", user.Name)

	// Build the PolicyBinding
	policyBinding := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("user-self-manage-%s", user.Name),
			Namespace: v.systemNamespace,
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      v.userSelfManageRoleName,
				Namespace: v.systemNamespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: user.Name,
					UID:  string(user.GetUID()),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
					Kind:     "User",
					Name:     user.Name,
					UID:      string(user.GetUID()),
				},
			},
		},
	}

	if err := v.client.Create(ctx, policyBinding); err != nil {
		return fmt.Errorf("failed to create policy binding resource: %w", err)
	}

	return nil
}

// createUserPreference creates a UserPreference for the new user
func (v *UserValidator) createUserPreference(ctx context.Context, user *iamv1alpha1.User) (*iamv1alpha1.UserPreference, error) {
	userlog.Info("Attempting to create UserPreference for new user", "user", user.Name)

	// Build the UserPreference
	userPreference := &iamv1alpha1.UserPreference{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("userpreference-%s", user.Name),
		},
		Spec: iamv1alpha1.UserPreferenceSpec{
			UserRef: iamv1alpha1.UserReference{
				Name: user.Name,
			},
			Theme:       "system", // Default theme
			DisplayName: "",
			Title:       "",
		},
	}

	if err := v.client.Create(ctx, userPreference); err != nil {
		return nil, fmt.Errorf("failed to create user preference resource: %w", err)
	}

	return userPreference, nil
}

// createUserPreferencePolicyBinding creates a PolicyBinding for the user's UserPreference
func (v *UserValidator) createUserPreferencePolicyBinding(ctx context.Context, user *iamv1alpha1.User, userPreference *iamv1alpha1.UserPreference) error {
	userlog.Info("Attempting to create PolicyBinding for user preference", "user", user.Name)

	// Build the PolicyBinding
	policyBinding := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("userpreference-self-manage-%s", user.Name),
			Namespace: v.systemNamespace,
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef: iamv1alpha1.RoleReference{
				Name:      "iam-user-preferences-manager",
				Namespace: v.systemNamespace,
			},
			Subjects: []iamv1alpha1.Subject{
				{
					Kind: "User",
					Name: user.Name,
					UID:  string(user.GetUID()),
				},
			},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup: iamv1alpha1.SchemeGroupVersion.Group,
					Kind:     "UserPreference",
					Name:     userPreference.Name,
					UID:      string(userPreference.UID),
				},
			},
		},
	}

	if err := v.client.Create(ctx, policyBinding); err != nil {
		return fmt.Errorf("failed to create user preference policy binding resource: %w", err)
	}

	return nil
}

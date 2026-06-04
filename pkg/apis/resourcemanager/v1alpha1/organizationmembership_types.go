package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OrganizationMembership establishes a user's membership in an organization and
// optionally assigns roles to grant permissions. The controller automatically
// manages PolicyBinding resources for each assigned role, simplifying access
// control management.
//
// Key features:
//   - Establishes user-organization relationship
//   - Automatic PolicyBinding creation and deletion for assigned roles
//   - Supports multiple roles per membership
//   - Cross-namespace role references
//   - Detailed status tracking with per-role reconciliation state
//
// Prerequisites:
//   - User resource must exist
//   - Organization resource must exist
//   - Referenced Role resources must exist in their respective namespaces
//
// Example - Basic membership with role assignment:
//
//	apiVersion: resourcemanager.miloapis.com/v1alpha1
//	kind: OrganizationMembership
//	metadata:
//	  name: jane-acme-membership
//	  namespace: organization-acme-corp
//	spec:
//	  organizationRef:
//	    name: acme-corp
//	  userRef:
//	    name: jane-doe
//	  roles:
//	  - name: organization-viewer
//	    namespace: organization-acme-corp
//
// Related resources:
//   - User: The user being granted membership
//   - Organization: The organization the user joins
//   - Role: Defines permissions granted to the user
//   - PolicyBinding: Automatically created by the controller for each role
//
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Organization",type="string",JSONPath=".spec.organizationRef.name"
// +kubebuilder:printcolumn:name="Organization Type",type="string",JSONPath=".status.organization.type"
// +kubebuilder:printcolumn:name="Organization Display Name",type="string",JSONPath=".status.organization.displayName"
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.userRef.name"
// +kubebuilder:printcolumn:name="User Email",type="string",JSONPath=".status.user.email",priority=1
// +kubebuilder:printcolumn:name="User Given Name",type="string",JSONPath=".status.user.givenName",priority=1
// +kubebuilder:printcolumn:name="User Family Name",type="string",JSONPath=".status.user.familyName",priority=1
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:path=organizationmemberships,scope=Namespaced,singular=organizationmembership
// +kubebuilder:selectablefield:JSONPath=".spec.userRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.organizationRef.name"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,User"
type OrganizationMembership struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OrganizationMembershipSpec   `json:"spec,omitempty"`
	Status OrganizationMembershipStatus `json:"status,omitempty"`
}

// OrganizationMembershipSpec defines the desired state of OrganizationMembership.
// It specifies which user should be a member of which organization, and optionally
// which roles should be assigned to grant permissions.
type OrganizationMembershipSpec struct {
	// OrganizationRef identifies the organization to grant membership in.
	// The organization must exist before creating the membership.
	//
	// Required field.
	//
	// +kubebuilder:validation:Required
	OrganizationRef OrganizationReference `json:"organizationRef"`

	// UserRef identifies the user to grant organization membership.
	// The user must exist before creating the membership.
	//
	// Required field.
	//
	// +kubebuilder:validation:Required
	UserRef MemberReference `json:"userRef"`

	// Roles specifies a list of roles to assign to the user within the organization.
	// The controller automatically creates and manages PolicyBinding resources for
	// each role. Roles can be added or removed after the membership is created.
	//
	// Optional field. When omitted or empty, the membership is established without
	// any role assignments. Roles can be added later via update operations.
	//
	// Each role reference must specify:
	//   - name: The role name (required)
	//   - namespace: The role namespace (optional, defaults to membership namespace)
	//
	// Duplicate roles are prevented by admission webhook validation.
	//
	// Example:
	//
	//   roles:
	//   - name: organization-admin
	//     namespace: organization-acme-corp
	//   - name: billing-manager
	//     namespace: organization-acme-corp
	//   - name: shared-developer
	//     namespace: milo-system
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	Roles []RoleReference `json:"roles,omitempty"`
}

// OrganizationReference contains information that points to the Organization being referenced.
type OrganizationReference struct {
	// Name is the name of resource being referenced
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// MemberReference contains information that points to the User being referenced.
type MemberReference struct {
	// Name is the name of resource being referenced
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// RoleReference defines a reference to a Role resource for organization membership.
// +k8s:deepcopy-gen=true
type RoleReference struct {
	// Name of the referenced Role.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the referenced Role.
	// If not specified, it defaults to the organization membership's namespace.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// OrganizationMembershipStatus defines the observed state of OrganizationMembership.
// The controller populates this status to reflect the current reconciliation state,
// including whether the membership is ready and which roles have been successfully applied.
type OrganizationMembershipStatus struct {
	// ObservedGeneration tracks the most recent membership spec that the
	// controller has processed. Use this to determine if status reflects
	// the latest changes.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the current status of the membership.
	//
	// Standard conditions:
	//   - Ready: Indicates membership has been established (user and org exist)
	//   - RolesApplied: Indicates whether all roles have been successfully applied
	//
	// Check the RolesApplied condition to determine overall role assignment status:
	//   - True with reason "AllRolesApplied": All roles successfully applied
	//   - True with reason "NoRolesSpecified": No roles in spec, membership only
	//   - False with reason "PartialRolesApplied": Some roles failed (check appliedRoles for details)
	//
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// User contains cached information about the user in this membership.
	// This information is populated by the controller from the referenced user.
	//
	// +kubebuilder:validation:Optional
	User OrganizationMembershipUserStatus `json:"user,omitempty"`

	// Organization contains cached information about the organization in this membership.
	// This information is populated by the controller from the referenced organization.
	//
	// +kubebuilder:validation:Optional
	Organization OrganizationMembershipOrganizationStatus `json:"organization,omitempty"`

	// AppliedRoles tracks the reconciliation state of each role in spec.roles.
	// This array provides per-role status, making it easy to identify which
	// roles are applied and which failed.
	//
	// Each entry includes:
	//   - name and namespace: Identifies the role
	//   - status: "Applied", "Pending", or "Failed"
	//   - policyBindingRef: Reference to the created PolicyBinding (when Applied)
	//   - appliedAt: Timestamp when role was applied (when Applied)
	//   - message: Error details (when Failed)
	//
	// Use this to troubleshoot role assignment issues. Roles marked as "Failed"
	// include a message explaining why the PolicyBinding could not be created.
	//
	// Example:
	//
	//   appliedRoles:
	//   - name: org-admin
	//     namespace: organization-acme-corp
	//     status: Applied
	//     appliedAt: "2025-10-28T10:00:00Z"
	//     policyBindingRef:
	//       name: jane-acme-membership-a1b2c3d4
	//       namespace: organization-acme-corp
	//   - name: invalid-role
	//     namespace: organization-acme-corp
	//     status: Failed
	//     message: "role 'invalid-role' not found in namespace 'organization-acme-corp'"
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	AppliedRoles []AppliedRole `json:"appliedRoles,omitempty"`
}

// AppliedRole tracks the reconciliation status of a single role assignment
// within an organization membership. The controller maintains this status to
// provide visibility into which roles are successfully applied and which failed.
//
// +k8s:deepcopy-gen=true
type AppliedRole struct {
	// Name identifies the Role resource.
	//
	// Required field.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace identifies the namespace containing the Role resource.
	// Empty when the role is in the membership's namespace.
	//
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// Status indicates the current state of this role assignment.
	//
	// Valid values:
	//   - "Applied": PolicyBinding successfully created and role is active
	//   - "Pending": Role is being reconciled (transitional state)
	//   - "Failed": PolicyBinding could not be created (see Message for details)
	//
	// Required field.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Applied;Pending;Failed
	Status string `json:"status"`

	// Message provides additional context about the role status.
	// Contains error details when Status is "Failed", explaining why the
	// PolicyBinding could not be created.
	//
	// Common failure messages:
	//   - "role 'role-name' not found in namespace 'namespace'"
	//   - "Failed to create PolicyBinding: <error details>"
	//
	// Empty when Status is "Applied" or "Pending".
	//
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// PolicyBindingRef references the PolicyBinding resource that was
	// automatically created for this role.
	//
	// Only populated when Status is "Applied". Use this reference to
	// inspect or troubleshoot the underlying PolicyBinding.
	//
	// +kubebuilder:validation:Optional
	PolicyBindingRef *PolicyBindingReference `json:"policyBindingRef,omitempty"`

	// AppliedAt records when this role was successfully applied.
	// Corresponds to the PolicyBinding creation time.
	//
	// Only populated when Status is "Applied".
	//
	// +kubebuilder:validation:Optional
	AppliedAt *metav1.Time `json:"appliedAt,omitempty"`
}

// PolicyBindingReference contains information about the PolicyBinding created for a role.
// +k8s:deepcopy-gen=true
type PolicyBindingReference struct {
	// Name of the PolicyBinding resource.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the PolicyBinding resource.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// OrganizationMembershipUserStatus defines the observed state of a user in a membership.
type OrganizationMembershipUserStatus struct {
	// Email is the email of the user in the membership.
	// +kubebuilder:validation:Optional
	Email string `json:"email,omitempty"`
	// GivenName is the given name of the user in the membership.
	// +kubebuilder:validation:Optional
	GivenName string `json:"givenName,omitempty"`
	// FamilyName is the family name of the user in the membership.
	// +kubebuilder:validation:Optional
	FamilyName string `json:"familyName,omitempty"`
	// AvatarURL is the avatar URL of the user in the membership.
	// +kubebuilder:validation:Optional
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// OrganizationMembershipOrganizationStatus defines the observed state of an organization in a membership.
type OrganizationMembershipOrganizationStatus struct {
	// Type is the type of the organization in the membership.
	// +kubebuilder:validation:Optional
	Type string `json:"type,omitempty"`
	// DisplayName is the display name of the organization in the membership.
	// +kubebuilder:validation:Optional
	DisplayName string `json:"displayName,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// OrganizationMembershipList contains a list of OrganizationMembership
type OrganizationMembershipList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OrganizationMembership `json:"items"`
}

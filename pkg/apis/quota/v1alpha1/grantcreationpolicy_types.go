package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GrantCreationPolicySpec defines the desired state of GrantCreationPolicy.
type GrantCreationPolicySpec struct {
	// Trigger defines what resource changes should trigger grant creation.
	//
	// +kubebuilder:validation:Required
	Trigger GrantTriggerSpec `json:"trigger"`
	// Target defines where and how grants should be created.
	//
	// +kubebuilder:validation:Required
	Target GrantTargetSpec `json:"target"`
	// Disabled determines if this policy is inactive.
	// If true, no **ResourceGrants** will be created for matching resources.
	//
	// +kubebuilder:default=false
	// +optional
	Disabled *bool `json:"disabled,omitempty"`
}

// GrantTriggerSpec defines the resource and conditions that trigger grant creation.
type GrantTriggerSpec struct {
	// Resource specifies which resource type triggers this policy.
	//
	// +kubebuilder:validation:Required
	Resource GrantTriggerResource `json:"resource"`
	// Constraints are CEL expressions that must evaluate to true for grant creation.
	// These are pure CEL expressions WITHOUT {{ }} delimiters (unlike template fields).
	// All constraints must pass for the policy to trigger.
	// The 'object' variable contains the trigger resource being evaluated.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	Constraints []ConditionExpression `json:"constraints,omitempty"`
}

// GrantTriggerResource identifies the resource type that triggers grant creation.
type GrantTriggerResource struct {
	// APIVersion of the trigger resource in the format "group/version".
	// For core resources, use "v1".
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*\/)?v[0-9]+((alpha|beta)[0-9]*)?$`
	APIVersion string `json:"apiVersion"`
	// Kind is the kind of the trigger resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Z][a-zA-Z0-9]*$`
	Kind string `json:"kind"`
}

// ConditionExpression defines a CEL expression that determines when the policy should trigger.
// All expressions in a policy's trigger conditions must evaluate to true for the policy to activate.
type ConditionExpression struct {
	// Expression specifies the CEL expression to evaluate against the trigger resource.
	// This is a pure CEL expression WITHOUT {{ }} delimiters (unlike template fields).
	// Must return a boolean value (true to match, false to skip).
	// Maximum 1024 characters.
	//
	// Available variables in GrantCreationPolicy context:
	// - trigger: The complete resource being watched (map[string]any)
	//   - trigger.metadata.name, trigger.spec.*, trigger.status.*, etc.
	//
	// Common expression patterns:
	// - trigger.spec.tier == "premium" (check resource field)
	// - trigger.metadata.labels["environment"] == "prod" (check labels)
	// - trigger.status.phase == "Active" (check status)
	// - trigger.metadata.namespace == "production" (check namespace)
	// - has(trigger.spec.quotaProfile) (check field existence)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Expression string `json:"expression"`

	// Message provides a human-readable description explaining when this condition applies.
	// Used for documentation and debugging. Maximum 256 characters.
	//
	// Examples:
	// - "Applies only to premium tier organizations"
	// - "Matches organizations in production environment"
	// - "Triggers when quota profile is specified"
	//
	// +optional
	// +kubebuilder:validation:MaxLength=256
	Message string `json:"message,omitempty"`
}

// GrantTargetSpec defines where and how grants are created.
type GrantTargetSpec struct {
	// ParentContext defines cross-control-plane targeting.
	// If specified, grants will be created in the target parent context
	// instead of the current control plane.
	//
	// +optional
	ParentContext *GrantParentContextSpec `json:"parentContext,omitempty"`
	// ResourceGrantTemplate defines how to create **ResourceGrants**.
	// String fields support CEL expressions wrapped in {{ }} delimiters for dynamic content.
	// Plain strings without {{ }} are treated as literal values.
	//
	// +kubebuilder:validation:Required
	ResourceGrantTemplate ResourceGrantTemplate `json:"resourceGrantTemplate"`
}

// GrantParentContextSpec enables cross-cluster grant creation by targeting a parent control plane.
// Used to create grants in infrastructure clusters when policies run in child clusters.
type GrantParentContextSpec struct {
	// APIGroup specifies the API group of the parent context resource.
	// Must follow DNS subdomain format. Maximum 253 characters.
	//
	// Examples:
	// - "resourcemanager.miloapis.com" (for Organization parent context)
	// - "infrastructure.miloapis.com" (for Cluster parent context)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	APIGroup string `json:"apiGroup"`

	// Kind specifies the resource type that represents the parent context.
	// Must be a valid Kubernetes resource Kind. Maximum 63 characters.
	//
	// Examples:
	// - "Organization" (create grants in organization's parent control plane)
	// - "Cluster" (create grants in cluster's parent infrastructure)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Z][a-zA-Z0-9]*$`
	Kind string `json:"kind"`

	// NameExpression is a CEL expression that resolves the name of the parent context resource.
	// Must return a string value that identifies the specific parent context instance.
	// Maximum 512 characters.
	//
	// Available variables:
	// - object: The trigger resource being evaluated (complete object)
	//
	// Common expression patterns:
	// - object.spec.organization (direct field reference)
	// - object.metadata.labels["parent-org"] (label-based resolution)
	// - object.metadata.namespace.split("-")[0] (derived from namespace naming)
	//
	// Examples:
	// - "acme-corp" (literal parent name)
	// - object.spec.parentOrganization (field from trigger resource)
	// - object.metadata.labels["quota.miloapis.com/organization"] (label value)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	NameExpression string `json:"nameExpression"`
}

// ResourceGrantTemplate defines the specification for creating ResourceGrants using actual ResourceGrant structure.
type ResourceGrantTemplate struct {
	// Metadata for the created ResourceGrant.
	// String fields support CEL expressions wrapped in {{ }} delimiters.
	//
	// +kubebuilder:validation:Required
	Metadata ObjectMetaTemplate `json:"metadata"`
	// Spec for the created ResourceGrant.
	// String fields support CEL expressions wrapped in {{ }} delimiters.
	//
	// +kubebuilder:validation:Required
	Spec ResourceGrantSpec `json:"spec"`
}

// GrantCreationPolicyStatus defines the observed state of GrantCreationPolicy.
//
// Status fields
// - conditions[type=Ready]: True when the policy is validated and active.
// - conditions[type=ParentContextReady]: True when cross‑cluster targeting is resolvable.
// - observedGeneration: Latest spec generation processed by the quota system.
//
// See also
// - [ResourceGrant](#resourcegrant): The object created by this policy.
// - [ResourceRegistration](#resourceregistration): Resource types for which grants are issued.
type GrantCreationPolicyStatus struct {
	// ObservedGeneration is the most recent generation observed.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the policy's current state.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition type constants for GrantCreationPolicy.
const (
	// GrantCreationPolicyReady indicates the policy is ready for use.
	GrantCreationPolicyReady = "Ready"
	// GrantCreationPolicyParentContextReady indicates parent context resolution is working.
	GrantCreationPolicyParentContextReady = "ParentContextReady"
)

// Condition reason constants for GrantCreationPolicy.
const (
	// GrantCreationPolicyReadyReason indicates the policy is ready.
	GrantCreationPolicyReadyReason = "PolicyReady"
	// GrantCreationPolicyValidationFailedReason indicates validation failed.
	GrantCreationPolicyValidationFailedReason = "ValidationFailed"
	// GrantCreationPolicyDisabledReason indicates the policy is disabled.
	GrantCreationPolicyDisabledReason = "PolicyDisabled"
	// GrantCreationPolicyParentContextReadyReason indicates parent context is ready.
	GrantCreationPolicyParentContextReadyReason = "ParentContextReady"
	// GrantCreationPolicyParentContextFailedReason indicates parent context resolution failed.
	GrantCreationPolicyParentContextFailedReason = "ParentContextFailed"
)

// Helper method to get the GVK for the trigger resource.
func (t *GrantTriggerResource) GetGVK() schema.GroupVersionKind {
	gv, _ := schema.ParseGroupVersion(t.APIVersion)
	return schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    t.Kind,
	}
}

// GrantCreationPolicy automates ResourceGrant creation when observed resources meet conditions.
// Use it to provision quota based on resource lifecycle events and attributes.
//
// ### How It Works
// - Watch the kind in `spec.trigger.resource` and evaluate all `spec.trigger.constraints[]`.
// - When all constraints are true, evaluate `spec.target.resourceGrantTemplate` and create a `ResourceGrant`.
// - Optionally target a parent control plane via `spec.target.parentContext` (CEL-resolved name) for cross-cluster allocation.
// - Allowances (resource types and amounts) are static in `v1alpha1`.
//
// ### Template Expressions
// Template expressions generate dynamic content for ResourceGrant fields including metadata and specification.
// Content inside `{{ }}` delimiters is evaluated as CEL expressions, while content outside is treated as literal text.
//
// **Template Expression Rules:**
// - `{{expression}}` - Pure CEL expression, evaluated and substituted
// - `literal-text` - Used as-is without any evaluation
// - `{{expression}}-literal` - CEL output combined with literal text
// - `prefix-{{expression}}-suffix` - Literal text surrounding CEL expression
//
// **Template Expression Examples:**
// - `{{trigger.metadata.name + '-grant'}}` - Pure CEL expression (metadata)
// - `{{trigger.metadata.name}}-quota-grant` - CEL + literal suffix (metadata)
// - `{{trigger.spec.type + "-consumer"}}` - Extract spec field for consumer name (spec)
// - `{{trigger.metadata.labels["environment"] + "-grants"}}` - Label-based naming (spec)
// - `fixed-grant-name` - Literal string only (no evaluation)
//
// **Use Template Expressions For:** ResourceGrantTemplate fields (metadata and spec)
//
// ### Constraint Expressions
// Constraint expressions determine whether a policy should trigger by evaluating boolean conditions.
// These are pure CEL expressions without delimiters that must return true/false values.
//
// **Constraint Expression Rules:**
// - Write pure CEL expressions directly (no wrapping syntax)
// - Must return boolean values (true = trigger policy, false = skip)
// - All constraints in a policy must return true for the policy to activate
//
// **Constraint Expression Examples:**
// - `trigger.spec.tier == "premium"` - Field equality check
// - `trigger.metadata.labels["environment"] == "prod"` - Label-based filtering
// - `trigger.status.phase == "Active"` - Status condition check
// - `has(trigger.spec.quotaProfile)` - Field existence check
//
// **Use Constraint Expressions For:** spec.trigger.constraints fields
//
// ### Expression Variables
// Both template and constraint expressions have access to the resource context variables:
//
// **trigger**: The complete resource that triggered the policy, including all metadata, spec,
// and status fields. Navigate using CEL property access: `trigger.metadata.name`, `trigger.spec.tier`.
// This is the only variable available since GrantCreationPolicy runs during resource watching,
// not during admission processing.
//
// **CEL Functions**: Standard CEL functions available for data manipulation including conditional
// expressions (`condition ? value1 : value2`), string methods (`lowerAscii()`, `upperAscii()`, `trim()`),
// and collection operations (`exists()`, `all()`, `filter()`).
//
// ### Works With
// - Creates [ResourceGrant](#resourcegrant) objects whose `allowances[].resourceType` must exist in a [ResourceRegistration](#resourceregistration).
// - May target a parent control plane via `spec.target.parentContext` for cross-plane quota allocation.
// - Policy readiness (`status.conditions[type=Ready]`) signals expression/constraint validity.
//
// ### Status
// - `status.conditions[type=Ready]`: Policy validated and active.
// - `status.conditions[type=ParentContextReady]`: Cross‑cluster targeting is resolvable.
// - `status.observedGeneration`: Latest spec generation processed.
//
// ### Selectors and Filtering
//   - Field selectors (server-side):
//     `spec.trigger.resource.kind`, `spec.trigger.resource.apiVersion`,
//     `spec.target.parentContext.kind`, `spec.target.parentContext.apiGroup`.
//   - Label selectors (add your own):
//   - `quota.miloapis.com/trigger-kind`: `Organization`
//   - `quota.miloapis.com/environment`: `prod`
//   - Common queries:
//   - All policies for a trigger kind: label selector `quota.miloapis.com/trigger-kind`.
//   - All active policies: field selector `spec.disabled=false`.
//
// ### Defaults and Limits
// - Resource grant allowances are static (no expression-based amounts) in `v1alpha1`.
//
// ### Notes
// - If `ParentContextReady=False`, verify `nameExpression` and referenced attributes.
// - Disabled policies (`spec.disabled=true`) do not create grants.
//
// ### See Also
// - [ResourceGrant](#resourcegrant): The object created by this policy.
// - [ResourceRegistration](#resourceregistration): Resource types that grants must reference.
// - [ClaimCreationPolicy](#claimcreationpolicy): Creates claims at admission for enforcement.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Trigger",type="string",JSONPath=".spec.trigger.resource.kind"
// +kubebuilder:printcolumn:name="Disabled",type="boolean",JSONPath=".spec.disabled"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +k8s:openapi-gen=true
// +kubebuilder:selectablefield:JSONPath=".spec.trigger.resource.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.trigger.resource.apiVersion"
// +kubebuilder:selectablefield:JSONPath=".spec.target.parentContext.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.target.parentContext.apiGroup"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type GrantCreationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   GrantCreationPolicySpec   `json:"spec"`
	Status GrantCreationPolicyStatus `json:"status,omitempty"`
}

// GrantCreationPolicyList contains a list of GrantCreationPolicy.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type GrantCreationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GrantCreationPolicy `json:"items"`
}

// Validation rules using kubebuilder CEL expressions
//
// +kubebuilder:validation:XValidation:rule="!has(self.spec.disabled) || self.spec.disabled == false || size(self.spec.trigger.constraints) == 0",message="disabled policies should not have trigger constraints"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.target.parentContext) || size(self.spec.target.parentContext.nameExpression) > 0",message="parent context must have a name expression"
// +kubebuilder:validation:XValidation:rule="size(self.spec.target.resourceGrantTemplate.spec.allowances) <= 20",message="maximum 20 allowances per policy"

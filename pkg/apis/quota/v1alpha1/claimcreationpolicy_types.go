package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ClaimCreationPolicySpec defines the desired state of ClaimCreationPolicy.
type ClaimCreationPolicySpec struct {
	// Trigger defines what resource changes should trigger claim creation.
	//
	// +kubebuilder:validation:Required
	Trigger ClaimTriggerSpec `json:"trigger"`
	// Target defines how and where **ResourceClaims** should be created.
	//
	// +kubebuilder:validation:Required
	Target ClaimTargetSpec `json:"target"`
	// Disabled determines if this policy is inactive.
	// If true, no **ResourceClaims** will be created for matching resources.
	//
	// +kubebuilder:default=false
	// +optional
	Disabled *bool `json:"disabled,omitempty"`
}

// ClaimTriggerResource identifies the resource type that triggers this policy.
type ClaimTriggerResource struct {
	// APIVersion of the trigger resource in the format "group/version" or "version" for core resources.
	// Examples: "v1" for core resources like Secret, "resourcemanager.miloapis.com/v1alpha1" for custom resources.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(v[0-9]+((alpha|beta)[0-9]*)?|[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*\/v[0-9]+((alpha|beta)[0-9]*)?)$`
	APIVersion string `json:"apiVersion"`
	// Kind is the kind of the trigger resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
}

// ClaimTriggerSpec defines the resource type and optional conditions for triggering claim creation.
type ClaimTriggerSpec struct {
	// Resource specifies which resource type triggers this policy.
	//
	// +kubebuilder:validation:Required
	Resource ClaimTriggerResource `json:"resource"`
	// Constraints are CEL expressions that must evaluate to true for claim creation to occur.
	// These are pure CEL expressions WITHOUT {{ }} delimiters (unlike template fields).
	// Evaluated in the admission context.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	Constraints []ConditionExpression `json:"constraints,omitempty"`
}

// ClaimTargetSpec defines how **ResourceClaims** are created for a matched trigger.
type ClaimTargetSpec struct {
	// ResourceClaimTemplate defines how to create **ResourceClaims**.
	// String fields support CEL expressions for dynamic content.
	//
	// +kubebuilder:validation:Required
	ResourceClaimTemplate ResourceClaimTemplate `json:"resourceClaimTemplate"`
}

// ResourceClaimTemplate defines how to create **ResourceClaims** using actual **ResourceClaim** structure.
//
// +kubebuilder:validation:XValidation:rule="!has(self.spec.resourceRef)",message="resourceRef field is automatically populated and cannot be set in template"
type ResourceClaimTemplate struct {
	// Metadata for the created **ResourceClaim**.
	// String fields support CEL expressions.
	//
	// +kubebuilder:validation:Required
	Metadata ObjectMetaTemplate `json:"metadata"`
	// Spec for the created ResourceClaim.
	// String fields support CEL expressions.
	//
	// +kubebuilder:validation:Required
	Spec ResourceClaimSpec `json:"spec"`
}

// ObjectMetaTemplate defines metadata fields that support template rendering for created objects.
// Templates can access trigger resource data to generate dynamic names, namespaces, and annotations.
type ObjectMetaTemplate struct {
	// Name specifies the exact name for the created ResourceClaim.
	// Supports CEL expressions wrapped in {{ }} delimiters with access to template variables.
	// Leave empty to use GenerateName for auto-generated names.
	//
	// CEL Expression Syntax: CEL expressions must be enclosed in double curly braces {{ }}.
	// Plain strings without {{ }} are treated as literal values.
	//
	// Template variables available:
	// - trigger: The resource triggering claim creation
	// - requestInfo: Request details (verb, resource, name, etc.)
	// - user: User information (name, uid, groups, extra)
	//
	// Examples:
	// - "{{trigger.metadata.name + '-quota-claim'}}" (CEL expression)
	// - "{{trigger.metadata.name}}-claim" (CEL + literal)
	// - "fixed-claim-name" (literal string)
	//
	// +optional
	Name string `json:"name,omitempty"`

	// GenerateName specifies a prefix for auto-generated names when Name is empty.
	// Kubernetes appends random characters to create unique names.
	// Supports CEL expressions wrapped in {{ }} delimiters.
	//
	// Examples:
	// - "{{trigger.spec.type + '-claim-'}}" (CEL expression)
	// - "{{trigger.spec.type}}-claim-" (CEL + literal)
	// - "quota-claim-" (literal string)
	//
	// +optional
	GenerateName string `json:"generateName,omitempty"`

	// Namespace specifies where the ResourceClaim will be created.
	// Supports CEL expressions wrapped in {{ }} delimiters to derive namespace from trigger resource.
	// Leave empty to create in the same namespace as the trigger resource.
	//
	// Examples:
	// - "{{trigger.metadata.namespace}}" (CEL: same namespace as trigger)
	// - "milo-system" (literal: fixed system namespace)
	// - "{{trigger.spec.organization + '-claims'}}" (CEL: derived namespace)
	//
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Labels specifies static labels to apply to the created ResourceClaim.
	// Values are literal strings (no template processing).
	// The system automatically adds standard labels for policy tracking.
	//
	// Useful for:
	// - Organizing claims by policy or resource type
	// - Adding environment or tier indicators
	// - Enabling label-based queries and monitoring
	//
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations specifies annotations to apply to the created ResourceClaim.
	// Values support CEL expressions wrapped in {{ }} delimiters for dynamic content.
	// The system automatically adds standard annotations for tracking.
	//
	// Template variables available:
	// - trigger: The resource triggering claim creation
	// - requestInfo: Request details
	// - user: User information
	//
	// Examples:
	// - created-for: "{{trigger.metadata.name}}" (CEL expression)
	// - requested-by: "{{user.name}}" (CEL expression)
	// - environment: "production" (literal string)
	//
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ClaimCreationPolicyStatus defines the observed state of ClaimCreationPolicy.
//
// Status fields
// - conditions[type=Ready]: True when the policy is validated and active.
//
// See also
// - [ResourceClaim](#resourceclaim): The object created by this policy.
type ClaimCreationPolicyStatus struct {
	// ObservedGeneration is the most recent generation observed.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the policy's current state.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition type constants for ClaimCreationPolicy.
const (
	// ClaimCreationPolicyReady indicates the policy is ready for use.
	ClaimCreationPolicyReady = "Ready"
	// ClaimCreationPolicyValidationFailed indicates policy validation failed.
	ClaimCreationPolicyValidationFailed = "ValidationFailed"
)

// Condition reason constants for ClaimCreationPolicy.
const (
	// ClaimCreationPolicyReadyReason indicates the policy is ready.
	ClaimCreationPolicyReadyReason = "PolicyReady"
	// ClaimCreationPolicyValidationFailedReason indicates validation failed.
	ClaimCreationPolicyValidationFailedReason = "ValidationFailed"
	// ClaimCreationPolicyDisabledReason indicates the policy is disabled.
	ClaimCreationPolicyDisabledReason = "PolicyDisabled"
)

// Helper method to get the GVK for the trigger resource.
func (t *ClaimTriggerResource) GetGVK() schema.GroupVersionKind {
	gv, _ := schema.ParseGroupVersion(t.APIVersion)
	return schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    t.Kind,
	}
}

// ClaimCreationPolicy automatically creates ResourceClaims during admission to enforce quota in real-time.
// Policies intercept resource creation requests, evaluate trigger conditions, and generate
// quota claims that prevent resource creation when quota limits are exceeded.
//
// ### How It Works
// 1. **Trigger Matching**: Admission webhook matches incoming resource creates against spec.trigger.resource
// 2. **Constraint Evaluation**: All CEL expressions in spec.trigger.constraints must evaluate to true
// 3. **Template Rendering**: Policy renders spec.target.resourceClaimTemplate using available template variables
// 4. **Claim Creation**: System creates the rendered ResourceClaim in the specified namespace
// 5. **Quota Evaluation**: Claim is immediately evaluated against AllowanceBucket capacity
// 6. **Admission Decision**: Original resource creation succeeds or fails based on claim result
//
// ### Policy Processing Flow
// **Active Policies** (spec.disabled=false):
// 1. Admission webhook receives resource creation request
// 2. Finds all ClaimCreationPolicies matching the resource type
// 3. Evaluates trigger constraints for each matching policy
// 4. Creates ResourceClaim for each policy where all constraints are true
// 5. Evaluates all created claims against quota buckets
// 6. Allows resource creation only if all claims are granted
//
// **Disabled Policies** (spec.disabled=true):
// - Completely ignored during admission processing
// - No constraints evaluated, no claims created
// - Useful for temporarily disabling quota enforcement
//
// ### Template Expressions
// Template expressions generate dynamic content for ResourceClaim fields including metadata and specification.
// Content inside `{{ }}` delimiters is evaluated as CEL expressions, while content outside is treated as literal text.
//
// **Template Expression Rules:**
// - `{{expression}}` - Pure CEL expression, evaluated and substituted
// - `literal-text` - Used as-is without any evaluation
// - `{{expression}}-literal` - CEL output combined with literal text
// - `prefix-{{expression}}-suffix` - Literal text surrounding CEL expression
//
// **Template Expression Examples:**
// - `{{trigger.metadata.name + '-claim'}}` - Pure CEL expression (metadata)
// - `{{trigger.metadata.name}}-quota-claim` - CEL + literal suffix (metadata)
// - `{{trigger.spec.organization}}` - Extract spec field for consumer name (spec)
// - `{{trigger.metadata.labels["tier"] + "-tier"}}` - Label-based naming (spec)
// - `fixed-claim-name` - Literal string only (no evaluation)
//
// **Use Template Expressions For:** ResourceClaimTemplate fields (metadata and spec)
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
// - `user.groups.exists(g, g == "admin")` - User authorization check
// - `has(trigger.spec.quotaProfile)` - Field existence check
//
// **Use Constraint Expressions For:** spec.trigger.constraints fields
//
// ### Expression Variables
// Both template and constraint expressions have access to the same context variables:
//
// **trigger**: The complete resource that triggered the policy, including all metadata, spec,
// and status fields. Navigate using CEL property access: `trigger.metadata.name`, `trigger.spec.replicas`.
//
// **user**: Authentication context providing access to the requester's name, unique identifier,
// group memberships, and additional attributes. Enables user-based quota policies.
//
// **requestInfo**: Operational context including the API verb being performed and resource type
// being manipulated. Useful for distinguishing between create, update, and delete operations.
//
// **CEL Functions**: Standard CEL functions available for data manipulation including conditional
// expressions (`condition ? value1 : value2`), string methods (`lowerAscii()`, `upperAscii()`, `trim()`),
// and collection operations (`exists()`, `all()`, `filter()`).
//
// ### Consumer Resolution
// The system automatically resolves spec.consumerRef for created claims:
// - Uses parent context resolution to find the appropriate consumer
// - Typically resolves to Organization for Project resources, Project for User resources, etc.
// - Consumer must match the ResourceRegistration.spec.consumerType for the requested resource type
//
// ### Validation and Dependencies
// **Policy Validation:**
// - Target resource type must exist and be accessible
// - All resource types in claim specification must have active ResourceRegistrations
// - Consumer resolution must be resolvable for target resources
// - CEL expressions must be syntactically valid
//
// **Runtime Dependencies:**
// - ResourceRegistration must be Active for each requested resource type
// - Triggering resource kind must be listed in ResourceRegistration.spec.claimingResources
// - AllowanceBucket must exist (created automatically when ResourceGrants are active)
//
// ### Policy Lifecycle
// 1. **Creation**: Administrator creates ClaimCreationPolicy
// 2. **Validation**: System validates target resource and expressions
// 3. **Activation**: System sets Ready=True when validation passes
// 4. **Operation**: Admission webhook uses active policies to create claims
// 5. **Updates**: Changes trigger re-validation; only Ready policies are used
//
// ### Status Conditions
// - **Ready=True**: Policy is validated and actively creating claims
// - **Ready=False, reason=ValidationFailed**: Configuration errors prevent activation (check message)
// - **Ready=False, reason=PolicyDisabled**: Policy is disabled (spec.disabled=true)
//
// ### Automatic Claim Features
// Claims created by ClaimCreationPolicy include:
// - **Standard Labels**: quota.miloapis.com/auto-created=true, quota.miloapis.com/policy=<policy-name>
// - **Standard Annotations**: quota.miloapis.com/created-by=claim-creation-plugin, timestamps
// - **Owner References**: Set to triggering resource when possible for lifecycle management
// - **Cleanup**: Automatically cleaned up when denied to prevent accumulation
//
// ### Field Constraints and Limits
// - Maximum 10 constraints per trigger (spec.trigger.constraints)
// - Static amounts only in v1alpha1 (no expression-based quota amounts)
// - Template metadata labels are literal strings (no expression processing)
// - Template annotation values support CEL expressions
//
// ### Selectors and Filtering
// - **Field selectors**: spec.trigger.resource.kind, spec.trigger.resource.apiVersion, spec.disabled
// - **Recommended labels** (add manually):
//   - quota.miloapis.com/target-kind: Project
//   - quota.miloapis.com/environment: production
//   - quota.miloapis.com/tier: premium
//
// ### Common Queries
// - All policies for a resource kind: label selector quota.miloapis.com/target-kind=<kind>
// - Active policies only: field selector spec.disabled=false
// - Environment-specific policies: label selector quota.miloapis.com/environment=<env>
// - Failed policies: filter by status.conditions[type=Ready].status=False
//
// ### Troubleshooting
// - **Policy not triggering**: Check spec.disabled=false and status.conditions[type=Ready]=True
// - **Template errors**: Review status condition message for CEL expression syntax issues
// - **CEL expression failures**: Validate expression syntax and available variables
// - **Claims not created**: Verify trigger constraints match the incoming resource
// - **Consumer resolution errors**: Check parent context resolution and ResourceRegistration setup
//
// ### Performance Considerations
// - Policies are evaluated synchronously during admission (affects API latency)
// - Complex CEL expressions can impact admission performance
// - Template rendering occurs for every matching admission request
// - Consider using specific trigger constraints to limit policy evaluation scope
//
// ### Security Considerations
// - Templates can access complete trigger resource data (sensitive field exposure)
// - CEL expressions have access to user information and request details
// - Only trusted administrators should create or modify policies
// - Review template output to ensure no sensitive data leakage in claim metadata
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type="string",JSONPath=".spec.trigger.resource.kind"
// +kubebuilder:printcolumn:name="Disabled",type="boolean",JSONPath=".spec.disabled"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +k8s:openapi-gen=true
// +kubebuilder:selectablefield:JSONPath=".spec.trigger.resource.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.trigger.resource.apiVersion"
// +kubebuilder:selectablefield:JSONPath=".spec.disabled"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type ClaimCreationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   ClaimCreationPolicySpec   `json:"spec"`
	Status ClaimCreationPolicyStatus `json:"status,omitempty"`
}

// ClaimCreationPolicyList contains a list of ClaimCreationPolicy.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type ClaimCreationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClaimCreationPolicy `json:"items"`
}

// Validation rules
//
// Note: In v1alpha1, ResourceClaim amounts are static integers. Expression-based amounts
// are not supported in the claim specification.

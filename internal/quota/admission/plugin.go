package admission

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/apiserver/pkg/warning"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/metrics"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	"k8s.io/kubernetes/pkg/api/legacyscheme"

	"go.miloapis.com/milo/internal/quota/engine"
	"go.miloapis.com/milo/internal/quota/validation"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
)

const (
	// PluginName is the name of the admission plugin.
	PluginName = "ResourceQuotaEnforcement"

	// ClaimWaitTimeout is the maximum time to wait for a ResourceClaim to be granted
	ClaimWaitTimeout = 30 * time.Second
)

// Metrics for quota admission decisions. Registered once at init.
var (
	admissionResultTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota",
			Name:           "admission_result_total",
			Help:           "Total quota admission decisions by outcome, policy, policy namespace, and resource type.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"result", "policy_name", "policy_namespace", "resource_group", "resource_kind"},
	)

	// claimResolutionTotal tracks how often each pre-create resolution path
	// fires for existing claims at the predetermined name. This provides
	// visibility into stale-claim behavior that would otherwise be hidden.
	claimResolutionTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota",
			Name:           "admission_existing_claim_resolution_total",
			Help:           "Total resolutions of existing ResourceClaims encountered during admission (granted=short-circuit, denied=conflict returned, deleted_pending=stale claim removed, create_conflict=Create returned AlreadyExists).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"resolution"},
	)
)

func init() {
	// Register metrics with Kubernetes legacy registry so they are exposed on the apiserver /metrics.
	legacyregistry.MustRegister(admissionResultTotal)
	legacyregistry.MustRegister(claimResolutionTotal)
}

// ResourceQuotaEnforcementPlugin enforces quota by creating ResourceClaims for applicable resources.
//
// CRDs are always decoded as *unstructured.Unstructured by apiextensions-apiserver regardless
// of scheme registration. Validation converts to typed structs via runtime.DefaultUnstructuredConverter.
type ResourceQuotaEnforcementPlugin struct {
	*admission.Handler
	dynamicClient                 dynamic.Interface
	loopbackConfig                *rest.Config
	projectClients                sync.Map // map[string]dynamic.Interface (cached project clients)
	policyEngine                  engine.PolicyEngine
	templateEngine                engine.TemplateEngine
	resourceClaimValidator        validation.ResourceClaimValidator
	resourceRegistrationValidator *validation.ResourceRegistrationValidator
	claimCreationPolicyValidator  *validation.ClaimCreationPolicyValidator
	grantCreationPolicyValidator  *validation.GrantCreationPolicyValidator
	resourceGrantValidator        *validation.ResourceGrantValidator

	// The plugin uses the resource type validator to prevent the apiserver from
	// being marked ready until the resource type's cache has been synced.
	resourceTypeValidator validation.ResourceTypeValidator

	watchManagers sync.Map // map[string]ClaimWatchManager (projectID -> watch manager, "" = root)
	config        *AdmissionPluginConfig
	logger        logr.Logger
}

// Ensure ResourceQuotaEnforcementPlugin implements the required initializer interfaces
var _ initializer.WantsDynamicClient = &ResourceQuotaEnforcementPlugin{}
var _ admission.ValidationInterface = &ResourceQuotaEnforcementPlugin{}
var _ admission.InitializationValidator = &ResourceQuotaEnforcementPlugin{}

// NewResourceQuotaEnforcementPlugin creates a new ResourceQuotaEnforcementPlugin.
func NewResourceQuotaEnforcementPlugin() (*ResourceQuotaEnforcementPlugin, error) {
	logger := klog.NewKlogr().WithName("resource-quota-enforcement-plugin")
	klog.V(1).InfoS("Creating ResourceQuotaEnforcement admission plugin instance")

	// Create the admission plugin - tracer will be initialized when TracerProvider is injected
	plugin := &ResourceQuotaEnforcementPlugin{
		Handler: admission.NewHandler(admission.Create),
		config:  DefaultAdmissionPluginConfig(),
		logger:  logger,
	}

	return plugin, nil
}

// SetDynamicClient implements initializer.WantsDynamicClient
func (p *ResourceQuotaEnforcementPlugin) SetDynamicClient(dynamicClient dynamic.Interface) {
	p.dynamicClient = dynamicClient
	p.logger.V(2).Info("Dynamic client set", "plugin", PluginName)

	if dynamicClient != nil && p.policyEngine == nil {
		p.initializeEngines()
	}
}

// SetLoopbackConfig enables project virtualization by allowing dynamic client creation
// for each project's control plane.
func (p *ResourceQuotaEnforcementPlugin) SetLoopbackConfig(cfg *rest.Config) {
	p.loopbackConfig = cfg
	p.logger.V(2).Info("Loopback config injected", "plugin", PluginName)
}

// ValidateInitialization implements admission.InitializationValidator
func (p *ResourceQuotaEnforcementPlugin) ValidateInitialization() error {
	if p.dynamicClient == nil {
		return fmt.Errorf("dynamic client not initialized")
	}
	if p.policyEngine == nil {
		return fmt.Errorf("policy engine not initialized")
	}
	if p.templateEngine == nil {
		return fmt.Errorf("template engine not initialized")
	}
	if p.resourceClaimValidator == nil {
		return fmt.Errorf("resource claim validator not initialized")
	}
	if p.resourceRegistrationValidator == nil {
		return fmt.Errorf("resource registration validator not initialized")
	}
	if p.claimCreationPolicyValidator == nil {
		return fmt.Errorf("claim creation policy validator not initialized")
	}
	if p.grantCreationPolicyValidator == nil {
		return fmt.Errorf("grant creation policy validator not initialized")
	}
	if p.resourceGrantValidator == nil {
		return fmt.Errorf("resource grant validator not initialized")
	}
	return nil
}

// initializeEngines initializes the policy and template engines
func (p *ResourceQuotaEnforcementPlugin) initializeEngines() {
	p.logger.V(2).Info("Initializing engines for admission plugin")

	celEngine, err := engine.NewCELEngine()
	if err != nil {
		p.logger.Error(err, "Failed to create CEL engine")
		return
	}

	p.templateEngine = engine.NewTemplateEngine(celEngine, p.logger.WithName("template"))
	p.policyEngine = engine.NewPolicyEngine(p.dynamicClient, p.logger)
	p.resourceTypeValidator = validation.NewResourceTypeValidator(p.dynamicClient)
	p.resourceClaimValidator = validation.NewResourceClaimValidator(p.dynamicClient, p.resourceTypeValidator)
	p.resourceRegistrationValidator = validation.NewResourceRegistrationValidator(p.resourceTypeValidator)

	celValidator, err := validation.NewCELValidator()
	if err != nil {
		p.logger.Error(err, "Failed to create CEL validator")
		return
	}

	grantTemplateValidator, err := validation.NewGrantTemplateValidator(p.resourceTypeValidator)
	if err != nil {
		p.logger.Error(err, "Failed to create grant template validator")
		return
	}

	p.claimCreationPolicyValidator = validation.NewClaimCreationPolicyValidator(p.resourceTypeValidator)
	p.grantCreationPolicyValidator = validation.NewGrantCreationPolicyValidator(celValidator, grantTemplateValidator)
	p.resourceGrantValidator = validation.NewResourceGrantValidator(p.resourceTypeValidator)

	go func() {
		if err := p.policyEngine.Start(context.Background()); err != nil {
			p.logger.Error(err, "Failed to start policy engine")
		}
	}()

	p.logger.V(2).Info("Engines initialized successfully")
}

// getClient routes to infrastructure or project-scoped clients based on request context.
func (p *ResourceQuotaEnforcementPlugin) getClient(ctx context.Context) (dynamic.Interface, error) {
	projectID, ok := milorequest.ProjectID(ctx)
	if !ok || projectID == "" {
		return p.dynamicClient, nil
	}
	return p.getProjectClient(projectID)
}

// getProjectClient creates or retrieves a cached client for a project's virtual control plane.
func (p *ResourceQuotaEnforcementPlugin) getProjectClient(projectID string) (dynamic.Interface, error) {
	if cached, ok := p.projectClients.Load(projectID); ok {
		return cached.(dynamic.Interface), nil
	}

	if p.loopbackConfig == nil {
		return nil, fmt.Errorf("loopback config not initialized for project client creation")
	}

	cfg := rest.CopyConfig(p.loopbackConfig)

	// Host field supports URL paths to route requests to project control planes
	projectPath := fmt.Sprintf("/apis/resourcemanager.miloapis.com/v1alpha1/projects/%s/control-plane", projectID)

	if strings.HasPrefix(cfg.Host, "http://") || strings.HasPrefix(cfg.Host, "https://") {
		cfg.Host = cfg.Host + projectPath
	} else {
		cfg.Host = cfg.Host + projectPath
	}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create project dynamic client for project %s: %w", projectID, err)
	}

	actual, _ := p.projectClients.LoadOrStore(projectID, client)
	p.logger.V(3).Info("Created project-specific dynamic client", "project", projectID, "path", projectPath)
	return actual.(dynamic.Interface), nil
}

// getWatchManager returns a project-scoped watch manager, blocking until ready.
func (p *ResourceQuotaEnforcementPlugin) getWatchManager(ctx context.Context) (ClaimWatchManager, error) {
	projectID, _ := milorequest.ProjectID(ctx)

	if cached, ok := p.watchManagers.Load(projectID); ok {
		return cached.(ClaimWatchManager), nil
	}

	var client dynamic.Interface
	var err error
	if projectID == "" {
		client = p.dynamicClient
	} else {
		client, err = p.getProjectClient(projectID)
		if err != nil {
			return nil, fmt.Errorf("failed to get project client for watch manager: %w", err)
		}
	}

	logger := p.logger.WithName("watch-manager")
	if projectID != "" {
		logger = logger.WithValues("project", projectID)
	}

	wm := NewWatchManager(client, logger, projectID)

	if wmWithCallback, ok := wm.(*watchManager); ok {
		wmWithCallback.SetTTLExpiredCallback(func() {
			p.logger.Info("Watch manager TTL expired, removing from cache",
				"project", projectID)
			p.watchManagers.Delete(projectID)
		})
	}

	// Dedicated timeout prevents admission request timeout from affecting watch manager startup
	startupTimeout := 30 * time.Second
	startupCtx, startupCancel := context.WithTimeout(context.Background(), startupTimeout)
	defer startupCancel()

	startChan := make(chan error, 1)
	go func() {
		startChan <- wm.Start(startupCtx)
	}()

	select {
	case err := <-startChan:
		if err != nil {
			return nil, fmt.Errorf("failed to start watch manager: %w", err)
		}
	case <-startupCtx.Done():
		return nil, fmt.Errorf("watch manager startup timed out after %v: %w", startupTimeout, startupCtx.Err())
	}

	actual, _ := p.watchManagers.LoadOrStore(projectID, wm)
	if projectID == "" {
		p.logger.V(2).Info("Created and started watch manager")
	} else {
		p.logger.V(2).Info("Created and started watch manager",
			"project", projectID)
	}
	return actual.(ClaimWatchManager), nil
}

// Validate implements admission.ValidationInterface and orchestrates the main admission flow
func (p *ResourceQuotaEnforcementPlugin) Validate(ctx context.Context, attrs admission.Attributes, _ admission.ObjectInterfaces) error {
	projectID, _ := milorequest.ProjectID(ctx)

	p.logger.V(3).Info("ResourceQuotaEnforcement admission plugin triggered",
		"project", projectID,
		"operation", attrs.GetOperation(),
		"resource.group", attrs.GetKind().Group,
		"resource.version", attrs.GetKind().Version,
		"resource.kind", attrs.GetKind().Kind,
		"name", attrs.GetName(),
		"namespace", attrs.GetNamespace(),
		"user", attrs.GetUserInfo().GetName(),
		"dryRun", attrs.IsDryRun(),
	)

	// Route to appropriate handler based on resource type
	if attrs.GetKind().Group == "quota.miloapis.com" {
		switch attrs.GetKind().Kind {
		case "ResourceClaim":
			return p.validateResourceClaim(ctx, attrs)
		case "ResourceRegistration":
			return p.validateResourceRegistration(ctx, attrs)
		case "ClaimCreationPolicy":
			return p.validateClaimCreationPolicy(ctx, attrs)
		case "GrantCreationPolicy":
			return p.validateGrantCreationPolicy(ctx, attrs)
		case "ResourceGrant":
			return p.validateResourceGrant(ctx, attrs)
		}
	}

	// Only handle CREATE operations for other resources
	if attrs.GetOperation() != admission.Create {
		p.logger.V(4).Info("Skipping non-CREATE operation", "operation", attrs.GetOperation())
		return nil
	}

	// Skip dry run requests to avoid creating ResourceClaims during validation
	if attrs.IsDryRun() {
		return nil
	}

	return p.handleResourceQuotaEnforcement(ctx, attrs)
}

// handleResourceQuotaEnforcement enforces resource quotas by creating and validating ResourceClaims.
func (p *ResourceQuotaEnforcementPlugin) handleResourceQuotaEnforcement(ctx context.Context, attrs admission.Attributes) error {
	projectID, _ := milorequest.ProjectID(ctx)

	spanAttrs := []trace.SpanStartOption{
		trace.WithAttributes(
			attribute.String("operation", string(attrs.GetOperation())),
			attribute.String("resource.name", attrs.GetName()),
			attribute.String("resource.namespace", attrs.GetNamespace()),
			attribute.String("resource.group", attrs.GetKind().Group),
			attribute.String("resource.version", attrs.GetKind().Version),
			attribute.String("resource.kind", attrs.GetKind().Kind),
			attribute.String("user.name", attrs.GetUserInfo().GetName()),
			attribute.Bool("dry_run", attrs.IsDryRun()),
		),
	}

	// Include parent context attributes when executing in a project control plane.
	if projectID != "" {
		spanAttrs = append(spanAttrs, trace.WithAttributes(
			attribute.String("parent.kind", "Project"),
			attribute.String("parent.name", projectID),
			attribute.String("parent.api_group", "resourcemanager.miloapis.com"),
		))
	}

	ctx, span := p.startSpan(ctx, "quota.admission.ResourceQuotaEnforcement", spanAttrs...)
	defer span.End()

	gvk := schema.GroupVersionKind{
		Group:   attrs.GetKind().Group,
		Version: attrs.GetKind().Version,
		Kind:    attrs.GetKind().Kind,
	}

	// Look up policy for this resource type
	policy, err := p.lookupPolicyForResource(ctx, gvk)
	if err != nil {
		p.logger.Error(err, "Failed to get policy for GVK", "gvk", gvk)
		warning.AddWarning(ctx, "", fmt.Sprintf("Failed to get ClaimCreationPolicy for %v: %v", gvk, err))
		return err
	}

	if policy == nil {
		// No policy for this resource type - allow without ResourceClaim creation
		p.logger.V(3).Info("No policy found for GVK, skipping ResourceClaim creation", "gvk", gvk)
		return nil
	}

	// Check if policy is disabled
	if policy.Spec.Disabled != nil && *policy.Spec.Disabled {
		// Record policy disabled decision with full context
		admissionResultTotal.WithLabelValues("policy_disabled", policy.Name, policy.Namespace,
			gvk.Group, gvk.Kind).Inc()

		p.logger.V(3).Info("Policy is disabled, skipping ResourceClaim creation",
			"policy", policy.Name,
			"gvk", gvk)
		return nil
	}

	// Process the resource with the policy
	return p.processResourceWithPolicy(ctx, attrs, policy, gvk)
}

// lookupPolicyForResource retrieves the policy for a given GVK with tracing
func (p *ResourceQuotaEnforcementPlugin) lookupPolicyForResource(ctx context.Context, gvk schema.GroupVersionKind) (*quotav1alpha1.ClaimCreationPolicy, error) {

	// Get the policy for this GVK with tracing
	_, policySpan := p.startSpan(ctx, "quota.admission.ResourceQuotaEnforcement.policyLookup",
		trace.WithAttributes(
			attribute.String("gvk.group", gvk.Group),
			attribute.String("gvk.version", gvk.Version),
			attribute.String("gvk.kind", gvk.Kind),
		))
	defer policySpan.End()

	policy, err := p.policyEngine.GetPolicyForGVK(gvk)
	if err != nil {
		policySpan.RecordError(err)
		policySpan.SetStatus(codes.Error, fmt.Sprintf("Failed to get policy for GVK: %v", err))
		return nil, err
	}

	policySpan.SetAttributes(
		attribute.Bool("policy.found", policy != nil),
	)
	if policy != nil {
		policySpan.SetAttributes(
			attribute.String("policy.name", policy.Name),
			attribute.Bool("policy.disabled", policy.Spec.Disabled != nil && *policy.Spec.Disabled),
		)
	}

	return policy, nil
}

// processResourceWithPolicy creates a ResourceClaim and blocks until quota is granted or denied.
// Waiter registration precedes claim creation to prevent race conditions with watch events.
func (p *ResourceQuotaEnforcementPlugin) processResourceWithPolicy(ctx context.Context, attrs admission.Attributes, policy *quotav1alpha1.ClaimCreationPolicy, gvk schema.GroupVersionKind) error {
	// Get the object - it may be structured (native k8s types) or unstructured (CRDs)
	obj := attrs.GetObject()
	if obj == nil {
		return fmt.Errorf("admission object is nil")
	}

	// Convert to unstructured if needed
	var unstructuredObj *unstructured.Unstructured
	var err error

	switch v := obj.(type) {
	case *unstructured.Unstructured:
		// Already unstructured (CRDs from apiextensions-apiserver)
		unstructuredObj = v
	default:
		// Structured type (native k8s types) — the admission handler decodes
		// these as internal Go types (e.g. pkg/apis/discovery.EndpointSlice),
		// not the external versioned types (e.g. api/discovery/v1.EndpointSlice).
		// Internal types inline ObjectMeta without a "metadata" wrapper, so
		// both json.Marshal and ToUnstructured produce maps without a "metadata"
		// key. Convert to the external versioned type first using the scheme,
		// then use ToUnstructured to get proper Kubernetes JSON structure.
		toConvert := obj
		targetGV := schema.GroupVersion{Group: gvk.Group, Version: gvk.Version}
		if versioned, convErr := legacyscheme.Scheme.ConvertToVersion(obj, targetGV); convErr == nil {
			toConvert = versioned
		}
		unstructuredMap, convErr := runtime.DefaultUnstructuredConverter.ToUnstructured(toConvert)
		if convErr != nil {
			return fmt.Errorf("failed to convert %T to unstructured: %w", toConvert, convErr)
		}
		unstructuredObj = &unstructured.Unstructured{Object: unstructuredMap}
	}

	// Build evaluation context
	evalContext := p.buildEvaluationContext(attrs, unstructuredObj)

	// Evaluate trigger constraints to determine if this resource should trigger the policy
	constraintsMet, err := p.templateEngine.EvaluateConditions(policy.Spec.Trigger.Constraints, unstructuredObj)
	if err != nil {
		p.logger.Error(err, "Failed to evaluate policy constraints",
			"policy", policy.Name,
			"resourceName", attrs.GetName())
		warning.AddWarning(ctx, "", fmt.Sprintf("Failed to evaluate policy constraints: %v", err))
		return nil // Don't block resource creation on constraint evaluation errors
	}

	if !constraintsMet {
		// Policy constraints not met - skip ResourceClaim creation
		p.logger.V(3).Info("Policy constraints not met, skipping ResourceClaim creation",
			"policy", policy.Name,
			"resourceName", attrs.GetName(),
			"gvk", gvk)
		return nil
	}

	p.logger.V(2).Info("Policy constraints met, creating ResourceClaim based on policy",
		"policy", policy.Name,
		"resourceName", attrs.GetName())

	// Create the ResourceClaim and wait for it to be granted
	if err := p.createAndWaitForResourceClaim(ctx, attrs, policy, evalContext); err != nil {
		// ResourceClaim creation or granting failed - block the resource creation

		failure, ok := asClaimFailure(err)
		if !ok {
			// Unclassified errors bubble up as an infrastructure failure.
			failure = newInternalFailure(err)
		}

		outcome := admissionOutcomeFor(failure.kind)
		admissionResultTotal.WithLabelValues(outcome, policy.Name, policy.Namespace,
			evalContext.GVK.Group, evalContext.GVK.Kind).Inc()

		p.logger.Error(err, "ResourceClaim not granted, denying resource creation",
			"policy", policy.Name,
			"resourceName", attrs.GetName(),
			"gvk", gvk,
			"failureKind", outcome)

		// Map the failure kind to a user-facing error. All paths return 403
		// Forbidden (consistent with core Kubernetes quota behavior) but the
		// message distinguishes the root cause so callers can take the right
		// action.
		gr := schema.GroupResource{Group: gvk.Group, Resource: attrs.GetResource().Resource}
		return errors.NewForbidden(gr, attrs.GetName(), userFacingClaimError(failure))
	}

	// Record granted admission decision with full context
	admissionResultTotal.WithLabelValues("granted", policy.Name, policy.Namespace,
		evalContext.GVK.Group, evalContext.GVK.Kind).Inc()

	p.logger.V(2).Info("ResourceClaim granted, allowing resource creation",
		"policy", policy.Name,
		"resourceName", attrs.GetName())

	return nil // Allow original resource creation only if claim is granted
}

// admissionOutcomeFor maps a claim failure kind to the label emitted on
// admission_result_total so operators can tell denials apart from timeouts
// and infrastructure errors in metrics.
func admissionOutcomeFor(kind claimFailureKind) string {
	switch kind {
	case claimFailureDenied:
		return "denied"
	case claimFailureTimeout:
		return "timeout"
	case claimFailureConflict:
		return "conflict"
	default:
		return "internal_error"
	}
}

// userFacingClaimError converts a typed claim failure into the error surfaced
// in the 403 Forbidden response body.
//
// Tone guidelines for these messages:
//   - Explain what happened in plain language (no "admission", "eval", etc).
//   - Tell the user what they can do next (retry, free capacity, get help).
//   - Keep it to one or two sentences.
//   - Capitalize the leading word because the message is shown verbatim in
//     kubectl / API client output.
func userFacingClaimError(f *claimFailure) error {
	switch f.kind {
	case claimFailureDenied:
		if f.message != "" {
			//lint:ignore ST1005 user-facing message
			return fmt.Errorf("You've reached your quota for this resource type (%s). Delete unused resources to free up capacity, or contact support to request a higher limit.", f.message)
		}
		//lint:ignore ST1005 user-facing message
		return fmt.Errorf("You've reached your quota for this resource type. Delete unused resources to free up capacity, or contact support to request a higher limit.")
	case claimFailureTimeout:
		//lint:ignore ST1005 user-facing message
		return fmt.Errorf("Your request took too long to be checked against your quota. Please try again in a moment — if this keeps happening, contact support.")
	case claimFailureConflict:
		if f.message != "" {
			//lint:ignore ST1005 user-facing message
			return fmt.Errorf("We're still cleaning up from a previous attempt to create this resource (%s). Please try again in a few seconds.", f.message)
		}
		//lint:ignore ST1005 user-facing message
		return fmt.Errorf("We're still cleaning up from a previous attempt to create this resource. Please try again in a few seconds.")
	default:
		//lint:ignore ST1005 user-facing message
		return fmt.Errorf("Something went wrong while checking your quota for this request. Please try again — if this keeps happening, contact support.")
	}
}

// createAndWaitForResourceClaim creates a ResourceClaim and blocks until the claim is resolved.
// The waiter is registered before claim creation to prevent missed events.
//
// All error returns are *claimFailure so the caller can produce precise
// user-facing messages and metrics labels.
func (p *ResourceQuotaEnforcementPlugin) createAndWaitForResourceClaim(ctx context.Context, attrs admission.Attributes, policy *quotav1alpha1.ClaimCreationPolicy, evalContext *EvaluationContext) error {
	ctx, span := p.startSpan(ctx, "quota.admission.ResourceQuotaEnforcement.createAndWaitForResourceClaim",
		trace.WithAttributes(
			attribute.String("policy.name", policy.Name),
			attribute.String("resource.name", evalContext.Object.GetName()),
			attribute.String("resource.namespace", evalContext.Object.GetNamespace()),
		))
	defer span.End()

	watchManager, err := p.getWatchManager(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to get watch manager")
		return newInternalFailure(fmt.Errorf("failed to get watch manager: %w", err))
	}

	// Determine claim name (must be deterministic to pre-register waiter before claim creation).
	claimName, err := p.determineClaimName(evalContext, policy)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to determine claim name")
		return newInternalFailure(fmt.Errorf("failed to determine claim name: %w", err))
	}
	namespace := p.getClaimNamespace(policy, evalContext)

	span.SetAttributes(
		attribute.String("claim.name", claimName),
		attribute.String("claim.namespace", namespace),
	)

	p.logger.V(2).Info("Determined claim name",
		"claimName", claimName,
		"namespace", namespace,
		"policy", policy.Name,
		"resourceName", evalContext.Object.GetName())

	// Register waiter before claim exists to ensure watch stream catches the ADDED event.
	timeout := p.config.WatchManager.DefaultTimeout
	resultChan, cancelFunc, err := watchManager.RegisterClaimWaiter(ctx, claimName, namespace, timeout)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to register waiter")
		return newInternalFailure(fmt.Errorf("failed to register waiter: %w", err))
	}
	defer cancelFunc()

	p.logger.V(2).Info("Waiter registered before claim creation",
		"claimName", claimName,
		"namespace", namespace,
		"timeout", timeout)

	// createResourceClaim either creates a fresh ResourceClaim, returns a
	// typed *claimFailure for a real failure, or returns an errAlreadyGranted
	// sentinel when an existing Granted claim with the same name already
	// reserves capacity for this resource.
	createErr := p.createResourceClaim(ctx, attrs, policy, evalContext, claimName, namespace)
	switch {
	case createErr == nil:
		// Claim was created fresh; fall through to wait for the controller.
	case createErr == errAlreadyGranted:
		span.SetAttributes(attribute.String("claim.result", "short_circuit_granted"))
		p.logger.V(2).Info("Existing ResourceClaim already granted; short-circuiting admission",
			"claimName", claimName,
			"namespace", namespace)
		return nil
	default:
		span.RecordError(createErr)
		span.SetStatus(codes.Error, "Failed to create ResourceClaim")
		if _, ok := asClaimFailure(createErr); ok {
			return createErr
		}
		return newInternalFailure(createErr)
	}

	p.logger.V(2).Info("ResourceClaim created with predetermined name",
		"claimName", claimName,
		"namespace", namespace)

	// Wait for result from watch stream.
	select {
	case result, ok := <-resultChan:
		if !ok {
			span.SetStatus(codes.Error, "Result channel closed")
			return newInternalFailure(fmt.Errorf("result channel closed unexpectedly"))
		}

		// Timeouts and deletions carry result.Error; denials do not (they
		// come back as Granted=false with a denial Reason/message).
		if result.Error != nil {
			span.RecordError(result.Error)
			span.SetStatus(codes.Error, "Wait failed")
			if result.Reason == "timeout" {
				return newTimeoutFailure(result.Error)
			}
			return newInternalFailure(result.Error)
		}

		if result.Granted {
			span.SetAttributes(
				attribute.String("claim.result", "granted"),
			)
			p.logger.V(2).Info("ResourceClaim granted",
				"claimName", claimName,
				"namespace", namespace)
			return nil
		}

		span.SetAttributes(
			attribute.String("claim.result", "denied"),
			attribute.String("claim.denial_reason", result.Reason),
		)
		p.logger.Info("ResourceClaim denied",
			"claimName", claimName,
			"namespace", namespace,
			"reason", result.Reason)
		return newDeniedFailure(quotav1alpha1.ResourceClaimDeniedReason, result.Reason)

	case <-ctx.Done():
		span.SetStatus(codes.Error, "Context cancelled")
		watchManager.UnregisterClaimWaiter(claimName, namespace)
		return newInternalFailure(ctx.Err())
	}
}


// getClaimNamespace determines the namespace for a ResourceClaim.
// If the policy template specifies a namespace containing CEL expressions,
// the template is rendered to evaluate those expressions. Otherwise, the
// namespace from the triggering resource is used.
func (p *ResourceQuotaEnforcementPlugin) getClaimNamespace(policy *quotav1alpha1.ClaimCreationPolicy, evalContext *EvaluationContext) string {
	if policy.Spec.Target.ResourceClaimTemplate.Metadata.Namespace != "" {
		engineContext := p.convertToEngineContext(evalContext)
		claim, err := p.templateEngine.RenderClaim(policy, engineContext)
		if err != nil {
			p.logger.Error(err, "Failed to render claim template for namespace extraction, using literal value",
				"policy", policy.Name,
				"namespace", policy.Spec.Target.ResourceClaimTemplate.Metadata.Namespace)
			return policy.Spec.Target.ResourceClaimTemplate.Metadata.Namespace
		}
		return claim.Namespace
	}

	return evalContext.Object.GetNamespace()
}

// createResourceClaim creates a ResourceClaim with the specified name and namespace.
// The claim name must be predetermined to allow waiter registration before creation.
//
// Because claim names are deterministic, repeated admission attempts for the
// same resource (for example when a client controller retries) can collide
// with a leftover claim from a prior pass. Before attempting to Create this
// function inspects any existing claim at the same name and:
//
//   - Granted (condition Granted=True) → returns errAlreadyGranted, which the
//     caller treats as admission success. Quota has already been reserved
//     for this resource, so there is nothing more to do.
//   - Denied (Granted=False, reason=QuotaExceeded) → returns a
//     claimFailureConflict. The DeniedAutoClaimCleanupController will remove
//     the stale claim shortly; the user should retry.
//   - Pending / no condition → deletes the stale claim and falls through to
//     Create a fresh one. This covers claims left behind by a previous
//     admission timeout.
//
// Any non-NotFound Get error falls through to Create so that transient API
// read failures do not permanently block admission.
func (p *ResourceQuotaEnforcementPlugin) createResourceClaim(ctx context.Context, attrs admission.Attributes, policy *quotav1alpha1.ClaimCreationPolicy, evalContext *EvaluationContext, claimName, namespace string) error {
	ctx, span := p.startSpan(ctx, "quota.admission.ResourceQuotaEnforcement.createResourceClaim",
		trace.WithAttributes(
			attribute.String("policy.name", policy.Name),
			attribute.String("claim.name", claimName),
			attribute.String("claim.namespace", namespace),
		))
	defer span.End()

	engineContext := p.convertToEngineContext(evalContext)
	claim, err := p.templateEngine.RenderClaim(policy, engineContext)
	if err != nil {
		return fmt.Errorf("failed to render ResourceClaim: %w", err)
	}

	// Use predetermined name/namespace to ensure waiter receives events
	claim.Name = claimName
	claim.Namespace = namespace
	claim.GenerateName = ""

	claim.Spec.ResourceRef = quotav1alpha1.UnversionedObjectReference{
		APIGroup:  evalContext.GVK.Group,
		Kind:      evalContext.GVK.Kind,
		Name:      attrs.GetName(),
		Namespace: attrs.GetNamespace(),
	}

	// Derive consumer from project context when template doesn't specify one
	if claim.Spec.ConsumerRef.Kind == "" || claim.Spec.ConsumerRef.Name == "" {
		projectID, ok := milorequest.ProjectID(ctx)
		if ok && projectID != "" {
			claim.Spec.ConsumerRef = quotav1alpha1.ConsumerRef{
				APIGroup: "resourcemanager.miloapis.com",
				Kind:     "Project",
				Name:     projectID,
			}
		}
	}

	if claim.Labels == nil {
		claim.Labels = make(map[string]string)
	}
	if claim.Annotations == nil {
		claim.Annotations = make(map[string]string)
	}

	claim.Labels["quota.miloapis.com/auto-created"] = "true"
	claim.Labels["quota.miloapis.com/policy"] = policy.Name
	// Use "core" for empty group to match Kubernetes convention
	group := evalContext.GVK.Group
	if group == "" {
		group = "core"
	}
	claim.Labels["quota.miloapis.com/gvk"] = fmt.Sprintf("%s.%s.%s", group, evalContext.GVK.Version, evalContext.GVK.Kind)

	claim.Annotations["quota.miloapis.com/created-by"] = "claim-creation-plugin"
	claim.Annotations["quota.miloapis.com/created-at"] = time.Now().Format(time.RFC3339)
	claim.Annotations["quota.miloapis.com/resource-name"] = evalContext.Object.GetName()
	claim.Annotations["quota.miloapis.com/policy"] = policy.Name

	gvr := schema.GroupVersionResource{
		Group:    "quota.miloapis.com",
		Version:  "v1alpha1",
		Resource: "resourceclaims",
	}

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(claim)
	if err != nil {
		return fmt.Errorf("failed to convert ResourceClaim to unstructured: %w", err)
	}
	unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}

	client, err := p.getClient(ctx)
	if err != nil {
		return newInternalFailure(fmt.Errorf("failed to get client for context: %w", err))
	}

	// Resolve any existing claim at the same deterministic name before
	// attempting to Create. See function doc comment for decision table.
	if action, existing, getErr := p.resolveExistingClaim(ctx, client, gvr, claimName, namespace); getErr == nil {
		switch action {
		case existingClaimGranted:
			claimResolutionTotal.WithLabelValues("granted").Inc()
			return errAlreadyGranted
		case existingClaimDenied:
			claimResolutionTotal.WithLabelValues("denied").Inc()
			denialMsg := extractGrantedConditionMessage(existing)
			return newConflictFailure(denialMsg, nil)
		case existingClaimPending:
			// Stale pending claim - delete so we can create a fresh one. This
			// mirrors what the stale-claim GC controller would eventually do,
			// but doing it inline allows the current admission request to
			// succeed on the fresh claim.
			claimResolutionTotal.WithLabelValues("deleted_pending").Inc()
			if delErr := client.Resource(gvr).Namespace(namespace).Delete(ctx, claimName, metav1.DeleteOptions{}); delErr != nil && !errors.IsNotFound(delErr) {
				p.logger.Info("Failed to delete stale pending ResourceClaim before retry",
					"claimName", claimName,
					"namespace", namespace,
					"error", delErr)
				// Treat as conflict so the user retries once GC catches up.
				return newConflictFailure("unable to clean up stale pending claim", delErr)
			}
			p.logger.V(2).Info("Deleted stale pending ResourceClaim before retry",
				"claimName", claimName,
				"namespace", namespace)
		}
	}

	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, unstructuredObj, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// A concurrent admission pass (or our own Delete above racing
			// with a replace) recreated the claim. Surface as conflict so
			// the user retries.
			claimResolutionTotal.WithLabelValues("create_conflict").Inc()
			return newConflictFailure("concurrent quota evaluation in progress", err)
		}
		return newInternalFailure(fmt.Errorf("failed to create ResourceClaim: %w", err))
	}

	p.logger.V(2).Info("ResourceClaim created successfully",
		"claimName", claimName,
		"namespace", namespace,
		"policy", policy.Name,
	)

	return nil
}

// existingClaimAction classifies how createResourceClaim should handle a
// ResourceClaim that already exists at the predetermined name.
type existingClaimAction int

const (
	// existingClaimAbsent indicates no existing claim (caller should Create).
	existingClaimAbsent existingClaimAction = iota
	// existingClaimGranted indicates capacity is already reserved; admission
	// can short-circuit to allow.
	existingClaimGranted
	// existingClaimDenied indicates a previous denial awaiting GC; the user
	// should retry.
	existingClaimDenied
	// existingClaimPending indicates a stale claim with no final condition;
	// caller should delete and recreate.
	existingClaimPending
)

// resolveExistingClaim inspects the predetermined name for a lingering
// ResourceClaim from a prior admission pass and returns how to proceed.
// Returns (existingClaimAbsent, nil, nil) when no claim exists or when the
// read fails in a way that should fall through to Create.
func (p *ResourceQuotaEnforcementPlugin) resolveExistingClaim(ctx context.Context, client dynamic.Interface, gvr schema.GroupVersionResource, claimName, namespace string) (existingClaimAction, *unstructured.Unstructured, error) {
	existing, err := client.Resource(gvr).Namespace(namespace).Get(ctx, claimName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return existingClaimAbsent, nil, nil
		}
		// Transient read error - log and fall through to Create. The Create
		// will either succeed or return AlreadyExists, which is handled.
		p.logger.V(2).Info("Failed to look up existing ResourceClaim, proceeding to Create",
			"claimName", claimName,
			"namespace", namespace,
			"error", err)
		return existingClaimAbsent, nil, nil
	}

	// Only apply pre-create handling to auto-created claims to avoid
	// interfering with manually created ResourceClaims that happen to share
	// a name. This is defense in depth: claim names are derived from the
	// policy template plus the resource name, so collisions with manual
	// claims should be rare.
	labels := existing.GetLabels()
	if labels["quota.miloapis.com/auto-created"] != "true" {
		return existingClaimAbsent, nil, nil
	}

	switch classifyGrantedCondition(existing) {
	case claimConditionGranted:
		return existingClaimGranted, existing, nil
	case claimConditionDenied:
		return existingClaimDenied, existing, nil
	default:
		return existingClaimPending, existing, nil
	}
}

// claimConditionState is the coarse Granted-condition state used by
// resolveExistingClaim.
type claimConditionState int

const (
	claimConditionPending claimConditionState = iota
	claimConditionGranted
	claimConditionDenied
)

// classifyGrantedCondition reads the Granted condition from a ResourceClaim
// and returns the corresponding state. Missing conditions map to pending.
func classifyGrantedCondition(claim *unstructured.Unstructured) claimConditionState {
	conditions, found, err := unstructured.NestedSlice(claim.Object, "status", "conditions")
	if err != nil || !found {
		return claimConditionPending
	}
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(m, "type")
		if condType != quotav1alpha1.ResourceClaimGranted {
			continue
		}
		status, _, _ := unstructured.NestedString(m, "status")
		reason, _, _ := unstructured.NestedString(m, "reason")
		switch {
		case status == string(metav1.ConditionTrue):
			return claimConditionGranted
		case status == string(metav1.ConditionFalse) && reason == quotav1alpha1.ResourceClaimDeniedReason:
			return claimConditionDenied
		default:
			// Granted=False with any non-denied reason (e.g. PendingEvaluation,
			// ValidationFailed) is not a final state from our perspective.
			return claimConditionPending
		}
	}
	return claimConditionPending
}

// extractGrantedConditionMessage returns the message from the Granted
// condition if present, otherwise the empty string.
func extractGrantedConditionMessage(claim *unstructured.Unstructured) string {
	conditions, found, err := unstructured.NestedSlice(claim.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(m, "type")
		if condType != quotav1alpha1.ResourceClaimGranted {
			continue
		}
		msg, _, _ := unstructured.NestedString(m, "message")
		return msg
	}
	return ""
}

// validateResourceClaim validates ResourceClaim objects when they are created directly
func (p *ResourceQuotaEnforcementPlugin) validateResourceClaim(ctx context.Context, attrs admission.Attributes) error {
	ctx, span := p.startSpan(ctx, "quota.admission.ResourceClaimValidation",
		trace.WithAttributes(
			attribute.String("operation", string(attrs.GetOperation())),
			attribute.String("claim.name", attrs.GetName()),
			attribute.String("claim.namespace", attrs.GetNamespace()),
			attribute.String("user.name", attrs.GetUserInfo().GetName()),
			attribute.Bool("dry_run", attrs.IsDryRun()),
		))
	defer span.End()

	// Only validate CREATE operations for ResourceClaims
	if attrs.GetOperation() != admission.Create {
		span.SetAttributes(attribute.String("validation.status", "skipped"))
		p.logger.V(4).Info("Skipping non-CREATE operation for ResourceClaim", "operation", attrs.GetOperation())
		return nil
	}

	// Get the ResourceClaim object
	obj := attrs.GetObject()
	if obj == nil {
		return nil
	}

	// CRDs always arrive as unstructured from the apiextensions-apiserver
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured.Unstructured for CRD, got %T", obj)
	}

	// Convert to typed struct for validation
	claim := &quotav1alpha1.ResourceClaim{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, claim); err != nil {
		return fmt.Errorf("failed to convert to ResourceClaim: %w", err)
	}

	span.SetAttributes(
		attribute.String("claim.name", claim.Name),
		attribute.String("claim.namespace", claim.Namespace),
		attribute.Int("claim.request_count", len(claim.Spec.Requests)),
	)

	// Validate the resource claim using field-based validation
	// Validate the ResourceClaim using the complete validator
	if errs := p.resourceClaimValidator.Validate(ctx, claim); len(errs) > 0 {
		span.SetAttributes(
			attribute.String("validation.status", "failed"),
			attribute.Int("validation.error_count", len(errs)),
		)
		span.SetStatus(codes.Error, "ResourceClaim validation failed")
		p.logger.Info("ResourceClaim validation failed",
			"name", claim.Name,
			"namespace", claim.Namespace,
			"errors", errs)
		return admission.NewForbidden(attrs, errors.NewInvalid(
			quotav1alpha1.GroupVersion.WithKind("ResourceClaim").GroupKind(),
			claim.Name,
			errs))
	}

	span.SetAttributes(attribute.String("validation.status", "passed"))

	return nil
}

// startSpan safely starts a span using the tracer provider from the context
func (p *ResourceQuotaEnforcementPlugin) startSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	// Get the tracer provider from the existing span context
	tracerProvider := trace.SpanFromContext(ctx).TracerProvider()
	tracer := tracerProvider.Tracer("go.miloapis.com/milo/admission/quota")
	return tracer.Start(ctx, name, opts...)
}

// buildEvaluationContext creates an EvaluationContext from admission attributes.
func (p *ResourceQuotaEnforcementPlugin) buildEvaluationContext(attrs admission.Attributes, obj *unstructured.Unstructured) *EvaluationContext {
	user := UserContext{
		Name:   attrs.GetUserInfo().GetName(),
		UID:    attrs.GetUserInfo().GetUID(),
		Groups: attrs.GetUserInfo().GetGroups(),
		Extra:  attrs.GetUserInfo().GetExtra(),
	}

	// Build RequestInfo from admission attributes
	// Map admission operation to HTTP verb equivalents
	verb := strings.ToLower(string(attrs.GetOperation()))

	requestInfo := &request.RequestInfo{
		IsResourceRequest: true,
		Verb:              verb,
		APIGroup:          attrs.GetKind().Group,
		APIVersion:        attrs.GetKind().Version,
		Namespace:         attrs.GetNamespace(),
		Resource:          attrs.GetResource().Resource,
		Subresource:       attrs.GetSubresource(),
		Name:              attrs.GetName(),
	}

	return &EvaluationContext{
		Object:      obj,
		User:        user,
		RequestInfo: requestInfo,
		Namespace:   attrs.GetNamespace(),
		GVK: schema.GroupVersionKind{
			Group:   attrs.GetKind().Group,
			Version: attrs.GetKind().Version,
			Kind:    attrs.GetKind().Kind,
		},
	}
}

// convertToEngineContext converts admission EvaluationContext to engine EvaluationContext
func (p *ResourceQuotaEnforcementPlugin) convertToEngineContext(admissionCtx *EvaluationContext) *engine.EvaluationContext {
	return &engine.EvaluationContext{
		Object: admissionCtx.Object,
		User: engine.UserContext{
			Name:   admissionCtx.User.Name,
			UID:    admissionCtx.User.UID,
			Groups: admissionCtx.User.Groups,
			Extra:  admissionCtx.User.Extra,
		},
		RequestInfo: admissionCtx.RequestInfo,
		Namespace:   admissionCtx.Namespace,
		GVK: struct {
			Group   string
			Version string
			Kind    string
		}{
			Group:   admissionCtx.GVK.Group,
			Version: admissionCtx.GVK.Version,
			Kind:    admissionCtx.GVK.Kind,
		},
	}
}

// determineClaimName determines the claim name.
// Renders the template first, then uses the name if specified, or generates
// a name using Kubernetes standard name generation for generateName.
func (p *ResourceQuotaEnforcementPlugin) determineClaimName(
	evalContext *EvaluationContext,
	policy *quotav1alpha1.ClaimCreationPolicy,
) (string, error) {
	// Render template to get name/generateName after CEL evaluation
	engineContext := p.convertToEngineContext(evalContext)
	claim, err := p.templateEngine.RenderClaim(policy, engineContext)
	if err != nil {
		return "", fmt.Errorf("failed to render claim template: %w", err)
	}

	// If name is specified in template (after rendering), use it directly
	if claim.Name != "" {
		return claim.Name, nil
	}

	// If generateName is specified, use Kubernetes standard name generation
	if claim.GenerateName != "" {
		return names.SimpleNameGenerator.GenerateName(claim.GenerateName), nil
	}

	// Neither name nor generateName - generate base name from resource and kind
	baseName := fmt.Sprintf("%s-%s-claim-",
		evalContext.Object.GetName(),
		strings.ToLower(evalContext.GVK.Kind))
	return names.SimpleNameGenerator.GenerateName(baseName), nil
}

// validateResourceRegistration validates ResourceRegistration objects for cross-resource duplicates.
func (p *ResourceQuotaEnforcementPlugin) validateResourceRegistration(ctx context.Context, attrs admission.Attributes) error {
	ctx, span := p.startSpan(ctx, "quota.admission.ResourceRegistrationValidation",
		trace.WithAttributes(
			attribute.String("operation", string(attrs.GetOperation())),
			attribute.String("registration.name", attrs.GetName()),
			attribute.String("user.name", attrs.GetUserInfo().GetName()),
		))
	defer span.End()

	// Only validate on CREATE to check for duplicate resourceType
	// Updates are handled by CEL immutability rules
	if attrs.GetOperation() != admission.Create {
		span.SetAttributes(attribute.String("validation.status", "skipped"))
		return nil
	}

	obj := attrs.GetObject()
	if obj == nil {
		return nil
	}

	// CRDs always arrive as unstructured from the apiextensions-apiserver
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured.Unstructured for CRD, got %T", obj)
	}

	// Convert to typed struct for validation
	registration := &quotav1alpha1.ResourceRegistration{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, registration); err != nil {
		return fmt.Errorf("failed to convert to ResourceRegistration: %w", err)
	}

	span.SetAttributes(
		attribute.String("registration.name", registration.Name),
		attribute.String("registration.resourceType", registration.Spec.ResourceType),
	)

	// Validate the ResourceRegistration
	if validationErrs := p.resourceRegistrationValidator.Validate(registration); len(validationErrs) > 0 {
		span.SetAttributes(attribute.String("validation.status", "failed"))
		span.SetStatus(codes.Error, "ResourceRegistration validation failed")

		return admission.NewForbidden(attrs, errors.NewInvalid(
			quotav1alpha1.GroupVersion.WithKind("ResourceRegistration").GroupKind(),
			registration.Name,
			validationErrs,
		))
	}

	span.SetAttributes(attribute.String("validation.status", "passed"))
	return nil
}

// validateClaimCreationPolicy validates ClaimCreationPolicy objects for template syntax and resource types.
func (p *ResourceQuotaEnforcementPlugin) validateClaimCreationPolicy(ctx context.Context, attrs admission.Attributes) error {
	ctx, span := p.startSpan(ctx, "quota.admission.ClaimCreationPolicyValidation",
		trace.WithAttributes(
			attribute.String("operation", string(attrs.GetOperation())),
			attribute.String("policy.name", attrs.GetName()),
			attribute.String("policy.namespace", attrs.GetNamespace()),
			attribute.String("user.name", attrs.GetUserInfo().GetName()),
		))
	defer span.End()

	// Only validate on CREATE - updates can be handled by CEL immutability rules
	if attrs.GetOperation() != admission.Create {
		span.SetAttributes(attribute.String("validation.status", "skipped"))
		return nil
	}

	obj := attrs.GetObject()
	if obj == nil {
		return nil
	}

	// CRDs always arrive as unstructured from the apiextensions-apiserver
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured.Unstructured for CRD, got %T", obj)
	}

	// Convert to typed struct for validation
	policy := &quotav1alpha1.ClaimCreationPolicy{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, policy); err != nil {
		return fmt.Errorf("failed to convert to ClaimCreationPolicy: %w", err)
	}

	span.SetAttributes(
		attribute.String("policy.name", policy.Name),
		attribute.String("policy.namespace", policy.Namespace),
	)

	if validationErrs := p.claimCreationPolicyValidator.Validate(ctx, policy, validation.AdmissionValidationOptions()); len(validationErrs) > 0 {
		span.SetAttributes(attribute.String("validation.status", "failed"))
		span.SetStatus(codes.Error, "ClaimCreationPolicy validation failed")

		p.logger.Info("ClaimCreationPolicy validation failed",
			"name", policy.Name,
			"namespace", policy.Namespace,
			"errors", validationErrs)

		return admission.NewForbidden(attrs, errors.NewInvalid(
			quotav1alpha1.GroupVersion.WithKind("ClaimCreationPolicy").GroupKind(),
			policy.Name,
			validationErrs,
		))
	}

	span.SetAttributes(attribute.String("validation.status", "passed"))
	return nil
}

// validateGrantCreationPolicy validates GrantCreationPolicy objects for CEL expressions and template syntax.
func (p *ResourceQuotaEnforcementPlugin) validateGrantCreationPolicy(ctx context.Context, attrs admission.Attributes) error {
	ctx, span := p.startSpan(ctx, "quota.admission.GrantCreationPolicyValidation",
		trace.WithAttributes(
			attribute.String("operation", string(attrs.GetOperation())),
			attribute.String("policy.name", attrs.GetName()),
			attribute.String("policy.namespace", attrs.GetNamespace()),
			attribute.String("user.name", attrs.GetUserInfo().GetName()),
		))
	defer span.End()

	// Only validate on CREATE - updates can be handled by CEL immutability rules
	if attrs.GetOperation() != admission.Create {
		span.SetAttributes(attribute.String("validation.status", "skipped"))
		return nil
	}

	obj := attrs.GetObject()
	if obj == nil {
		return nil
	}

	// CRDs always arrive as unstructured from the apiextensions-apiserver
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured.Unstructured for CRD, got %T", obj)
	}

	// Convert to typed struct for validation
	policy := &quotav1alpha1.GrantCreationPolicy{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, policy); err != nil {
		return fmt.Errorf("failed to convert to GrantCreationPolicy: %w", err)
	}

	span.SetAttributes(
		attribute.String("policy.name", policy.Name),
		attribute.String("policy.namespace", policy.Namespace),
	)

	if validationErrs := p.grantCreationPolicyValidator.Validate(ctx, policy, validation.AdmissionValidationOptions()); len(validationErrs) > 0 {
		span.SetAttributes(attribute.String("validation.status", "failed"))
		span.SetStatus(codes.Error, "GrantCreationPolicy validation failed")

		p.logger.Info("GrantCreationPolicy validation failed",
			"name", policy.Name,
			"namespace", policy.Namespace,
			"errors", validationErrs)

		return admission.NewForbidden(attrs, errors.NewInvalid(
			quotav1alpha1.GroupVersion.WithKind("GrantCreationPolicy").GroupKind(),
			policy.Name,
			validationErrs,
		))
	}

	span.SetAttributes(attribute.String("validation.status", "passed"))
	return nil
}

// validateResourceGrant validates ResourceGrant objects for resource type validity.
func (p *ResourceQuotaEnforcementPlugin) validateResourceGrant(ctx context.Context, attrs admission.Attributes) error {
	ctx, span := p.startSpan(ctx, "quota.admission.ResourceGrantValidation",
		trace.WithAttributes(
			attribute.String("operation", string(attrs.GetOperation())),
			attribute.String("grant.name", attrs.GetName()),
			attribute.String("grant.namespace", attrs.GetNamespace()),
			attribute.String("user.name", attrs.GetUserInfo().GetName()),
		))
	defer span.End()

	// Only validate on CREATE - updates can be handled by CEL immutability rules
	if attrs.GetOperation() != admission.Create {
		span.SetAttributes(attribute.String("validation.status", "skipped"))
		return nil
	}

	obj := attrs.GetObject()
	if obj == nil {
		return nil
	}

	// CRDs always arrive as unstructured from the apiextensions-apiserver
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected unstructured.Unstructured for CRD, got %T", obj)
	}

	// Convert to typed struct for validation
	grant := &quotav1alpha1.ResourceGrant{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, grant); err != nil {
		return fmt.Errorf("failed to convert to ResourceGrant: %w", err)
	}

	span.SetAttributes(
		attribute.String("grant.name", grant.Name),
		attribute.String("grant.namespace", grant.Namespace),
		attribute.Int("grant.allowances_count", len(grant.Spec.Allowances)),
	)

	if validationErrs := p.resourceGrantValidator.Validate(ctx, grant, validation.AdmissionValidationOptions()); len(validationErrs) > 0 {
		span.SetAttributes(attribute.String("validation.status", "failed"))
		span.SetStatus(codes.Error, "ResourceGrant validation failed")

		p.logger.Info("ResourceGrant validation failed",
			"name", grant.Name,
			"namespace", grant.Namespace,
			"errors", validationErrs)

		return admission.NewForbidden(attrs, errors.NewInvalid(
			quotav1alpha1.GroupVersion.WithKind("ResourceGrant").GroupKind(),
			grant.Name,
			validationErrs,
		))
	}

	span.SetAttributes(attribute.String("validation.status", "passed"))
	return nil
}

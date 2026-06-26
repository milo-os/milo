// Package policy implements controllers for quota policy management.
// It contains controllers for ClaimCreationPolicy and GrantCreationPolicy resources
// that validate policy configurations and manage grant creation workflows.
package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.miloapis.com/milo/internal/informer"
	"go.miloapis.com/milo/pkg/quota/engine"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// GrantCreationController watches trigger resources and creates grants based on active policies.
type GrantCreationController struct {
	Scheme                *runtime.Scheme
	Manager               mcmanager.Manager
	TemplateEngine        engine.TemplateEngine
	CELEngine             engine.CELEngine
	ParentContextResolver *ParentContextResolver
	EventRecorder         record.EventRecorder

	informerManager informer.Manager
	logger          logr.Logger
}

// grantCreationHandler implements informer.ResourceEventHandler for grant creation.
type grantCreationHandler struct {
	controller *GrantCreationController
	policyName string
}

// OnAdd implements informer.ResourceEventHandler.
func (h *grantCreationHandler) OnAdd(obj *unstructured.Unstructured) {
	h.controller.processTriggerResource(obj, h.policyName, "ADD")
}

// OnUpdate implements informer.ResourceEventHandler.
func (h *grantCreationHandler) OnUpdate(old, new *unstructured.Unstructured) {
	h.controller.processTriggerResource(new, h.policyName, "UPDATE")
}

// OnDelete implements informer.ResourceEventHandler.
func (h *grantCreationHandler) OnDelete(obj *unstructured.Unstructured) {
	h.controller.processTriggerResource(obj, h.policyName, "DELETE")
}

// NewGrantCreationController creates a new GrantCreationController.
func NewGrantCreationController(
	manager mcmanager.Manager,
	scheme *runtime.Scheme,
	templateEngine engine.TemplateEngine,
	celEngine engine.CELEngine,
	parentContextResolver *ParentContextResolver,
	eventRecorder record.EventRecorder,
	informerManager informer.Manager,
) *GrantCreationController {
	logger := ctrl.Log.WithName("grant-creation")

	return &GrantCreationController{
		Manager:               manager,
		Scheme:                scheme,
		TemplateEngine:        templateEngine,
		CELEngine:             celEngine,
		ParentContextResolver: parentContextResolver,
		EventRecorder:         eventRecorder,
		informerManager:       informerManager,
		logger:                logger,
	}
}

// +kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=resourcegrants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=quota.miloapis.com,resources=grantcreationpolicies,verbs=get;list;watch

// Reconcile processes GrantCreationPolicy changes.

// processTriggerResource processes a trigger resource event.
func (r *GrantCreationController) processTriggerResource(obj *unstructured.Unstructured, policyName, eventType string) {
	ctx := context.Background()
	logger := r.logger.WithValues(
		"triggerResource", obj.GetName(),
		"triggerKind", obj.GetKind(),
		"namespace", obj.GetNamespace(),
		"policy", policyName,
		"eventType", eventType,
	)

	// Skip DELETE events for now (we handle cleanup via owner references)
	if eventType == "DELETE" {
		logger.V(2).Info("Skipping DELETE event")
		return
	}

	// Get the specific policy
	policy, err := r.getPolicyByName(ctx, policyName)
	if err != nil {
		logger.Error(err, "Failed to get policy")
		return
	}

	if policy == nil {
		logger.V(2).Info("Policy not found, removing watch")
		r.removeWatchForPolicy(ctx, policyName)
		return
	}

	logger.Info("Processing trigger resource for grant creation")

	// Process the policy
	if err := r.processPolicy(ctx, policy, obj); err != nil {
		logger.Error(err, "Failed to process policy")
		r.EventRecorder.Eventf(obj, "Warning", "PolicyProcessingFailed",
			"Failed to process grant creation policy %s: %v", policy.Name, err)
	}
}

// Reconcile handles GrantCreationPolicy changes.
func (r *GrantCreationController) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("policyName", req.Name)
	if req.ClusterName != "" {
		logger = logger.WithValues("cluster", req.ClusterName)
		ctx = log.IntoContext(ctx, logger)
	}

	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.ClusterName, err)
	}
	clusterClient := cluster.GetClient()

	// Fetch the policy
	var policy quotav1alpha1.GrantCreationPolicy
	if err := clusterClient.Get(ctx, req.NamespacedName, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			// Policy was deleted
			logger.Info("Policy deleted, removing watch")

			// Remove dynamic watch
			if err := r.removeWatchForPolicy(ctx, req.Name); err != nil {
				logger.Error(err, "Failed to remove watch for deleted policy")
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check if policy is Ready (has Ready=True condition)
	isReady := r.isPolicyReady(&policy)
	logger.V(1).Info("Policy reconciled", "ready", isReady)

	if isReady {
		// Policy is ready - set up dynamic watch for the trigger resource
		if err := r.addWatchForPolicy(ctx, &policy); err != nil {
			logger.Error(err, "Failed to add watch for policy")
			return ctrl.Result{RequeueAfter: time.Second * 10}, nil
		}
	} else {
		// Policy not ready - clean up watch
		if err := r.removeWatchForPolicy(ctx, req.Name); err != nil {
			logger.Error(err, "Failed to remove watch for not-ready policy")
		}
	}

	logger.Info("Successfully processed policy change")
	return ctrl.Result{}, nil
}

// isPolicyReady checks if a GrantCreationPolicy has Ready=True status condition.
func (r *GrantCreationController) isPolicyReady(policy *quotav1alpha1.GrantCreationPolicy) bool {
	for _, condition := range policy.Status.Conditions {
		if condition.Type == quotav1alpha1.GrantCreationPolicyReady && condition.Status == "True" {
			return true
		}
	}
	return false
}

// addWatchForPolicy adds a dynamic watch for a policy's trigger resource.
func (r *GrantCreationController) addWatchForPolicy(ctx context.Context, policy *quotav1alpha1.GrantCreationPolicy) error {
	gvk := policy.Spec.Trigger.Resource.GetGVK()
	consumerID := fmt.Sprintf("grant-creation-policy-%s", policy.Name)

	handler := &grantCreationHandler{
		controller: r,
		policyName: policy.Name,
	}

	req := informer.WatchRequest{
		GVK:        gvk,
		ConsumerID: consumerID,
		Handler:    handler,
	}

	return r.informerManager.AddWatch(ctx, req)
}

// removeWatchForPolicy removes a dynamic watch for a policy.
func (r *GrantCreationController) removeWatchForPolicy(ctx context.Context, policyName string) error {
	// We need to get the policy to know what GVK to remove
	policy, err := r.getPolicyByName(ctx, policyName)
	if err != nil {
		return err
	}

	if policy == nil {
		// Policy doesn't exist, nothing to remove
		return nil
	}

	gvk := policy.Spec.Trigger.Resource.GetGVK()
	consumerID := fmt.Sprintf("grant-creation-policy-%s", policyName)

	return r.informerManager.RemoveWatch(ctx, gvk, consumerID)
}

// getPolicyByName retrieves a GrantCreationPolicy by name.
// Uses local cluster client since policies exist in the core control plane.
func (r *GrantCreationController) getPolicyByName(ctx context.Context, name string) (*quotav1alpha1.GrantCreationPolicy, error) {
	policy := &quotav1alpha1.GrantCreationPolicy{}
	key := client.ObjectKey{Name: name}

	clusterClient := r.Manager.GetLocalManager().GetClient()

	if err := clusterClient.Get(ctx, key, policy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return policy, nil
}

// processPolicy processes a single policy against a trigger resource.
func (r *GrantCreationController) processPolicy(
	ctx context.Context,
	policy *quotav1alpha1.GrantCreationPolicy,
	triggerObj *unstructured.Unstructured,
) error {
	logger := log.FromContext(ctx).WithValues("policy", policy.Name)

	// Evaluate trigger conditions
	conditionsMet, err := r.TemplateEngine.EvaluateConditions(policy.Spec.Trigger.Constraints, triggerObj)
	if err != nil {
		return fmt.Errorf("failed to evaluate conditions: %w", err)
	}

	if !conditionsMet {
		logger.V(2).Info("Trigger conditions not met, skipping grant creation")
		// Check if there's an existing grant that should be cleaned up
		return r.cleanupGrant(ctx, policy, triggerObj)
	}

	logger.Info("Trigger conditions met, creating/updating grant")

	// Determine target client (same cluster or cross-cluster)
	targetClient, err := r.resolveTargetClient(ctx, policy, triggerObj)
	if err != nil {
		return fmt.Errorf("failed to resolve target client: %w", err)
	}

	// Render the grant (namespace is rendered by template engine)
	grant, err := r.TemplateEngine.RenderGrant(policy, triggerObj)
	if err != nil {
		return fmt.Errorf("failed to render grant: %w", err)
	}

	// Create or update the grant
	if err := r.createOrUpdateGrant(ctx, targetClient, grant, policy, triggerObj); err != nil {
		return fmt.Errorf("failed to create/update grant: %w", err)
	}

	logger.Info("Successfully processed policy", "grantName", grant.Name, "grantNamespace", grant.Namespace)
	return nil
}

// resolveTargetClient determines the target client for grant creation.
// Namespace is always rendered by the template engine.
func (r *GrantCreationController) resolveTargetClient(
	ctx context.Context,
	policy *quotav1alpha1.GrantCreationPolicy,
	triggerObj *unstructured.Unstructured,
) (client.Client, error) {
	if policy.Spec.Target.ParentContext == nil {
		return r.Manager.GetLocalManager().GetClient(), nil
	}

	// Resolve parent context name using CEL template expression
	variables := map[string]interface{}{
		"trigger": triggerObj.Object,
	}
	parentContextName, err := r.CELEngine.EvaluateTemplateExpression(
		policy.Spec.Target.ParentContext.NameExpression,
		variables,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate parent context name: %w", err)
	}

	// Get client for parent context
	parentContext := policy.Spec.Target.ParentContext
	targetClient, err := r.ParentContextResolver.ResolveClient(ctx, &ParentContextSpec{
		APIGroup: parentContext.APIGroup,
		Kind:     parentContext.Kind,
		Name:     parentContextName, // Use resolved name directly
	}, triggerObj)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve parent context client: %w", err)
	}

	return targetClient, nil
}

// createOrUpdateGrant creates or updates a ResourceGrant.
func (r *GrantCreationController) createOrUpdateGrant(
	ctx context.Context,
	targetClient client.Client,
	grant *quotav1alpha1.ResourceGrant,
	policy *quotav1alpha1.GrantCreationPolicy,
	triggerObj *unstructured.Unstructured,
) error {
	logger := log.FromContext(ctx).WithValues("grantName", grant.Name, "grantNamespace", grant.Namespace)

	// Check if grant already exists
	existingGrant := &quotav1alpha1.ResourceGrant{}
	err := targetClient.Get(ctx, client.ObjectKey{
		Name:      grant.Name,
		Namespace: grant.Namespace,
	}, existingGrant)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Owner references are only valid within the same cluster.
			if policy.Spec.Target.ParentContext == nil {
				if err := controllerutil.SetControllerReference(triggerObj, grant, r.Scheme); err != nil {
					return fmt.Errorf("failed to set owner reference: %w", err)
				}
			}
			logger.Info("Creating new ResourceGrant")
			if err := targetClient.Create(ctx, grant); err != nil {
				return fmt.Errorf("failed to create grant: %w", err)
			}

			r.EventRecorder.Eventf(triggerObj, "Normal", "GrantCreated",
				"Created ResourceGrant %s/%s from policy %s", grant.Namespace, grant.Name, policy.Name)

			return nil
		}
		return fmt.Errorf("failed to check existing grant: %w", err)
	}

	original := existingGrant.DeepCopy()

	existingGrant.Spec = grant.Spec
	existingGrant.Labels = grant.Labels
	existingGrant.Annotations = grant.Annotations

	if equality.Semantic.DeepEqual(existingGrant, original) {
		logger.V(1).Info("ResourceGrant unchanged, skipping update")
		return nil
	}

	logger.Info("Updating existing ResourceGrant")
	if err := targetClient.Update(ctx, existingGrant); err != nil {
		return fmt.Errorf("failed to update grant: %w", err)
	}

	r.EventRecorder.Eventf(triggerObj, "Normal", "GrantUpdated",
		"Updated ResourceGrant %s/%s from policy %s", grant.Namespace, grant.Name, policy.Name)

	return nil
}

// cleanupGrant removes a grant if conditions are no longer met.
func (r *GrantCreationController) cleanupGrant(
	ctx context.Context,
	policy *quotav1alpha1.GrantCreationPolicy,
	triggerObj *unstructured.Unstructured,
) error {
	logger := log.FromContext(ctx).WithValues("policy", policy.Name)

	// Determine target client
	targetClient, err := r.resolveTargetClient(ctx, policy, triggerObj)
	if err != nil {
		return fmt.Errorf("failed to resolve target client: %w", err)
	}

	// Render the grant to get its name and namespace
	grant, err := r.TemplateEngine.RenderGrant(policy, triggerObj)
	if err != nil {
		return fmt.Errorf("failed to render grant for cleanup: %w", err)
	}

	// Check if grant exists
	existingGrant := &quotav1alpha1.ResourceGrant{}
	err = targetClient.Get(ctx, client.ObjectKey{
		Name:      grant.Name,
		Namespace: grant.Namespace,
	}, existingGrant)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Grant doesn't exist, nothing to clean up
			return nil
		}
		return fmt.Errorf("failed to check existing grant: %w", err)
	}

	// Check if this grant was created by our policy
	if existingGrant.Labels["quota.miloapis.com/policy"] == policy.Name {
		logger.Info("Cleaning up grant due to unmet conditions", "grantName", existingGrant.Name)

		if err := targetClient.Delete(ctx, existingGrant); err != nil {
			return fmt.Errorf("failed to delete grant: %w", err)
		}

		r.EventRecorder.Eventf(triggerObj, "Normal", "GrantDeleted",
			"Deleted ResourceGrant %s/%s due to unmet conditions", existingGrant.Namespace, existingGrant.Name)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrantCreationController) SetupWithManager(mgr mcmanager.Manager) error {
	r.logger.Info("Setting up GrantCreationController")

	controller := mcbuilder.ControllerManagedBy(mgr).
		For(&quotav1alpha1.GrantCreationPolicy{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Named("grant-creation-controller")

	r.logger.Info("GrantCreationController setup completed successfully")

	return controller.Complete(r)
}

// ParentContextSpec is a simplified version for the resolver.
type ParentContextSpec struct {
	// APIGroup is the API group of the parent context resource.
	APIGroup string
	// Kind is the kind of the parent context resource.
	Kind string
	// Name is the resolved name of the parent context resource.
	Name string
}

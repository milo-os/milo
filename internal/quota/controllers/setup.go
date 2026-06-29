// Package controllers provides a simplified setup function for all quota controllers.
//
// Why: A single setup function reduces boilerplate wiring in controller
// manager and keeps controller lifecycle consistent across the package.
package controllers

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/client-go/dynamic"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.miloapis.com/milo/internal/informer"
	"go.miloapis.com/milo/internal/quota/controllers/core"
	"go.miloapis.com/milo/internal/quota/controllers/lifecycle"
	"go.miloapis.com/milo/internal/quota/controllers/policy"
	"go.miloapis.com/milo/pkg/quota/engine"
	"go.miloapis.com/milo/pkg/quota/validation"
)

// SetupQuotaControllers registers all quota controllers with the provided multicluster manager.
//
// All quota controllers now use the multicluster runtime framework to enable cross-cluster
// quota management. Controllers watch resources based on their engagement strategy:
//   - Core cluster only: ResourceRegistration, ClaimCreationPolicy, GrantCreationPolicy, GrantCreation
//   - All clusters: ResourceGrant, ResourceClaim, AllowanceBucket, Ownership, Cleanup
//
// Parameters:
//   - mgr: Multicluster controller manager
//   - dynamicClient: Dynamic client for resource type validation
//   - logger: Logger for quota controller operations
func SetupQuotaControllers(mgr mcmanager.Manager, dynamicClient dynamic.Interface, logger logr.Logger) error {
	logger.Info("Setting up quota controllers with multicluster support")

	// Get the local manager for accessing shared components like EventRecorder
	standardMgr := mgr.GetLocalManager()

	// Create shared validation components once using async initialization
	// This prevents blocking controller startup if API server isn't fully ready
	sharedResourceTypeValidator := validation.NewResourceTypeValidator(dynamicClient)
	logger.Info("Shared ResourceTypeValidator created, will sync in background")

	// Create shared CEL validator for policy validation
	celValidator, err := validation.NewCELValidator()
	if err != nil {
		return fmt.Errorf("failed to create CEL validator: %w", err)
	}

	// Create CEL engine for runtime evaluation (used by grant creation controller)
	celEngine, err := engine.NewCELEngine()
	if err != nil {
		return fmt.Errorf("failed to create CEL engine: %w", err)
	}

	// Setup controllers in logical order

	// 1. ResourceRegistration controller (foundational - core cluster only)
	logger.V(1).Info("Setting up ResourceRegistration controller (core cluster only)")
	if err := (&core.ResourceRegistrationController{
		Scheme:  standardMgr.GetScheme(),
		Manager: mgr,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ResourceRegistrationController: %w", err)
	}

	// 2. ResourceGrant controller (all clusters)
	logger.V(1).Info("Setting up ResourceGrant controller (all clusters)")
	grantValidator := validation.NewResourceGrantValidator(sharedResourceTypeValidator)
	if err := (&core.ResourceGrantController{
		Scheme:         standardMgr.GetScheme(),
		Manager:        mgr,
		GrantValidator: grantValidator,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ResourceGrantController: %w", err)
	}

	// 3. ResourceClaim controller (all clusters)
	logger.V(1).Info("Setting up ResourceClaim controller (all clusters)")
	if err := (&core.ResourceClaimController{
		Scheme:  standardMgr.GetScheme(),
		Manager: mgr,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ResourceClaimController: %w", err)
	}

	// 4. AllowanceBucket controller (aggregates quota data - all clusters)
	logger.V(1).Info("Setting up AllowanceBucket controller (all clusters)")
	if err := (&core.AllowanceBucketController{
		Scheme:  standardMgr.GetScheme(),
		Manager: mgr,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup AllowanceBucketController: %w", err)
	}

	// 5. ClaimCreationPolicy controller (policy validation - core cluster only)
	logger.V(1).Info("Setting up ClaimCreationPolicy controller (core cluster only)")
	claimCreationPolicyValidator := validation.NewClaimCreationPolicyValidator(sharedResourceTypeValidator)
	if err := (&policy.ClaimCreationPolicyReconciler{
		Scheme:          standardMgr.GetScheme(),
		Manager:         mgr,
		PolicyValidator: claimCreationPolicyValidator,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ClaimCreationPolicyReconciler: %w", err)
	}

	// 6. GrantCreationPolicy controller (policy validation - core cluster only)
	logger.V(1).Info("Setting up GrantCreationPolicy controller (core cluster only)")
	grantTemplateValidator, err := validation.NewGrantTemplateValidator(sharedResourceTypeValidator)
	if err != nil {
		return fmt.Errorf("failed to create GrantTemplateValidator: %w", err)
	}
	grantCreationPolicyValidator := validation.NewGrantCreationPolicyValidator(celValidator, grantTemplateValidator)
	if err := (&policy.GrantCreationPolicyReconciler{
		Scheme:          standardMgr.GetScheme(),
		Manager:         mgr,
		PolicyValidator: grantCreationPolicyValidator,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup GrantCreationPolicyReconciler: %w", err)
	}

	// 7. Grant Creation controller (automatic grant creation - core cluster only)
	logger.V(1).Info("Setting up Grant Creation controller (core cluster only)")
	templateEngine := engine.NewTemplateEngine(celEngine, logger)
	parentContextResolver := policy.NewParentContextResolver(standardMgr.GetClient(), standardMgr.GetConfig(), standardMgr.GetScheme(), policy.ParentContextResolverOptions{})

	informerManager, err := informer.NewManagerFromManager(standardMgr)
	if err != nil {
		return fmt.Errorf("failed to create informer manager: %w", err)
	}

	if err := standardMgr.Add(informerManager); err != nil {
		return fmt.Errorf("failed to add informer manager to controller manager: %w", err)
	}

	grantCreationController := policy.NewGrantCreationController(
		mgr,
		standardMgr.GetScheme(),
		templateEngine,
		celEngine,
		parentContextResolver,
		standardMgr.GetEventRecorderFor("grant-creation"),
		informerManager,
	)
	if err := grantCreationController.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup GrantCreationController: %w", err)
	}

	// 8. ResourceClaim Ownership controller (lifecycle management - all clusters)
	logger.V(1).Info("Setting up ResourceClaim Ownership controller (all clusters)")
	if err := (&lifecycle.ResourceClaimOwnershipController{
		Scheme:  standardMgr.GetScheme(),
		Manager: mgr,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup ResourceClaimOwnershipController: %w", err)
	}

	// 9. DeniedAutoClaim Cleanup controller (lifecycle management - all clusters)
	logger.V(1).Info("Setting up DeniedAutoClaim Cleanup controller (all clusters)")
	deniedCleanupController := lifecycle.NewDeniedAutoClaimCleanupController(
		standardMgr.GetScheme(),
		mgr,
	)
	if err := deniedCleanupController.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup DeniedAutoClaimCleanupController: %w", err)
	}

	logger.Info("All quota controllers set up successfully")
	return nil
}

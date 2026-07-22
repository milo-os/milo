// Package projectsuspension implements the ProjectSuspensionEnforcement
// admission plugin. It denies Create/Update requests targeting a suspended
// project's virtual control plane, resolving the project from request
// context (set by pkg/server/filters/projects.go's ProjectRouterWithRequestInfo)
// and checking the authoritative core Project object's Suspended condition.
//
// Reads (Get/List/Watch) are never routed through admission plugins, so
// FR2 ("reads always allowed") requires no code here; it is a structural
// guarantee of the Kubernetes admission chain, not a behavior this plugin
// implements. Delete is intentionally allowed during suspension: the
// Handler below only registers for Create and Update, so Delete requests
// never reach Validate at all. Updates to objects already mid-deletion
// (deletionTimestamp set) are also exempted, so finalizer removal can
// complete — otherwise any resource with a finalizer would get stuck in
// Terminating forever once its project is suspended, since finalizer
// removal for arbitrary CRDs is itself a plain Update.
package projectsuspension

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
)

// Plugin denies Create/Update requests targeting a suspended project's
// virtual control plane. It resolves the project from request context
// (set by pkg/server/filters/projects.go's ProjectRouterWithRequestInfo)
// and checks the authoritative core Project object's Suspended condition.
type Plugin struct {
	*admission.Handler

	// dynamicClient is the root-scope client injected via
	// initializer.WantsDynamicClient. This plugin only ever reads the
	// root-scoped Project object, so no project-scoped client is needed.
	dynamicClient dynamic.Interface

	// suspensionCache maintains an in-memory, informer-backed cache of suspended projects.
	suspensionCache *ProjectSuspensionCache

	logger logr.Logger
}

// projectGVR identifies the cluster-scoped Project resource.
var projectGVR = schema.GroupVersionResource{
	Group:    "resourcemanager.miloapis.com",
	Version:  "v1alpha1",
	Resource: "projects",
}

var (
	_ initializer.WantsDynamicClient    = &Plugin{}
	_ admission.ValidationInterface     = &Plugin{}
	_ admission.InitializationValidator = &Plugin{}
)

// accessReviewResources lists the resources that must never be blocked by
// this plugin, regardless of project suspension state, so authorization
// checks never depend on suspension. Mirrors the same allow-list pattern
// used by internal/apiserver/admission/plugin/namespace/lifecycle.
var accessReviewResources = map[schema.GroupResource]bool{
	{Group: "authorization.k8s.io", Resource: "subjectaccessreviews"}:      true,
	{Group: "authorization.k8s.io", Resource: "localsubjectaccessreviews"}: true,
}

// suspensionState is the cached result of resolving a project's suspension
// status from the authoritative core Project object.
type suspensionState struct {
	Suspended   bool
	Suspensions []resourcemanagerv1alpha1.ProjectSuspensionInfo
}

// NewPlugin creates a new ProjectSuspensionEnforcement admission plugin.
// It only registers for Create and Update; Delete requests are never
// routed to Validate (see Decisions Made in the design doc: DELETE is
// allowed during suspension so customers can clean up/offboard).
func NewPlugin() (*Plugin, error) {
	return &Plugin{
		Handler: admission.NewHandler(admission.Create, admission.Update),
		logger:  klog.NewKlogr().WithName("project-suspension-enforcement"),
	}, nil
}

// SetDynamicClient implements initializer.WantsDynamicClient.
func (p *Plugin) SetDynamicClient(c dynamic.Interface) {
	p.dynamicClient = c
	if c != nil && p.suspensionCache == nil {
		p.suspensionCache = NewProjectSuspensionCache(c, p.logger)
		p.suspensionCache.Start()
	}
}

// Close stops the underlying project suspension informer cache.
func (p *Plugin) Close() {
	if p.suspensionCache != nil {
		p.suspensionCache.Stop()
	}
}

// ValidateInitialization implements admission.InitializationValidator.
func (p *Plugin) ValidateInitialization() error {
	if p.dynamicClient == nil {
		return fmt.Errorf("%s: missing dynamic client", PluginName)
	}
	if p.suspensionCache == nil {
		return fmt.Errorf("%s: missing suspension cache", PluginName)
	}
	return nil
}

// Validate implements admission.ValidationInterface.
func (p *Plugin) Validate(ctx context.Context, attrs admission.Attributes, _ admission.ObjectInterfaces) error {
	if isExempt(ctx, attrs) {
		return nil
	}

	projectID, ok := milorequest.ProjectID(ctx)
	if !ok || projectID == "" {
		// This request is not scoped to a project control plane (e.g. root
		// cluster CRUD on Project/ProjectSuspension themselves, or an
		// org-scoped request). Out of scope for this plugin.
		return nil
	}

	state := p.getSuspensionState(ctx, projectID)
	if state == nil || !state.Suspended {
		return nil
	}

	return newSuspendedForbiddenError(attrs, projectID, state)
}

// getSuspensionState resolves the suspension state of the project
// identified by projectID, consulting the in-memory informer cache.
func (p *Plugin) getSuspensionState(ctx context.Context, projectID string) *suspensionState {
	if p.suspensionCache != nil {
		return p.suspensionCache.GetSuspensionState(ctx, projectID)
	}
	return nil
}

// parseSuspensionState extracts the Suspended condition and any active
// suspensions from a Project object retrieved via the dynamic client.
// Returns nil if the project is not suspended.
func parseSuspensionState(obj *unstructured.Unstructured) *suspensionState {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return nil
	}

	suspended := false
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(m, "type")
		if condType != resourcemanagerv1alpha1.ProjectSuspended {
			continue
		}
		status, _, _ := unstructured.NestedString(m, "status")
		suspended = status == string(metav1.ConditionTrue)
		break
	}

	if !suspended {
		return nil
	}

	var suspensions []resourcemanagerv1alpha1.ProjectSuspensionInfo
	raw, found, err := unstructured.NestedSlice(obj.Object, "status", "suspensions")
	if err == nil && found {
		for _, item := range raw {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			var info resourcemanagerv1alpha1.ProjectSuspensionInfo
			if convErr := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &info); convErr != nil {
				continue
			}
			suspensions = append(suspensions, info)
		}
	}

	return &suspensionState{Suspended: true, Suspensions: suspensions}
}

// isExempt reports whether the request should bypass suspension
// enforcement entirely, checked in order (cheapest first):
//  1. Access review passthrough, so authorization checks never leak or
//     depend on project suspension state.
//  2. Superuser/loopback identities (system:masters), matching the general
//     Kubernetes convention that system:masters bypasses admission-time
//     policy checks.
//  3. Updates to objects already mid-deletion, so finalizer removal can
//     complete (see isAlreadyTerminating).
func isExempt(_ context.Context, attrs admission.Attributes) bool {
	if accessReviewResources[attrs.GetResource().GroupResource()] {
		return true
	}

	for _, g := range attrs.GetUserInfo().GetGroups() {
		if g == user.SystemPrivilegedGroup {
			return true
		}
	}

	if attrs.GetOperation() == admission.Update && isAlreadyTerminating(attrs.GetOldObject()) {
		return true
	}

	return false
}

// isAlreadyTerminating reports whether obj already has a deletionTimestamp
// set, i.e. it is mid-deletion. Finalizer removal for arbitrary CRDs is a
// plain Update — there is no generic "/finalize" subresource outside a
// handful of built-in types — so without this exemption, any resource with
// a finalizer that is (or starts) terminating while its project is
// suspended would get stuck in Terminating forever, contradicting the
// decision to allow Delete during suspension (see Decisions Made in the
// design doc).
func isAlreadyTerminating(obj runtime.Object) bool {
	if obj == nil {
		return false
	}
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false
	}
	return accessor.GetDeletionTimestamp() != nil
}

// newSuspendedForbiddenError builds a 403 Forbidden error carrying a
// ProjectSuspendedCause in Details.Causes, distinct from other denial
// reasons (quota, RBAC, NamespaceLifecycle's NamespaceTerminatingCause) so
// API clients can render a specific "this project is suspended" message.
func newSuspendedForbiddenError(attrs admission.Attributes, projectID string, s *suspensionState) error {
	reasons := uniqueSortedReasons(s.Suspensions)
	reasonList := strings.Join(reasons, ", ")
	if reasonList == "" {
		reasonList = "unspecified"
	}

	msg := fmt.Sprintf(
		"project %q is suspended (%s) and cannot accept new writes while suspended",
		projectID, reasonList,
	)

	err := admission.NewForbidden(attrs, errors.New(msg))
	if apiErr, ok := err.(*apierrors.StatusError); ok {
		apiErr.ErrStatus.Details.Causes = append(apiErr.ErrStatus.Details.Causes, metav1.StatusCause{
			Type:    resourcemanagerv1alpha1.ProjectSuspendedCause,
			Message: msg,
		})
	}
	return err
}

// uniqueSortedReasons returns the sorted, de-duplicated set of suspension
// reasons across all active suspensions, so the denial message is
// deterministic even when a project has multiple simultaneous suspensions.
func uniqueSortedReasons(suspensions []resourcemanagerv1alpha1.ProjectSuspensionInfo) []string {
	seen := make(map[string]struct{}, len(suspensions))
	for _, s := range suspensions {
		seen[string(s.Reason)] = struct{}{}
	}

	reasons := make([]string, 0, len(seen))
	for r := range seen {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	return reasons
}

package resourcemanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"go.miloapis.com/milo/internal/controllers/projectpurge"
	infrastructurev1alpha1 "go.miloapis.com/milo/pkg/apis/infrastructure/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const projectFinalizer = "resourcemanager.miloapis.com/project-controller"

var gvrGatewayClass = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gatewayclasses",
}

var gvrDNSZoneClass = schema.GroupVersionResource{
	Group:    "dns.networking.miloapis.com",
	Version:  "v1alpha1",
	Resource: "dnszoneclasses",
}

var gvrConnectorClass = schema.GroupVersionResource{
	Group:    "networking.datumapis.com",
	Version:  "v1alpha1",
	Resource: "connectorclasses",
}

// ProjectController reconciles a Project object
type ProjectController struct {
	ControlPlaneClient client.Client
	InfraClient        client.Client

	// Base (root) API config used to derive per-project clients.
	BaseConfig *rest.Config

	// Purger orchestrates DeleteCollection across all resources
	Purger *projectpurge.Purger
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.miloapis.com,resources=projectcontrolplanes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnszoneclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectorclasses,verbs=get;list;watch;create;update;patch;delete

func (r *ProjectController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var project resourcemanagerv1alpha.Project
	if err := r.ControlPlaneClient.Get(ctx, req.NamespacedName, &project); apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("get project: %w", err)
	}

	// Deletion path: clean up project resources, then remove finalizer
	if !project.DeletionTimestamp.IsZero() {
		// Best-effort delete the ProjectControlPlane in infra
		if r.InfraClient != nil {
			var pcp infrastructurev1alpha1.ProjectControlPlane
			if err := r.InfraClient.Get(ctx, types.NamespacedName{
				Namespace: project.Namespace,
				Name:      project.Name,
			}, &pcp); err == nil && pcp.DeletionTimestamp.IsZero() {
				_ = r.InfraClient.Delete(ctx, &pcp)
			}
		}
		if controllerutil.ContainsFinalizer(&project, projectFinalizer) {
			projCfg := r.forProject(r.BaseConfig, project.Name)

			cleanupCond := apimeta.FindStatusCondition(project.Status.Conditions, resourcemanagerv1alpha.ProjectResourceCleanup)

			// If awaiting completion, check whether resources have drained.
			if cleanupCond != nil && cleanupCond.Status == metav1.ConditionTrue &&
				cleanupCond.Reason == resourcemanagerv1alpha.ProjectCleanupAwaitingCompletionReason {

				done, err := r.Purger.IsPurgeComplete(ctx, projCfg, project.Name)
				if err != nil {
					logger.Error(err, "check cleanup completion", "project", project.Name)
					return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
				}
				if done {
					// Update ResourceCleanup condition to reflect completion
					cleanupDone := metav1.Condition{
						Type:               resourcemanagerv1alpha.ProjectResourceCleanup,
						Status:             metav1.ConditionFalse,
						Reason:             resourcemanagerv1alpha.ProjectCleanupCompleteReason,
						Message:            "Project resources have been deleted",
						ObservedGeneration: project.Generation,
					}
					if apimeta.SetStatusCondition(&project.Status.Conditions, cleanupDone) {
						if err := r.ControlPlaneClient.Status().Update(ctx, &project); err != nil {
							return ctrl.Result{}, fmt.Errorf("update cleanup status: %w", err)
						}
					}

					// Re-fetch to get current resourceVersion after status update
					if err := r.ControlPlaneClient.Get(ctx, req.NamespacedName, &project); err != nil {
						return ctrl.Result{}, fmt.Errorf("re-fetch project: %w", err)
					}

					// Remove finalizer with fresh object
					before := project.DeepCopy()
					controllerutil.RemoveFinalizer(&project, projectFinalizer)
					if err := r.ControlPlaneClient.Patch(ctx, &project, client.MergeFrom(before)); err != nil {
						return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
					}
					return ctrl.Result{}, nil
				}

				// Resources still exist — transition back to CleanupStarted
				// so the next reconcile re-issues delete commands.
				reissue := metav1.Condition{
					Type:               resourcemanagerv1alpha.ProjectResourceCleanup,
					Status:             metav1.ConditionTrue,
					Reason:             resourcemanagerv1alpha.ProjectCleanupStartedReason,
					Message:            "Re-issuing delete commands for remaining project resources",
					ObservedGeneration: project.Generation,
				}
				if apimeta.SetStatusCondition(&project.Status.Conditions, reissue) {
					if err := r.ControlPlaneClient.Status().Update(ctx, &project); err != nil {
						return ctrl.Result{}, fmt.Errorf("update cleanup status: %w", err)
					}
				}
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			// CleanupStarted or no condition yet — issue delete commands.
			cleanupStarted := metav1.Condition{
				Type:               resourcemanagerv1alpha.ProjectResourceCleanup,
				Status:             metav1.ConditionTrue,
				Reason:             resourcemanagerv1alpha.ProjectCleanupStartedReason,
				Message:            "Issuing delete commands for project resources",
				ObservedGeneration: project.Generation,
			}
			if apimeta.SetStatusCondition(&project.Status.Conditions, cleanupStarted) {
				if err := r.ControlPlaneClient.Status().Update(ctx, &project); err != nil {
					return ctrl.Result{}, fmt.Errorf("update cleanup status: %w", err)
				}
			}

			if err := r.Purger.StartPurge(ctx, projCfg, project.Name, projectpurge.Options{
				Timeout:  2 * time.Minute,
				Parallel: 16,
			}); err != nil {
				logger.Error(err, "start cleanup", "project", project.Name)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			// Transition to awaiting completion — subsequent reconciles
			// will check IsPurgeComplete instead of re-issuing deletes.
			cleanupAwaiting := metav1.Condition{
				Type:               resourcemanagerv1alpha.ProjectResourceCleanup,
				Status:             metav1.ConditionTrue,
				Reason:             resourcemanagerv1alpha.ProjectCleanupAwaitingCompletionReason,
				Message:            "Waiting for project resources to be removed",
				ObservedGeneration: project.Generation,
			}
			if apimeta.SetStatusCondition(&project.Status.Conditions, cleanupAwaiting) {
				if err := r.ControlPlaneClient.Status().Update(ctx, &project); err != nil {
					return ctrl.Result{}, fmt.Errorf("update awaiting status: %w", err)
				}
			}

			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer present
	if !controllerutil.ContainsFinalizer(&project, projectFinalizer) {
		before := project.DeepCopy()
		controllerutil.AddFinalizer(&project, projectFinalizer)
		if err := r.ControlPlaneClient.Patch(ctx, &project, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		// trigger another reconcile after patch
		return ctrl.Result{}, nil
	}

	// ---- Ensure ProjectControlPlane exists & is Ready ----
	if r.InfraClient != nil {
		var pcp infrastructurev1alpha1.ProjectControlPlane
		if err := r.InfraClient.Get(ctx, types.NamespacedName{
			Namespace: project.Namespace,
			Name:      project.Name,
		}, &pcp); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("get projectcontrolplane: %w", err)
			}
			// create it
			pcp = infrastructurev1alpha1.ProjectControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: project.Namespace,
					Name:      project.Name,
					Labels: map[string]string{
						resourcemanagerv1alpha.ProjectNameLabel: project.Name,
						resourcemanagerv1alpha.ProjectUIDLabel:  string(project.UID),
					},
					Annotations: map[string]string{
						resourcemanagerv1alpha.OwnerNameLabel: project.Spec.OwnerRef.Name,
					},
				},
				Spec: infrastructurev1alpha1.ProjectControlPlaneSpec{},
			}
			if err := r.InfraClient.Create(ctx, &pcp); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, fmt.Errorf("create projectcontrolplane: %w", err)
			}
			// Requeue to ensure we get the latest state
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	}

	// Ensure per-project "default" Namespace exists
	projCfg := r.forProject(r.BaseConfig, project.Name)
	pc, err := buildProjectClients(projCfg)
	if err != nil {
		logger.Error(err, "build per-project clients failed", "project", project.Name)
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	if err := ensureDefaultNamespace(ctx, pc.Kube); err != nil {
		logger.Error(err, "ensure default namespace failed", "project", project.Name)
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// Ensure the project's External Global Proxy GatewayClass exists
	ok, err := hasResource(pc.Disc,
		gvrGatewayClass.GroupVersion(),
		"gatewayclasses",
	)
	if err != nil {
		logger.Error(err, "gatewayclass discovery failed")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if ok {
		if err := ensureGatewayClass(ctx, pc.Dynamic,
			"datum-external-global-proxy",
			"gateway.networking.datumapis.com/external-global-proxy-controller",
		); err != nil {
			logger.Error(err, "ensure gatewayclass failed", "project", project.Name)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	} else {
		logger.Info("GatewayClass CRD not installed; skipping", "project", project.Name)
	}

	// Ensure the project's External Global DNS DNSZoneClass exists
	ok, err = hasResource(pc.Disc,
		gvrDNSZoneClass.GroupVersion(),
		"dnszoneclasses",
	)
	if err != nil {
		logger.Error(err, "dnszoneclass discovery failed")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if ok {
		if err := ensureDNSZoneClass(ctx, pc.Dynamic,
			"datum-external-global-dns",
			"dns.networking.miloapis.com/datum-external-global-dns",
		); err != nil {
			logger.Error(err, "ensure dnszoneclass failed", "project", project.Name)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	} else {
		logger.Info("DNSZoneClass CRD not installed; skipping", "project", project.Name)
	}

	// Ensure the project's ConnectorClass exists
	ok, err = hasResource(pc.Disc,
		gvrConnectorClass.GroupVersion(),
		"connectorclasses",
	)
	if err != nil {
		logger.Error(err, "connectorclass discovery failed")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if ok {
		if err := ensureConnectorClass(ctx, pc.Dynamic,
			"iroh-quic-tunnel",
			"networking.datumapis.com/iroh-quic-tunnel",
		); err != nil {
			logger.Error(err, "ensure connectorclass failed", "project", project.Name)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	} else {
		logger.Info("ConnectorClass CRD not installed; skipping", "project", project.Name)
	}

	// Set Ready condition (idempotent)
	if cond := apimeta.FindStatusCondition(project.Status.Conditions, resourcemanagerv1alpha.ProjectReady); cond == nil || cond.Status != metav1.ConditionTrue {
		newCond := metav1.Condition{
			Type:               resourcemanagerv1alpha.ProjectReady,
			Status:             metav1.ConditionTrue,
			Reason:             resourcemanagerv1alpha.ProjectReady,
			Message:            "Project is ready",
			ObservedGeneration: project.Generation,
		}
		if apimeta.SetStatusCondition(&project.Status.Conditions, newCond) {
			_ = r.ControlPlaneClient.Status().Update(ctx, &project)
		}
	}

	return ctrl.Result{}, nil
}

// TODO(zach): Remove this once project addons are fully migrated to the new API.
// ensureGatewayClass ensures that a GatewayClass with the given name and controller exists.
func ensureGatewayClass(ctx context.Context, dc dynamic.Interface, name, controller string) error {
	_, err := dc.Resource(gvrGatewayClass).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get GatewayClass %q: %w", name, err)
	}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "GatewayClass",
			"metadata":   map[string]interface{}{"name": name},
			"spec":       map[string]interface{}{"controllerName": controller},
		},
	}
	if _, err := dc.Resource(gvrGatewayClass).Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create GatewayClass %q: %w", name, err)
	}
	return nil
}

// ensureDNSZoneClass ensures that a DNSZoneClass with the given name and controller exists.
func ensureDNSZoneClass(ctx context.Context, dc dynamic.Interface, name, controller string) error {
	_, err := dc.Resource(gvrDNSZoneClass).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get DNSZoneClass %q: %w", name, err)
	}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "dns.networking.miloapis.com/v1alpha1",
			"kind":       "DNSZoneClass",
			"metadata":   map[string]interface{}{"name": name},
			"spec":       map[string]interface{}{"controllerName": controller},
		},
	}
	if _, err := dc.Resource(gvrDNSZoneClass).Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create DNSZoneClass %q: %w", name, err)
	}
	return nil
}

// ensureConnectorClass ensures that a ConnectorClass with the given name and controller exists.
func ensureConnectorClass(ctx context.Context, dc dynamic.Interface, name, controller string) error {
	_, err := dc.Resource(gvrConnectorClass).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get ConnectorClass %q: %w", name, err)
	}
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.datumapis.com/v1alpha1",
			"kind":       "ConnectorClass",
			"metadata":   map[string]interface{}{"name": name},
			"spec":       map[string]interface{}{"controllerName": controller},
		},
	}
	if _, err := dc.Resource(gvrConnectorClass).Create(ctx, obj, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create ConnectorClass %q: %w", name, err)
	}
	return nil
}

func ensureDefaultNamespace(ctx context.Context, cs kubernetes.Interface) error {
	// GET is cheap and idempotent
	if _, err := cs.CoreV1().Namespaces().Get(ctx, metav1.NamespaceDefault, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get namespace %q: %w", metav1.NamespaceDefault, err)
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   metav1.NamespaceDefault,
			Labels: map[string]string{"miloapis.com/project-default": "true"},
		},
	}
	if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %q: %w", ns.Name, err)
	}
	return nil
}

func (r *ProjectController) forProject(base *rest.Config, project string) *rest.Config {
	c := rest.CopyConfig(base)
	c.Host = strings.TrimSuffix(base.Host, "/") + "/projects/" + project + "/control-plane"
	return c
}

type projectClients struct {
	Kube    kubernetes.Interface
	Dynamic dynamic.Interface
	Disc    discovery.DiscoveryInterface
}

func buildProjectClients(cfg *rest.Config) (*projectClients, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("http client: %w", err)
	}

	// Prefer *ForConfigAndClient to reuse the transport
	dc, err := dynamic.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("dynamic: %w", err)
	}

	cs, err := kubernetes.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("kube: %w", err)
	}

	disc, err := discovery.NewDiscoveryClientForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}

	return &projectClients{Kube: cs, Dynamic: dc, Disc: disc}, nil
}

func hasResource(
	disc discovery.DiscoveryInterface,
	gv schema.GroupVersion,
	resource string,
) (bool, error) {
	rl, err := disc.ServerResourcesForGroupVersion(gv.String())
	if apierrors.IsNotFound(err) {
		return false, nil // group/version not served yet
	}
	if err != nil {
		return false, fmt.Errorf("discover %s: %w", gv.String(), err)
	}
	for _, r := range rl.APIResources {
		if r.Name == resource {
			return true, nil
		}
	}
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectController) SetupWithManager(mgr ctrl.Manager, infraCluster cluster.Cluster) error {
	r.InfraClient = infraCluster.GetClient()
	r.ControlPlaneClient = mgr.GetClient()
	r.BaseConfig = mgr.GetConfig()
	r.Purger = projectpurge.New()

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		For(&resourcemanagerv1alpha.Project{}).
		WatchesRawSource(source.TypedKind(
			infraCluster.GetCache(),
			&infrastructurev1alpha1.ProjectControlPlane{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, projectControlPlane *infrastructurev1alpha1.ProjectControlPlane) []ctrl.Request {
				projectName, ok := projectControlPlane.Labels[resourcemanagerv1alpha.ProjectNameLabel]
				if !ok {
					return nil
				}
				return []ctrl.Request{
					{
						NamespacedName: types.NamespacedName{
							Name: projectName,
						},
					},
				}
			}),
		)).
		Named("project").
		Complete(r)
}

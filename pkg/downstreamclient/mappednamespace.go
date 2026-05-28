package downstreamclient

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

var _ ResourceStrategy = &MappedNamespaceResourceStrategy{}

// MappedNamespaceResourceStrategy implements [ResourceStrategy] using the
// ns-<upstream-namespace-uid> convention.
//
// When an upstream resource lives in namespace "my-project", this strategy
// looks up that namespace on the upstream cluster, reads its UID, and derives a
// stable downstream namespace name of the form "ns-<uid>". Resources from
// different upstream clusters can therefore coexist in the same downstream API
// server without name collisions.
//
// Ownership is tracked through anchor ConfigMaps written into the downstream
// namespace. The ConfigMap carries the meta.datumapis.com/* labels so that
// [TypedEnqueueRequestsForUpstreamOwner] can re-enqueue the upstream owner
// whenever a downstream resource changes.
//
// Construct with [NewMappedNamespaceResourceStrategy].
type MappedNamespaceResourceStrategy struct {
	upstreamClusterName string
	upstreamClient      client.Client
	downstreamClient    client.Client
}

// NewMappedNamespaceResourceStrategy returns a [ResourceStrategy] that maps
// upstream namespaces to downstream namespaces using the ns-<uid> convention.
//
// upstreamClusterName is a stable identifier for the upstream cluster (e.g. a
// KCP path such as "root:org:project"). It is embedded in labels on downstream
// resources to allow reverse-lookup.
//
// upstreamClient is used to resolve namespace UIDs. downstreamClient is used
// for all write operations.
func NewMappedNamespaceResourceStrategy(
	upstreamClusterName string,
	upstreamClient client.Client,
	downstreamClient client.Client,
) ResourceStrategy {
	return &MappedNamespaceResourceStrategy{
		upstreamClusterName: upstreamClusterName,
		upstreamClient:      upstreamClient,
		downstreamClient:    downstreamClient,
	}
}

// GetClient returns a [client.Client] that automatically ensures the downstream
// namespace exists before each Create call and delegates all other operations
// to the underlying downstream client.
func (c *MappedNamespaceResourceStrategy) GetClient() client.Client {
	return &mappedNamespaceClient{
		client:   c.downstreamClient,
		strategy: c,
	}
}

// ObjectMetaFromUpstreamObject returns a [metav1.ObjectMeta] with the Name
// preserved from the upstream object and the Namespace remapped to the
// downstream ns-<uid> form. The [UpstreamOwnerNamespaceLabel] is set on the
// returned ObjectMeta so the source namespace can be recovered later.
func (c *MappedNamespaceResourceStrategy) ObjectMetaFromUpstreamObject(ctx context.Context, obj metav1.Object) (metav1.ObjectMeta, error) {
	downstreamNamespaceName, err := c.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, obj.GetNamespace())
	if err != nil {
		return metav1.ObjectMeta{}, fmt.Errorf("failed to get downstream namespace name: %w", err)
	}

	return metav1.ObjectMeta{
		Name:      obj.GetName(),
		Namespace: downstreamNamespaceName,
		Labels: map[string]string{
			UpstreamOwnerNamespaceLabel: obj.GetNamespace(),
		},
	}, nil
}

// GetDownstreamNamespaceNameForUpstreamNamespace looks up the upstream
// namespace by name and returns "ns-<uid>".
func (c *MappedNamespaceResourceStrategy) GetDownstreamNamespaceNameForUpstreamNamespace(ctx context.Context, name string) (string, error) {
	namespace, err := c.getUpstreamNamespace(ctx, name)
	if err != nil {
		return "", fmt.Errorf("failed to get downstream namespace: %w", err)
	}

	return fmt.Sprintf("ns-%s", namespace.UID), nil
}

// SetControllerReference establishes a controller ownership relationship
// between owner and controlled in the downstream cluster.
//
// Because cross-cluster owner references are not supported by Kubernetes
// garbage collection, this method creates or retrieves an anchor ConfigMap in
// the same downstream namespace as controlled. The ConfigMap carries the
// meta.datumapis.com/* labels that identify the upstream owner, and controlled
// receives an in-cluster owner reference to that ConfigMap. Deleting the anchor
// (via [DeleteAnchorForObject]) then cascades GC to controlled.
//
// owner and controlled must both be namespaced resources.
func (c *MappedNamespaceResourceStrategy) SetControllerReference(ctx context.Context, owner, controlled metav1.Object, opts ...controllerutil.OwnerReferenceOption) error {
	if owner.GetNamespace() == "" || controlled.GetNamespace() == "" {
		return fmt.Errorf("cluster scoped resource controllers are not supported")
	}

	gvk, err := apiutil.GVKForObject(owner.(runtime.Object), c.upstreamClient.Scheme())
	if err != nil {
		return err
	}

	anchorName := fmt.Sprintf("anchor-%s", owner.GetUID())

	anchorLabels := map[string]string{
		UpstreamOwnerClusterNameLabel: fmt.Sprintf("cluster-%s", strings.ReplaceAll(c.upstreamClusterName, "/", "_")),
		UpstreamOwnerGroupLabel:       gvk.Group,
		UpstreamOwnerKindLabel:        gvk.Kind,
		UpstreamOwnerNameLabel:        owner.GetName(),
		UpstreamOwnerNamespaceLabel:   owner.GetNamespace(),
	}

	downstreamClient := c.GetClient()

	var anchorConfigMap corev1.ConfigMap
	err = downstreamClient.Get(ctx, client.ObjectKey{Namespace: controlled.GetNamespace(), Name: anchorName}, &anchorConfigMap)
	if apierrors.IsNotFound(err) {
		anchorConfigMap = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      anchorName,
				Namespace: controlled.GetNamespace(),
				Labels:    anchorLabels,
			},
		}
		if err := downstreamClient.Create(ctx, &anchorConfigMap); err != nil {
			return fmt.Errorf("failed creating anchor configmap: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed getting anchor configmap: %w", err)
	}

	if err := controllerutil.SetOwnerReference(&anchorConfigMap, controlled, downstreamClient.Scheme(), opts...); err != nil {
		return fmt.Errorf("failed setting anchor owner reference: %w", err)
	}

	labels := controlled.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[UpstreamOwnerClusterNameLabel] = anchorLabels[UpstreamOwnerClusterNameLabel]
	labels[UpstreamOwnerGroupLabel] = anchorLabels[UpstreamOwnerGroupLabel]
	labels[UpstreamOwnerKindLabel] = anchorLabels[UpstreamOwnerKindLabel]
	labels[UpstreamOwnerNameLabel] = anchorLabels[UpstreamOwnerNameLabel]
	labels[UpstreamOwnerNamespaceLabel] = anchorLabels[UpstreamOwnerNamespaceLabel]
	controlled.SetLabels(labels)

	return nil
}

// SetOwnerReference establishes a non-controller ownership relationship between
// owner and object using the downstream cluster's scheme. This does not create
// an anchor ConfigMap; it sets an in-cluster owner reference directly.
func (c *MappedNamespaceResourceStrategy) SetOwnerReference(ctx context.Context, owner, object metav1.Object, opts ...controllerutil.OwnerReferenceOption) error {
	return controllerutil.SetOwnerReference(owner, object, c.downstreamClient.Scheme(), opts...)
}

// DeleteAnchorForObject deletes the anchor ConfigMap associated with owner.
// Kubernetes garbage collection then cascades to all downstream resources that
// hold an owner reference to the anchor.
//
// If the anchor does not exist the call is a no-op.
func (c *MappedNamespaceResourceStrategy) DeleteAnchorForObject(ctx context.Context, owner client.Object) error {
	anchorName := fmt.Sprintf("anchor-%s", owner.GetUID())

	downstreamObjectMeta, err := c.ObjectMetaFromUpstreamObject(ctx, owner)
	if err != nil {
		return fmt.Errorf("failed to get downstream object metadata: %w", err)
	}

	downstreamClient := c.GetClient()

	var configMap corev1.ConfigMap
	if err := downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamObjectMeta.Namespace, Name: anchorName}, &configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed getting anchor configmap: %w", err)
	}

	return downstreamClient.Delete(ctx, &configMap)
}

// getUpstreamNamespace retrieves the named namespace from the upstream cluster.
func (c *MappedNamespaceResourceStrategy) getUpstreamNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	if c.upstreamClient == nil {
		return nil, fmt.Errorf("upstream client is nil")
	}

	namespace := &corev1.Namespace{}
	if err := c.upstreamClient.Get(ctx, client.ObjectKey{Name: name}, namespace); err != nil {
		return nil, fmt.Errorf("failed to get upstream namespace: %w", err)
	}

	return namespace, nil
}

// ensureDownstreamNamespace creates or updates the downstream namespace,
// labelling it with the upstream cluster name and source namespace.
func (c *MappedNamespaceResourceStrategy) ensureDownstreamNamespace(ctx context.Context, obj metav1.Object) (*corev1.Namespace, error) {
	downstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: obj.GetNamespace(),
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, c.downstreamClient, downstreamNamespace, func() error {
		if downstreamNamespace.Labels == nil {
			downstreamNamespace.Labels = make(map[string]string)
		}

		downstreamNamespace.Labels[UpstreamOwnerClusterNameLabel] = fmt.Sprintf("cluster-%s", strings.ReplaceAll(c.upstreamClusterName, "/", "_"))

		if v, ok := obj.GetLabels()[UpstreamOwnerNamespaceLabel]; ok {
			downstreamNamespace.Labels[UpstreamOwnerNamespaceLabel] = v
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to ensure downstream namespace: %w", err)
	}

	return downstreamNamespace, nil
}

// mappedNamespaceClient wraps a [client.Client] and ensures the downstream
// namespace exists before every Create call.
var _ client.Client = &mappedNamespaceClient{}

type mappedNamespaceClient struct {
	client   client.Client
	strategy *MappedNamespaceResourceStrategy
}

func (c *mappedNamespaceClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, err := c.strategy.ensureDownstreamNamespace(ctx, obj); err != nil {
		return fmt.Errorf("failed to ensure downstream namespace: %w", err)
	}

	return c.client.Create(ctx, obj, opts...)
}

func (c *mappedNamespaceClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return c.client.Delete(ctx, obj, opts...)
}

func (c *mappedNamespaceClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return c.client.DeleteAllOf(ctx, obj, opts...)
}

func (c *mappedNamespaceClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.client.Get(ctx, key, obj, opts...)
}

func (c *mappedNamespaceClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return c.client.List(ctx, list, opts...)
}

func (c *mappedNamespaceClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return c.client.Apply(ctx, obj, opts...)
}

func (c *mappedNamespaceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return c.client.Patch(ctx, obj, patch, opts...)
}

func (c *mappedNamespaceClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return c.client.Update(ctx, obj, opts...)
}

func (c *mappedNamespaceClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	return c.client.Apply(ctx, obj, opts...)
}

func (c *mappedNamespaceClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return c.client.GroupVersionKindFor(obj)
}

func (c *mappedNamespaceClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return c.client.IsObjectNamespaced(obj)
}

func (c *mappedNamespaceClient) Scheme() *runtime.Scheme {
	return c.client.Scheme()
}

func (c *mappedNamespaceClient) RESTMapper() meta.RESTMapper {
	return c.client.RESTMapper()
}

func (c *mappedNamespaceClient) Status() client.SubResourceWriter {
	return c.client.Status()
}

func (c *mappedNamespaceClient) SubResource(subResource string) client.SubResourceClient {
	return c.client.SubResource(subResource)
}

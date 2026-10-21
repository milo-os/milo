package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	rm "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Gateway API v1.5.1 standard bundle, pinned by the reviewed NSO source.
//
//go:embed testdata/appsownership/httproutes.gateway.networking.k8s.io.yaml
var apiFixtureHTTPRouteCRD []byte

const nativeWorkloadFinalizer = "compute.datumapis.com/workload-controller"
const nativeRouteFinalizer = "gateway.networking.datumapis.com/gateway-controller-gc"
const unrelatedFinalizer = "fixtures.apps.test/unrelated"

type nativeWriterResource struct{ name, group, version, resource, kind, finalizer string }

var nativeWriterResources = []nativeWriterResource{
	{"workload", "compute.datumapis.com", "v1alpha", "workloads", "Workload", nativeWorkloadFinalizer},
	{"route-v1", "gateway.networking.k8s.io", "v1", "httproutes", "HTTPRoute", nativeRouteFinalizer},
	{"route-v1beta1", "gateway.networking.k8s.io", "v1beta1", "httproutes", "HTTPRoute", nativeRouteFinalizer},
}
var nativeWriterSuffixes = []string{"status", "add-put", "add-merge", "add-apply", "remove-put", "remove-merge", "remove-apply", "last-put", "last-merge", "last-apply", "foreign", "partial", "annotation", "empty", "owner", "reacquire"}

// This finite allowlist is reachable only by the already-scoped test setup
// identity, namespace, main-resource requests and one-way gate in #868.
func nativeWriterSetupPermitted(a admission.Attributes) bool {
	for _, r := range nativeWriterResources {
		if a.GetResource() != (schema.GroupVersionResource{Group: r.group, Version: r.version, Resource: r.resource}) {
			continue
		}
		for _, suffix := range nativeWriterSuffixes {
			if a.GetName() == r.name+"-"+suffix {
				return true
			}
		}
	}
	return false
}

func (r nativeWriterResource) object(name string, marks map[string]string) *unstructured.Unstructured {
	spec := map[string]any{"hostnames": []any{"probe.example.test"}, "rules": []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}}, "backendRefs": []any{map[string]any{"name": "probe-backend", "port": int64(8080)}}}}}
	if r.kind == "Workload" {
		spec = map[string]any{"placements": []any{map[string]any{"name": "default", "locations": []any{map[string]any{"name": "dfw"}}, "scaleSettings": map[string]any{"minReplicas": int64(1), "instanceManagementPolicy": "OrderedReady"}}}, "template": map[string]any{"spec": map[string]any{"runtime": map[string]any{"resources": map[string]any{"instanceType": "datumcloud-d1-standard-2"}}, "networkInterfaces": []any{map[string]any{"network": map[string]any{"name": "probe-network"}}}}}}
	}
	o := &unstructured.Unstructured{Object: map[string]any{"apiVersion": r.group + "/" + r.version, "kind": r.kind, "spec": spec}}
	o.SetName(name)
	o.SetNamespace(apiFixtureNamespace)
	o.SetLabels(marks)
	return o
}
func (r nativeWriterResource) status(sequence int64) map[string]any {
	if r.kind == "Workload" {
		return map[string]any{"currentReplicas": int64(0), "deployments": int64(0), "desiredReplicas": sequence, "readyReplicas": int64(0), "replicas": int64(0), "updatedReplicas": int64(0)}
	}
	return map[string]any{"parents": []any{map[string]any{"parentRef": map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "probe-gateway"}, "controllerName": "fixtures.apps.test/native", "conditions": []any{map[string]any{"lastTransitionTime": "2026-10-20T00:00:00Z", "message": fmt.Sprintf("sequence %d", sequence), "reason": "Probe", "status": "False", "type": "Accepted"}}}}}
}

type fixtureHeaders struct {
	next    http.RoundTripper
	headers http.Header
}

func (r fixtureHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	for name, values := range r.headers {
		copy.Header[name] = append([]string(nil), values...)
	}
	return r.next.RoundTrip(copy)
}
func nativeWriterClient(t *testing.T, f *apiFixture, identity string, headers http.Header) client.Client {
	t.Helper()
	cfg := rest.CopyConfig(f.projectConfig)
	cfg.BearerToken = f.tokens[identity]
	require.NotEmpty(t, cfg.BearerToken)
	if headers != nil {
		cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper { return fixtureHeaders{rt, headers} }
	}
	c, err := client.New(cfg, client.Options{Scheme: f.scheme})
	require.NoError(t, err)
	return c
}
func getNative(t *testing.T, f *apiFixture, original *unstructured.Unstructured) *unstructured.Unstructured {
	t.Helper()
	got := original.DeepCopy()
	require.NoError(t, f.seed.Get(f.ctx, client.ObjectKeyFromObject(got), got))
	return got
}
func requireNativeDenied(t *testing.T, err error) {
	t.Helper()
	require.Truef(t, apierrors.IsForbidden(err), "expected guard denial: %v", err)
	require.Contains(t, err.Error(), "Apps ownership")
}
func requireNativeUnchanged(t *testing.T, f *apiFixture, before *unstructured.Unstructured) {
	t.Helper()
	require.Equal(t, before, getNative(t, f, before))
}

func writeNativeFinalizers(ctx context.Context, c client.Client, mode string, old *unstructured.Unstructured, finalizers []string, opts ...client.UpdateOption) error {
	next := old.DeepCopy()
	next.SetFinalizers(finalizers)
	if mode == "put" {
		return c.Update(ctx, next, opts...)
	}
	value := map[string]any{"metadata": map[string]any{"uid": string(old.GetUID()), "resourceVersion": old.GetResourceVersion(), "finalizers": finalizers}}
	patchType := types.MergePatchType
	var patchOpts []client.PatchOption
	if mode == "apply" {
		patchType = types.ApplyPatchType
		value["apiVersion"] = old.GetAPIVersion()
		value["kind"] = old.GetKind()
		m := value["metadata"].(map[string]any)
		m["name"] = old.GetName()
		m["namespace"] = old.GetNamespace()
		patchOpts = []client.PatchOption{client.FieldOwner("native-finalizer-fixture"), client.ForceOwnership}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.Patch(ctx, next, client.RawPatch(patchType, raw), patchOpts...)
}
func writeNativeStatus(ctx context.Context, c client.Client, mode string, old *unstructured.Unstructured, status map[string]any) error {
	next := old.DeepCopy()
	next.Object["status"] = status
	if mode == "put" {
		return c.Status().Update(ctx, next)
	}
	value := map[string]any{"metadata": map[string]any{"uid": string(old.GetUID()), "resourceVersion": old.GetResourceVersion()}, "status": status}
	patchType := types.MergePatchType
	var opts []client.SubResourcePatchOption
	if mode == "apply" {
		patchType = types.ApplyPatchType
		value["apiVersion"] = old.GetAPIVersion()
		value["kind"] = old.GetKind()
		m := value["metadata"].(map[string]any)
		m["name"] = old.GetName()
		m["namespace"] = old.GetNamespace()
		opts = []client.SubResourcePatchOption{client.FieldOwner("native-status-fixture"), client.ForceOwnership}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.Status().Patch(ctx, next, client.RawPatch(patchType, raw), opts...)
}
func outsideNativeFields(obj *unstructured.Unstructured, fields ...string) map[string]any {
	copy := obj.DeepCopy()
	copy.SetResourceVersion("")
	copy.SetManagedFields(nil)
	for _, field := range fields {
		if field == "status" {
			delete(copy.Object, "status")
		} else {
			unstructured.RemoveNestedField(copy.Object, "metadata", field)
		}
	}
	return copy.Object
}

func TestAppsNativeWritersAPIFixture(t *testing.T) {
	config := &appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration"}
	for _, r := range nativeWriterResources {
		status := appsownership.StatusWriter{Username: "native-status", UID: "native-status-uid", Group: r.group, Version: r.version, Resource: r.resource, Kind: r.kind}
		finalizer := status
		finalizer.Username = "native-finalizer"
		finalizer.UID = "native-finalizer-uid"
		config.StatusWriters = append(config.StatusWriters, status)
		config.FinalizerWriters = append(config.FinalizerWriters, appsownership.FinalizerWriter{StatusWriter: finalizer, Finalizer: r.finalizer})
	}
	f := startAPIFixture(t, config)
	require.Equal(t, "682203b109baecb18d38073a578f7bc2c17d22fe9b0c27bf030850abe014c370", fmt.Sprintf("%x", sha256.Sum256(apiFixtureHTTPRouteCRD)))
	crd := &extv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(apiFixtureHTTPRouteCRD, crd))
	// Upstream schema is unchanged; only Milo project-context discovery is added.
	crd.Annotations["discovery.miloapis.com/parent-contexts"] = "Project"
	require.NoError(t, f.root.Create(f.ctx, crd))
	require.Eventually(t, func() bool {
		got := &extv1.CustomResourceDefinition{}
		if f.root.Get(f.ctx, client.ObjectKeyFromObject(crd), got) != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == extv1.Established && c.Status == extv1.ConditionTrue {
				return true
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
	marks := map[string]string{appsownership.ProjectUIDLabel: string(f.project.UID), appsownership.AppUIDLabel: "fixture-app"}
	fixtures := map[string]*unstructured.Unstructured{}
	for _, r := range nativeWriterResources {
		for _, suffix := range nativeWriterSuffixes {
			if suffix == "reacquire" {
				continue
			}
			o := r.object(r.name+"-"+suffix, marks)
			switch suffix {
			case "foreign":
				o.SetLabels(map[string]string{appsownership.ProjectUIDLabel: "foreign-project-uid", appsownership.AppUIDLabel: "fixture-app"})
			case "partial":
				o.SetLabels(map[string]string{appsownership.ProjectUIDLabel: string(f.project.UID)})
			case "annotation":
				o.SetAnnotations(map[string]string{appsownership.AppUIDLabel: "alias"})
			case "empty":
				o.SetLabels(map[string]string{appsownership.ProjectUIDLabel: string(f.project.UID), appsownership.AppUIDLabel: ""})
			case "owner":
				o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "external", UID: "external-uid"}})
			}
			if len(suffix) > 4 && suffix[:4] == "add-" {
				o.SetFinalizers([]string{unrelatedFinalizer})
			}
			if len(suffix) > 7 && suffix[:7] == "remove-" {
				o.SetFinalizers([]string{unrelatedFinalizer})
			}
			var err error
			require.Eventually(t, func() bool { err = f.seed.Create(f.ctx, o); return err == nil }, 20*time.Second, 100*time.Millisecond, "create native fixture: %v", err)
			if (len(suffix) > 7 && suffix[:7] == "remove-") || (len(suffix) > 5 && suffix[:5] == "last-") {
				// SSA removal can only remove list entries owned by that field
				// manager. Establish that ownership through API apply, not by
				// constructing managedFields or modifying stored records.
				finalizers := append(o.GetFinalizers(), r.finalizer)
				require.NoError(t, writeNativeFinalizers(f.ctx, f.seed, "apply", o, finalizers))
				o = getNative(t, f, o)
				uid, rv := o.GetUID(), o.GetResourceVersion()
				require.NoError(t, f.seed.Delete(f.ctx, o, client.Preconditions{UID: &uid, ResourceVersion: &rv}))
				o = getNative(t, f, o)
				require.False(t, o.GetDeletionTimestamp().IsZero())
			}
			fixtures[o.GetName()] = o
		}
	}
	f.seal()
	statusClient := nativeWriterClient(t, f, "native-status", nil)
	finalizerClient := nativeWriterClient(t, f, "native-finalizer", nil)
	for _, r := range nativeWriterResources {
		t.Run(r.name, func(t *testing.T) {
			original := fixtures[r.name+"-status"]
			for _, identity := range []string{"customer", apiFixtureSetupName, "system-admin", "native-status", "native-finalizer"} {
				c := nativeWriterClient(t, f, identity, nil)
				requireNativeDenied(t, c.Create(f.ctx, r.object(r.name+"-reacquire", marks)))
			}
			for index, mode := range []string{"put", "merge", "apply"} {
				t.Run("status-"+mode, func(t *testing.T) {
					current := getNative(t, f, original)
					before := outsideNativeFields(current, "status")
					payload := r.status(int64(index + 1))
					require.NoError(t, writeNativeStatus(f.ctx, statusClient, mode, current, payload))
					got := getNative(t, f, current)
					require.Equal(t, payload, got.Object["status"])
					require.Equal(t, before, outsideNativeFields(got, "status"))
				})
			}
			current := getNative(t, f, original)
			// Native status strategy restores non-status fields before admission.
			malicious := current.DeepCopy()
			malicious.Object["spec"] = map[string]any{"attempted": "replacement"}
			malicious.SetLabels(nil)
			malicious.SetFinalizers([]string{"foreign.example/finalizer"})
			require.NoError(t, statusClient.Status().Update(f.ctx, malicious))
			require.Equal(t, outsideNativeFields(current, "status"), outsideNativeFields(getNative(t, f, current), "status"))
			current = getNative(t, f, original)
			for _, identity := range []string{"customer", apiFixtureSetupName, "system-admin", "native-finalizer", "wrong-status-uid"} {
				requireNativeDenied(t, writeNativeStatus(f.ctx, nativeWriterClient(t, f, identity, nil), "merge", current, r.status(99)))
			}
			requireNativeDenied(t, writeNativeFinalizers(f.ctx, statusClient, "merge", current, []string{r.finalizer}))
			requireNativeDenied(t, statusClient.Delete(f.ctx, current))
			requireNativeDenied(t, finalizerClient.Delete(f.ctx, current))
			requireNativeUnchanged(t, f, current)
			for _, principal := range []string{"native-status", "native-finalizer"} {
				for _, headers := range []http.Header{{"Impersonate-User": []string{principal}, "Impersonate-Uid": []string{principal + "-uid"}, "Impersonate-Group": []string{"system:masters"}, "Impersonate-Extra-test": []string{"asserted"}}, {"Impersonate-User": []string{""}}, {"Impersonate-Unknown": []string{"ignored"}}} {
					for _, identity := range []string{principal, "system-admin"} {
						c := nativeWriterClient(t, f, identity, headers)
						if principal == "native-status" {
							requireNativeDenied(t, writeNativeStatus(f.ctx, c, "merge", current, r.status(101)))
						} else {
							requireNativeDenied(t, writeNativeFinalizers(f.ctx, c, "merge", current, []string{r.finalizer}))
						}
					}
				}
			}
			requireNativeUnchanged(t, f, current)
			for _, suffix := range []string{"foreign", "partial", "annotation", "empty", "owner"} {
				invalid := fixtures[r.name+"-"+suffix]
				requireNativeDenied(t, writeNativeStatus(f.ctx, statusClient, "merge", invalid, r.status(42)))
				requireNativeDenied(t, writeNativeFinalizers(f.ctx, finalizerClient, "merge", invalid, []string{r.finalizer}))
				requireNativeUnchanged(t, f, invalid)
			}
			for _, mode := range []string{"put", "merge", "apply"} {
				t.Run("finalizers-"+mode, func(t *testing.T) {
					add := fixtures[r.name+"-add-"+mode]
					for _, identity := range []string{"customer", apiFixtureSetupName, "system-admin", "native-status", "wrong-finalizer-uid"} {
						requireNativeDenied(t, writeNativeFinalizers(f.ctx, nativeWriterClient(t, f, identity, nil), mode, add, []string{unrelatedFinalizer, r.finalizer}))
					}
					for _, mutate := range map[string]func(*unstructured.Unstructured){
						"other finalizer": func(o *unstructured.Unstructured) {
							o.SetFinalizers([]string{unrelatedFinalizer, r.finalizer, "foreign.example/hold"})
						},
						"remove unrelated": func(o *unstructured.Unstructured) { o.SetFinalizers([]string{r.finalizer}) },
						"marker": func(o *unstructured.Unstructured) {
							o.SetLabels(map[string]string{appsownership.ProjectUIDLabel: string(f.project.UID), appsownership.AppUIDLabel: "replacement-app"})
						},
						"strip":      func(o *unstructured.Unstructured) { o.SetLabels(nil) },
						"annotation": func(o *unstructured.Unstructured) { o.SetAnnotations(map[string]string{"extra": "changed"}) },
						"owner": func(o *unstructured.Unstructured) {
							o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "outside", UID: "external-uid"}})
						},
						"spec": func(o *unstructured.Unstructured) {
							if r.kind == "HTTPRoute" {
								o.Object["spec"].(map[string]any)["hostnames"] = []any{"changed.example.test"}
							} else {
								o.Object["spec"].(map[string]any)["placements"].([]any)[0].(map[string]any)["scaleSettings"].(map[string]any)["minReplicas"] = int64(2)
							}
						},
					} {
						candidate := add.DeepCopy()
						candidate.SetFinalizers([]string{unrelatedFinalizer, r.finalizer})
						mutate(candidate)
						requireNativeDenied(t, finalizerClient.Update(f.ctx, candidate))
						requireNativeUnchanged(t, f, add)
					}
					require.NoError(t, writeNativeFinalizers(f.ctx, finalizerClient, "put", add, []string{unrelatedFinalizer, r.finalizer}, client.DryRunAll))
					requireNativeUnchanged(t, f, add)
					require.NoError(t, writeNativeFinalizers(f.ctx, finalizerClient, mode, add, []string{unrelatedFinalizer, r.finalizer}))
					added := getNative(t, f, add)
					require.Equal(t, []string{unrelatedFinalizer, r.finalizer}, added.GetFinalizers())
					require.Equal(t, outsideNativeFields(add, "finalizers"), outsideNativeFields(added, "finalizers"))
					requireNativeDenied(t, writeNativeFinalizers(f.ctx, finalizerClient, mode, added, []string{unrelatedFinalizer}))
					requireNativeDenied(t, finalizerClient.Delete(f.ctx, added))
					candidate := added.DeepCopy()
					now := metav1.Now()
					candidate.SetDeletionTimestamp(&now)
					require.Error(t, finalizerClient.Update(f.ctx, candidate))
					requireNativeUnchanged(t, f, added)
					remove := fixtures[r.name+"-remove-"+mode]
					// Force an actual removal of both entries. SSA omission of another
					// manager's set element preserves it, and would legitimately
					// remove only this controller's finalizer.
					requireNativeDenied(t, writeNativeFinalizers(f.ctx, finalizerClient, "put", remove, []string{}))
					require.NoError(t, writeNativeFinalizers(f.ctx, finalizerClient, mode, remove, []string{unrelatedFinalizer}))
					removed := getNative(t, f, remove)
					require.Equal(t, []string{unrelatedFinalizer}, removed.GetFinalizers())
					require.Equal(t, outsideNativeFields(remove, "finalizers"), outsideNativeFields(removed, "finalizers"))
					require.Error(t, writeNativeFinalizers(f.ctx, finalizerClient, mode, removed, []string{unrelatedFinalizer, r.finalizer}))
					requireNativeUnchanged(t, f, removed)
					last := fixtures[r.name+"-last-"+mode]
					require.NoError(t, writeNativeFinalizers(f.ctx, finalizerClient, mode, last, []string{}))
					require.True(t, apierrors.IsNotFound(f.seed.Get(f.ctx, client.ObjectKeyFromObject(last), last.DeepCopy())))
					replacement := r.object(last.GetName(), nil)
					require.NoError(t, f.customer.Create(f.ctx, replacement))
					require.NotEqual(t, last.GetUID(), replacement.GetUID())
					require.Error(t, writeNativeFinalizers(f.ctx, finalizerClient, mode, last, []string{}))
					requireNativeUnchanged(t, f, replacement)
				})
			}
		})
	}
	t.Run("authoritative Project termination and UID reuse", func(t *testing.T) {
		project := f.project.DeepCopy()
		project.Finalizers = []string{"fixtures.apps.test/project-hold"}
		require.NoError(t, f.root.Update(f.ctx, project))
		require.NoError(t, f.root.Delete(f.ctx, project, client.Preconditions{UID: &project.UID, ResourceVersion: &project.ResourceVersion}))
		require.NoError(t, f.root.Get(f.ctx, client.ObjectKeyFromObject(project), project))
		require.False(t, project.DeletionTimestamp.IsZero())
		checkDenied := func() {
			for _, r := range nativeWriterResources {
				o := getNative(t, f, fixtures[r.name+"-status"])
				requireNativeDenied(t, writeNativeStatus(f.ctx, statusClient, "merge", o, r.status(250)))
				requireNativeDenied(t, writeNativeFinalizers(f.ctx, finalizerClient, "merge", o, []string{r.finalizer}))
				requireNativeUnchanged(t, f, o)
			}
		}
		checkDenied()
		project.Finalizers = nil
		require.NoError(t, f.root.Update(f.ctx, project))
		require.True(t, apierrors.IsNotFound(f.root.Get(f.ctx, client.ObjectKeyFromObject(project), &rm.Project{})))
		checkDenied()
		replacement := &rm.Project{ObjectMeta: metav1.ObjectMeta{Name: project.Name}, Spec: project.Spec}
		require.NoError(t, f.root.Create(f.ctx, replacement))
		require.NotEqual(t, project.UID, replacement.UID)
		checkDenied()
	})
}

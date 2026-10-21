package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	rm "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func namespaceAt(t *testing.T, f *apiFixture, c client.Client, name string) *corev1.Namespace {
	t.Helper()
	got := &corev1.Namespace{}
	require.NoError(t, c.Get(f.ctx, client.ObjectKey{Name: name}, got))
	return got
}
func finishNamespace(f *apiFixture, c client.Client, ns *corev1.Namespace) error {
	next := ns.DeepCopy()
	next.Spec.Finalizers = nil
	return c.SubResource("finalize").Update(f.ctx, next)
}

type namespaceLostReply struct {
	next    http.RoundTripper
	dropped *atomic.Bool
}

func (r namespaceLostReply) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.next.RoundTrip(req)
	if err == nil && req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/namespaces/"+namespaceRaceName) && response.StatusCode >= 200 && response.StatusCode < 300 && r.dropped.CompareAndSwap(false, true) {
		response.Body.Close()
		return nil, fmt.Errorf("test fixture discarded a successful reservation response")
	}
	return response, err
}

func TestAppsNamespaceReservationAPIFixture(t *testing.T) {
	r := nativeWriterResources[0]
	statusRule := appsownership.StatusWriter{Username: "native-status", UID: "native-status-uid", Group: r.group, Version: r.version, Resource: r.resource, Kind: r.kind}
	finalizerRule := statusRule
	finalizerRule.Username = "native-finalizer"
	finalizerRule.UID = "native-finalizer-uid"
	config := &appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration", StatusWriters: []appsownership.StatusWriter{statusRule}, FinalizerWriters: []appsownership.FinalizerWriter{{StatusWriter: finalizerRule, Finalizer: r.finalizer}}}
	first := startAPIFixture(t, config)
	second := startAPIFixtureReplica(t, first)
	key := appsownership.NamespaceReservationKey
	held := map[string]*corev1.Namespace{}
	for _, name := range []string{"ns-held-annotation", "ns-held-label", "ns-held-empty", "ns-terminating-held"} {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		if name == "ns-held-label" {
			ns.Labels = map[string]string{key: "fixture-reservation"}
		} else {
			value := "fixture-reservation"
			if name == "ns-held-empty" {
				value = ""
			}
			ns.Annotations = map[string]string{key: value}
		}
		require.NoError(t, first.seed.Create(first.ctx, ns))
		if name == "ns-terminating-held" {
			require.NoError(t, first.seed.Delete(first.ctx, ns, client.Preconditions{UID: &ns.UID, ResourceVersion: &ns.ResourceVersion}))
		}
		held[name] = namespaceAt(t, first, first.seed, name)
	}
	marks := map[string]string{appsownership.ProjectUIDLabel: string(first.project.UID), appsownership.AppUIDLabel: "fixture-app"}
	nativeHeld := r.object("held-workload", marks)
	nativeHeld.SetAnnotations(map[string]string{key: "fixture-reservation"})
	require.NoError(t, first.seed.Create(first.ctx, nativeHeld))
	nativeOrdinary := r.object("workload-status", marks)
	require.NoError(t, first.seed.Create(first.ctx, nativeOrdinary))
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "held-configmap", Annotations: map[string]string{key: ""}}}
	require.NoError(t, first.seed.Create(first.ctx, configMap))

	// The reverse ordering needs no timing hook: ordinary DELETE first, then a
	// stale reservation write through the still-open test setup gate.
	deletedFirst := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-delete-wins"}}
	require.NoError(t, first.customer.Create(first.ctx, deletedFirst))
	old := deletedFirst.DeepCopy()
	require.NoError(t, first.customer.Delete(first.ctx, deletedFirst, client.Preconditions{UID: &deletedFirst.UID, ResourceVersion: &deletedFirst.ResourceVersion}))
	stale := old.DeepCopy()
	stale.Annotations = map[string]string{key: "too-late"}
	stale.Finalizers = []string{"fixtures.apps.test/reservation"}
	require.True(t, apierrors.IsConflict(second.seed.Update(first.ctx, stale)))
	terminating := namespaceAt(t, first, second.seed, old.Name)
	require.False(t, terminating.DeletionTimestamp.IsZero())
	late := terminating.DeepCopy()
	late.Annotations = stale.Annotations
	late.Finalizers = stale.Finalizers
	require.True(t, apierrors.IsInvalid(second.seed.Update(first.ctx, late)), "Kubernetes must reject new metadata finalizers after termination")
	require.Equal(t, terminating, namespaceAt(t, first, first.seed, old.Name))
	require.NoError(t, finishNamespace(first, first.customer, terminating))
	require.True(t, apierrors.IsNotFound(second.customer.Get(first.ctx, client.ObjectKeyFromObject(terminating), &corev1.Namespace{})))
	replacement := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: old.Name}}
	require.NoError(t, second.customer.Create(first.ctx, replacement))
	require.NotEqual(t, old.UID, replacement.UID)
	err := second.seed.Update(first.ctx, stale)
	require.Truef(t, apierrors.IsConflict(err) || apierrors.IsInvalid(err), "stale UID/RV adopted replacement: %v", err)
	replacement = namespaceAt(t, first, first.seed, replacement.Name)

	// A DELETE is paused only after the actual compiled validator accepts its
	// old unreserved object. All fixture mutation still uses the public API.
	race := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceRaceName}}
	require.NoError(t, first.customer.Create(first.ctx, race))
	raceConfig := rest.CopyConfig(first.projectConfig)
	raceConfig.BearerToken = first.tokens["customer"]
	raceConfig.Timeout = 30 * time.Second
	raceClient, err := client.New(raceConfig, client.Options{Scheme: first.scheme})
	require.NoError(t, err)
	deleted := make(chan error, 1)
	go func() { deleted <- raceClient.Delete(first.ctx, race, client.Preconditions{UID: &race.UID}) }()
	defer first.signal('R')
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(first.ackPath + ".delete-validated")
		return err == nil && string(data) == "validated"
	}, 10*time.Second, 20*time.Millisecond)
	reservation := race.DeepCopy()
	reservation.Annotations = map[string]string{key: "fixture-reservation"}
	reservation.Finalizers = []string{"fixtures.apps.test/reservation"}
	var dropped atomic.Bool
	lostConfig := rest.CopyConfig(second.projectConfig)
	lostConfig.WrapTransport = func(rt http.RoundTripper) http.RoundTripper { return namespaceLostReply{rt, &dropped} }
	lostClient, err := client.New(lostConfig, client.Options{Scheme: first.scheme})
	require.NoError(t, err)
	require.Error(t, lostClient.Update(first.ctx, reservation))
	require.True(t, dropped.Load())
	recovered := namespaceAt(t, first, first.customer, race.Name)
	require.Equal(t, race.UID, recovered.UID)
	require.NotEqual(t, race.ResourceVersion, recovered.ResourceVersion)
	require.Equal(t, "fixture-reservation", recovered.Annotations[key])
	require.True(t, recovered.DeletionTimestamp.IsZero())
	// Every setup request has completed. No setup acquisition can follow these
	// acknowledgements, including retries after the discarded response.
	first.seal()
	second.seal()
	first.signal('R')
	select {
	case err := <-deleted:
		requireNativeDenied(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("paused DELETE did not finish")
	}
	attempts, err := os.ReadFile(first.ackPath + ".delete-attempts")
	require.NoError(t, err)
	count, err := strconv.Atoi(string(attempts))
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 2, "DELETE must revalidate the updated namespace")
	require.Equal(t, recovered, namespaceAt(t, first, second.customer, race.Name))
	held[race.Name] = recovered

	for index, replica := range []*apiFixture{first, second} {
		t.Run(fmt.Sprintf("replica-%d", index+1), func(t *testing.T) {
			for _, identity := range []string{"customer", apiFixtureSetupName, "system-admin", "native-status", "native-finalizer"} {
				c := nativeWriterClient(t, replica, identity, nil)
				for _, location := range []string{"label", "annotation"} {
					forged := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-reacquire"}}
					if location == "label" {
						forged.Labels = map[string]string{key: ""}
					} else {
						forged.Annotations = map[string]string{key: ""}
					}
					requireNativeDenied(t, c.Create(first.ctx, forged))
					fresh := replacement.DeepCopy()
					fresh.Annotations = map[string]string{key: "new-hold"}
					requireNativeDenied(t, c.Update(first.ctx, fresh))
				}
			}
			// A post-seal retry is denied even after setup lost a successful response.
			requireNativeDenied(t, replica.seed.Update(first.ctx, recovered.DeepCopy()))
			for name, before := range held {
				for _, c := range []client.Client{replica.customer, replica.seed} {
					candidate := before.DeepCopy()
					candidate.Annotations = nil
					candidate.Labels = nil
					candidate.Finalizers = nil
					requireNativeDenied(t, c.Update(first.ctx, candidate))
					candidate = before.DeepCopy()
					requireNativeDenied(t, c.Patch(first.ctx, candidate, client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"annotations":null,"labels":null,"finalizers":[]}}`))))
					candidate = before.DeepCopy()
					candidate.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace"))
					candidate.ManagedFields = nil
					raw, err := json.Marshal(candidate)
					require.NoError(t, err)
					requireNativeDenied(t, c.Patch(first.ctx, candidate, client.RawPatch(types.ApplyPatchType, raw), client.FieldOwner("namespace-reservation-fixture"), client.ForceOwnership))
					candidate = before.DeepCopy()
					candidate.Status.Conditions = []corev1.NamespaceCondition{{Type: corev1.NamespaceDeletionDiscoveryFailure, Status: corev1.ConditionFalse, Reason: "Fixture", Message: "status mutation", LastTransitionTime: metav1.Now()}}
					requireNativeDenied(t, c.Status().Update(first.ctx, candidate))
					candidate = before.DeepCopy()
					candidate.Spec.Finalizers = nil
					candidate.Annotations = nil
					candidate.Labels = nil
					requireNativeDenied(t, c.SubResource("finalize").Update(first.ctx, candidate))
					if before.DeletionTimestamp.IsZero() {
						requireNativeDenied(t, c.Delete(first.ctx, before, client.Preconditions{UID: &before.UID}))
					} else {
						// Kubernetes can return the existing terminating namespace without a
						// mutation or new DELETE admission. The full object must stay unchanged.
						err := c.Delete(first.ctx, before, client.Preconditions{UID: &before.UID})
						require.Truef(t, err == nil || apierrors.IsForbidden(err), "repeated terminating DELETE: %v", err)
					}
					require.Equal(t, before, namespaceAt(t, first, replica.customer, name))
				}
			}
			require.Equal(t, replacement, namespaceAt(t, first, replica.customer, replacement.Name))
			requireNativeDenied(t, replica.customer.DeleteAllOf(first.ctx, &corev1.ConfigMap{}, client.InNamespace(apiFixtureNamespace), client.MatchingFields{"metadata.name": configMap.Name}))
			gotConfigMap := &corev1.ConfigMap{}
			require.NoError(t, replica.customer.Get(first.ctx, client.ObjectKeyFromObject(configMap), gotConfigMap))
			require.Equal(t, configMap, gotConfigMap)
			// Namespace DELETE collection is not served by Kubernetes; do not claim
			// that unsupported operation exercised the ownership guard.
			require.True(t, apierrors.IsMethodNotSupported(replica.customer.DeleteAllOf(first.ctx, &corev1.Namespace{}, client.MatchingFields{"metadata.name": "ns-held-label"})))
			status := nativeWriterClient(t, replica, "native-status", nil)
			finalizer := nativeWriterClient(t, replica, "native-finalizer", nil)
			requireNativeDenied(t, writeNativeStatus(first.ctx, status, "merge", getNative(t, first, nativeHeld), r.status(1)))
			requireNativeDenied(t, writeNativeFinalizers(first.ctx, finalizer, "merge", getNative(t, first, nativeHeld), []string{r.finalizer}))
			requireNativeUnchanged(t, first, nativeHeld)
		})
	}
	// Same principals remain valid when this independent reservation is absent.
	require.NoError(t, writeNativeStatus(first.ctx, nativeWriterClient(t, first, "native-status", nil), "merge", getNative(t, first, nativeOrdinary), r.status(2)))
	require.NoError(t, writeNativeFinalizers(first.ctx, nativeWriterClient(t, second, "native-finalizer", nil), "merge", getNative(t, first, nativeOrdinary), []string{r.finalizer}))
	t.Run("same namespace name in another Project remains independent", func(t *testing.T) {
		project := &rm.Project{ObjectMeta: metav1.ObjectMeta{Name: "namespace-fixture-other-project"}, Spec: first.project.Spec}
		require.NoError(t, first.root.Create(first.ctx, project))
		cfg := rest.CopyConfig(second.projectConfig)
		cfg.Host = strings.Replace(cfg.Host, "/projects/"+first.project.Name+"/", "/projects/"+project.Name+"/", 1)
		cfg.BearerToken = first.tokens["customer"]
		c, err := client.New(cfg, client.Options{Scheme: first.scheme})
		require.NoError(t, err)
		ordinary := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-held-annotation"}}
		require.NoError(t, c.Create(first.ctx, ordinary))
		require.NotEqual(t, held[ordinary.Name].UID, ordinary.UID)
		require.NoError(t, c.Delete(first.ctx, ordinary, client.Preconditions{UID: &ordinary.UID, ResourceVersion: &ordinary.ResourceVersion}))
		ordinary = namespaceAt(t, first, c, ordinary.Name)
		require.NoError(t, finishNamespace(first, c, ordinary))
		require.True(t, apierrors.IsNotFound(c.Get(first.ctx, client.ObjectKeyFromObject(ordinary), &corev1.Namespace{})))
		require.Equal(t, held[ordinary.Name], namespaceAt(t, first, first.customer, ordinary.Name))
	})
	for _, location := range []string{"label", "annotation"} {
		project := &rm.Project{ObjectMeta: metav1.ObjectMeta{Name: "namespace-reservation-forgery"}, Spec: first.project.Spec}
		if location == "label" {
			project.Labels = map[string]string{key: ""}
		} else {
			project.Annotations = map[string]string{key: ""}
		}
		requireNativeDenied(t, first.root.Create(first.ctx, project))
	}
}

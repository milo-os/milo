package app

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	corev1 "k8s.io/api/core/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	regv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Apps #46 e1be5c515cbffb798a67bc2da8278e0d522dddc4; exact contract schema/vector.
//
//go:embed testdata/appsownership/appinstallgrants.apps.miloapis.com.yaml
var grantFixtureCRD []byte

//go:embed testdata/appsownership/appinstallgrant-v1-valid.json
var grantFixtureSpec []byte

func requireGrantDenied(t *testing.T, err error) {
	t.Helper()
	require.Truef(t, apierrors.IsForbidden(err), "expected compiled guard Forbidden, got %v", err)
	require.Contains(t, err.Error(), "Apps install grant and native registration are reserved")
}
func grantAt(t *testing.T, f *apiFixture) *unstructured.Unstructured {
	t.Helper()
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(grantFixtureResource.GroupVersion().WithKind("AppInstallGrant"))
	require.NoError(t, f.customer.Get(f.ctx, client.ObjectKey{Name: "held-grant"}, o))
	return o
}
func TestAppsGrantReservationAPIFixture(t *testing.T) {
	f := startAPIFixture(t, nil)
	require.NoError(t, regv1.AddToScheme(f.scheme))
	require.Equal(t, "33c9be9ec2f9ac76c1945776b53568c18159bb50743f48144cd28bbc6e039d6d", fmt.Sprintf("%x", sha256.Sum256(grantFixtureCRD)))
	require.Equal(t, "7fa22024487f856f7fb168572a3e8995819a0ef74422fdf61528d9c575d0a8d0", fmt.Sprintf("%x", sha256.Sum256(grantFixtureSpec)))
	crd := &extv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(grantFixtureCRD, crd))
	require.NoError(t, f.root.Create(f.ctx, crd))
	require.Eventually(t, func() bool {
		got := &extv1.CustomResourceDefinition{}
		if f.root.Get(f.ctx, client.ObjectKeyFromObject(crd), got) != nil {
			return false
		}
		for _, condition := range got.Status.Conditions {
			if condition.Type == extv1.Established && condition.Status == extv1.ConditionTrue {
				return true
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
	// Setup is native API-only and is permanently closed before assertions. Its
	// declared Project binding is updated in the pinned wire vector and rehashed;
	// no grant authority or installed request is claimed by this test fixture.
	spec := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(grantFixtureSpec, &spec))
	envelope := spec["envelope"].(map[string]interface{})
	envelope["projectUID"] = string(f.project.UID)
	envelope["templateRevision"].(map[string]interface{})["projectUID"] = string(f.project.UID)
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	spec["envelopeDigest"] = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	held := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": grantFixtureResource.GroupVersion().String(), "kind": "AppInstallGrant", "metadata": map[string]interface{}{"name": "held-grant", "labels": map[string]interface{}{"fixtures.apps.test/owned": "grant-reservation"}}, "spec": spec}}
	require.Eventually(t, func() bool { err = f.seed.Create(f.ctx, held); return err == nil }, 20*time.Second, 100*time.Millisecond, "native grant setup: %v", err)
	// Snapshot after normal CRD establishment, before sealing status controllers.
	require.NoError(t, f.root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
	f.seal()
	original := grantAt(t, f)
	t.Run("valid revoke requires compiled reservation", func(t *testing.T) {
		requireGrantDenied(t, f.customer.Patch(f.ctx, original.DeepCopy(), client.RawPatch(types.MergePatchType, []byte(`{"spec":{"revokeRequested":true}}`))))
	})
	for _, identity := range []string{"customer", apiFixtureSetupName, "system-admin"} {
		t.Run(identity, func(t *testing.T) {
			c := nativeWriterClient(t, f, identity, nil)
			create := held.DeepCopy()
			create.SetUID("")
			create.SetResourceVersion("")
			create.SetName("cannot-issue")
			if identity == apiFixtureSetupName {
				create.SetName("held-grant-reacquire")
			}
			requireGrantDenied(t, c.Create(f.ctx, create))
			updated := original.DeepCopy()
			updated.SetAnnotations(map[string]string{"customer": "edit"})
			requireGrantDenied(t, c.Update(f.ctx, updated))
			requireGrantDenied(t, c.Patch(f.ctx, original.DeepCopy(), client.RawPatch(types.MergePatchType, []byte(`{"spec":{"revokeRequested":true}}`))))
			requireGrantDenied(t, c.Patch(f.ctx, original.DeepCopy(), client.RawPatch(types.JSONPatchType, []byte(`[{"op":"add","path":"/metadata/annotations","value":{"claim":"forged"}}]`))))
			apply := original.DeepCopy()
			apply.SetManagedFields(nil)
			apply.SetResourceVersion("")
			apply.SetAnnotations(map[string]string{"ssa": "forged"})
			requireGrantDenied(t, c.Patch(f.ctx, apply, client.Apply, client.FieldOwner("grant-reservation-test"), client.ForceOwnership))
			requireGrantDenied(t, c.Delete(f.ctx, original.DeepCopy(), client.Preconditions{UID: ptrUID(original.GetUID())}))
		})
	}
	// Real self-impersonation, accepted by the fixture's AlwaysAllow authorizer,
	// cannot bypass a resource reservation with no trusted-principal exception.
	impersonated := nativeWriterClient(t, f, "customer", http.Header{"Impersonate-User": []string{"customer"}, "Impersonate-Uid": []string{"customer-uid"}})
	requireGrantDenied(t, impersonated.Patch(f.ctx, original.DeepCopy(), client.RawPatch(types.MergePatchType, []byte(`{"spec":{"revokeRequested":true}}`))))
	requireGrantDenied(t, f.customer.DeleteAllOf(f.ctx, original.DeepCopy(), client.MatchingLabels{"fixtures.apps.test/owned": "grant-reservation"}))
	require.Equal(t, original, grantAt(t, f), "every denied request must preserve exact UID/RV/spec")
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(grantFixtureResource.GroupVersion().WithKind("AppInstallGrantList"))
	require.NoError(t, f.customer.List(f.ctx, list))
	require.Len(t, list.Items, 1)
	t.Run("native registration custody", func(t *testing.T) {
		require.NoError(t, f.root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
		duplicate := crd.DeepCopy()
		duplicate.UID = ""
		duplicate.ResourceVersion = ""
		err := f.root.Create(f.ctx, duplicate)
		if apierrors.IsAlreadyExists(err) {
			t.Log("native existing-name lookup preceded admission; duplicate CREATE is not counted as guard proof")
		} else {
			requireGrantDenied(t, err)
		}
		changed := crd.DeepCopy()
		changed.Labels = map[string]string{"change": "metadata"}
		requireGrantDenied(t, f.root.Update(f.ctx, changed))
		requireGrantDenied(t, f.root.Status().Update(f.ctx, crd.DeepCopy()))
		requireGrantDenied(t, f.root.Delete(f.ctx, crd.DeepCopy(), client.Preconditions{UID: &crd.UID, ResourceVersion: &crd.ResourceVersion}))
		again := &extv1.CustomResourceDefinition{}
		require.NoError(t, f.root.Get(f.ctx, client.ObjectKeyFromObject(crd), again))
		require.Equal(t, crd, again)
		// The grant API stays served, not merely a failing discovery/404 route.
		require.Equal(t, original, grantAt(t, f))
		registration := &regv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: "v1alpha1." + appsownership.GrantGroup}, Spec: regv1.APIServiceSpec{Group: appsownership.GrantGroup, Version: "v1alpha1", GroupPriorityMinimum: 1000, VersionPriority: 15}}
		requireGrantDenied(t, f.root.Create(f.ctx, registration))
	})
	t.Run("unrelated APIs retain ordinary behavior", func(t *testing.T) {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: apiFixtureNamespace}, Data: map[string]string{"key": "value"}}
		require.NoError(t, f.customer.Create(f.ctx, cm))
		cm.Data["key"] = "updated"
		require.NoError(t, f.customer.Update(f.ctx, cm))
		require.NoError(t, f.customer.Delete(f.ctx, cm, client.Preconditions{UID: &cm.UID, ResourceVersion: &cm.ResourceVersion}))
		ordinary := &extv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "examples.fixture.apps.test"}, Spec: extv1.CustomResourceDefinitionSpec{Group: "fixture.apps.test", Names: extv1.CustomResourceDefinitionNames{Plural: "examples", Singular: "example", Kind: "Example"}, Scope: extv1.ClusterScoped, Versions: []extv1.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true, Schema: &extv1.CustomResourceValidation{OpenAPIV3Schema: &extv1.JSONSchemaProps{Type: "object"}}}}}}
		require.NoError(t, f.root.Create(f.ctx, ordinary))
		require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := f.root.Get(f.ctx, client.ObjectKeyFromObject(ordinary), ordinary); err != nil {
				return err
			}
			ordinary.Labels = map[string]string{"ordinary": "update"}
			return f.root.Update(f.ctx, ordinary)
		}))
		require.NoError(t, f.root.Delete(f.ctx, ordinary))
		service := &regv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: "v1.unrelated.apps.test"}, Spec: regv1.APIServiceSpec{Group: "unrelated.apps.test", Version: "v1", GroupPriorityMinimum: 1000, VersionPriority: 15}}
		require.NoError(t, f.root.Create(f.ctx, service))
		require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := f.root.Get(f.ctx, client.ObjectKeyFromObject(service), service); err != nil {
				return err
			}
			service.Labels = map[string]string{"ordinary": "update"}
			return f.root.Update(f.ctx, service)
		}))
		require.NoError(t, f.root.Delete(f.ctx, service, client.Preconditions{UID: &service.UID}))
	})
}

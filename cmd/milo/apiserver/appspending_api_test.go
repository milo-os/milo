package app

import (
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership/pendingwire"
	rm "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	admissionreg "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

// Exact Apps 71d7bf5ed3563bd44e8071867212a146fc6ecf14 experimental contracts.
//
//go:embed testdata/appsownership/apps.apps.miloapis.com.yaml
var pendingAPICRD []byte

//go:embed testdata/appsownership/apprevisions.apps.miloapis.com.yaml
var pendingHistoryAPICRD []byte

var pendingGVK = schema.GroupVersionKind{Group: "apps.miloapis.com", Version: "v1alpha1", Kind: "App"}

func pendingRequest(t *testing.T, f *apiFixture, name string) *unstructured.Unstructured {
	t.Helper()
	inputs, err := pendingwire.Parse([]byte(`{"integer":18446744073709551615,"decimal":0.123456789012345678901}`))
	require.NoError(t, err)
	spec := map[string]interface{}{"templateRef": map[string]interface{}{"projectRevision": map[string]interface{}{"projectUID": string(f.project.UID), "templateName": "source", "templateUID": "source-uid", "revisionName": "source-r1", "revisionUID": "source-r1-uid", "digest": "sha256:" + strings.Repeat("1", 64)}}, "inputsJSON": string(inputs.CanonicalJSON()), "inputsDigest": inputs.Digest(), "uninstallPolicy": "Delete"}
	digest, err := pendingwire.ValidateSpec(spec, string(f.project.UID))
	require.NoError(t, err)
	o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": pendingGVK.GroupVersion().String(), "kind": "App", "metadata": map[string]interface{}{"name": name}, "spec": spec}}
	o.SetAnnotations(map[string]string{appsownership.PendingProtocolAnnotation: appsownership.PendingProtocol, appsownership.PendingProjectAnnotation: string(f.project.UID), appsownership.PendingActorAnnotation: "customer-uid", appsownership.PendingDigestAnnotation: digest, appsownership.PendingGenerationAnnotation: "1"})
	o.SetFinalizers([]string{appsownership.PendingFinalizer})
	return o
}
func pendingDenied(t *testing.T, err error) {
	t.Helper()
	require.Truef(t, apierrors.IsForbidden(err), "expected compiled pending guard Forbidden, got %v", err)
	require.Contains(t, err.Error(), "Apps pending request custody denies")
}
func pendingGet(t *testing.T, c client.Client, f *apiFixture, name string) *unstructured.Unstructured {
	t.Helper()
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(pendingGVK)
	require.NoError(t, c.Get(f.ctx, client.ObjectKey{Name: name}, o))
	return o
}
func TestAppsPendingCustodyAPIFixture(t *testing.T) {
	f := startAPIFixtureWithArgs(t, &appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration", PendingRequests: appsownership.PendingRequestsV1}, grantRegistrationAuthorizer(t))
	f.seal()
	root := grantRootClient(t, f, "customer", nil)
	for _, raw := range [][]byte{pendingAPICRD, pendingHistoryAPICRD} {
		crd := &extv1.CustomResourceDefinition{}
		require.NoError(t, yaml.Unmarshal(raw, crd))
		bad := crd.DeepCopy()
		schema := bad.Spec.Versions[0].Schema.OpenAPIV3Schema
		if len(schema.XValidations) > 0 {
			schema.XValidations = nil
		} else {
			spec := schema.Properties["spec"]
			require.NotEmpty(t, spec.XValidations)
			spec.XValidations = nil
			schema.Properties["spec"] = spec
		}
		pendingDenied(t, root.Create(f.ctx, bad))
		pendingDenied(t, f.customer.Create(f.ctx, crd.DeepCopy()))
		denied := grantRootClient(t, f, "denied-registration", nil)
		err := denied.Create(f.ctx, crd.DeepCopy())
		require.True(t, apierrors.IsForbidden(err))
		require.Contains(t, err.Error(), "fixture registration authorizer denial")
		require.NoError(t, root.Create(f.ctx, crd))
		require.Eventually(t, func() bool {
			if root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd) != nil {
				return false
			}
			for _, c := range crd.Status.Conditions {
				if c.Type == extv1.Established && c.Status == extv1.ConditionTrue {
					return true
				}
			}
			return false
		}, 20*time.Second, 100*time.Millisecond, "ordinary native CRD establishment through compiled status predicate")
		pendingDenied(t, root.Delete(f.ctx, crd.DeepCopy()))
		changed := crd.DeepCopy()
		changed.Annotations["unexpected"] = "annotation"
		pendingDenied(t, root.Update(f.ctx, changed))
	}
	for _, cfg := range []*rest.Config{f.projectConfig, func() *rest.Config {
		c := rest.CopyConfig(f.projectConfig)
		c.Host = strings.SplitN(c.Host, "/apis/resourcemanager.miloapis.com/", 2)[0]
		return c
	}()} {
		d, err := discovery.NewDiscoveryClientForConfig(cfg)
		require.NoError(t, err)
		resources, err := d.ServerResourcesForGroupVersion(pendingGVK.GroupVersion().String())
		require.NoError(t, err)
		found := false
		for _, r := range resources.APIResources {
			if r.Name == "apps" {
				found = true
			}
		}
		require.True(t, found)
		h, err := rest.HTTPClientFor(cfg)
		require.NoError(t, err)
		mapper, err := apiutil.NewDynamicRESTMapper(cfg, h)
		require.NoError(t, err)
		mapping, err := mapper.RESTMapping(pendingGVK.GroupKind(), pendingGVK.Version)
		require.NoError(t, err)
		require.Equal(t, "apps", mapping.Resource.Resource)
	}
	// A real installed dynamic hook denies a valid create. Remove it through the
	// native API; all following custody assertions must still reach compiled code.
	// This hook is not the Apps source-eligibility validator and makes no source claim.
	require.NoError(t, admissionreg.AddToScheme(f.scheme))
	webhook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var review admissionv1.AdmissionReview
		_ = json.NewDecoder(r.Body).Decode(&review)
		review.Response = &admissionv1.AdmissionResponse{UID: review.Request.UID, Allowed: false, Result: &metav1.Status{Message: "fixture dynamic hook is present"}}
		review.Request = nil
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(review)
	}))
	defer webhook.Close()
	url := webhook.URL
	fail := admissionreg.Fail
	none := admissionreg.SideEffectClassNone
	scope := admissionreg.ClusterScope
	hooks := &admissionreg.ValidatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "pending-custody-fixture"}, Webhooks: []admissionreg.ValidatingWebhook{{Name: "pending.fixture.apps.test", ClientConfig: admissionreg.WebhookClientConfig{URL: &url, CABundle: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: webhook.Certificate().Raw})}, AdmissionReviewVersions: []string{"v1"}, FailurePolicy: &fail, SideEffects: &none, Rules: []admissionreg.RuleWithOperations{{Operations: []admissionreg.OperationType{admissionreg.Create}, Rule: admissionreg.Rule{APIGroups: []string{pendingGVK.Group}, APIVersions: []string{pendingGVK.Version}, Resources: []string{"apps"}, Scope: &scope}}}}}}
	require.NoError(t, root.Create(f.ctx, hooks))
	require.Eventually(t, func() bool {
		err := f.customer.Create(f.ctx, pendingRequest(t, f, "hook-probe"), client.DryRunAll)
		return err != nil && strings.Contains(err.Error(), "fixture dynamic hook is present")
	}, 10*time.Second, 100*time.Millisecond)
	require.NoError(t, root.Delete(f.ctx, hooks, client.Preconditions{UID: &hooks.UID, ResourceVersion: &hooks.ResourceVersion}))
	require.Eventually(t, func() bool {
		return f.customer.Create(f.ctx, pendingRequest(t, f, "hook-probe"), client.DryRunAll) == nil
	}, 10*time.Second, 100*time.Millisecond)
	t.Run("unhooked create boundary", func(t *testing.T) {
		for _, key := range []string{appsownership.ProjectUIDLabel, appsownership.AppUIDLabel, appsownership.NamespaceReservationKey} {
			for _, label := range []bool{false, true} {
				o := pendingRequest(t, f, "reserved-marker")
				if label {
					o.SetLabels(map[string]string{key: ""})
				} else {
					a := o.GetAnnotations()
					a[key] = ""
					o.SetAnnotations(a)
				}
				pendingDenied(t, f.customer.Create(f.ctx, o))
			}
		}
		for _, identity := range []string{"customer", "system-admin", apiFixtureSetupName} {
			c := nativeWriterClient(t, f, identity, nil)
			o := pendingRequest(t, f, "forged")
			a := o.GetAnnotations()
			a[appsownership.PendingActorAnnotation] = "forged-uid"
			o.SetAnnotations(a)
			pendingDenied(t, c.Create(f.ctx, o))
		}
		o := pendingRequest(t, f, "no-hold")
		o.SetFinalizers(nil)
		pendingDenied(t, f.customer.Create(f.ctx, o))
		o = pendingRequest(t, f, "new-protocol")
		a := o.GetAnnotations()
		a[appsownership.PendingProtocolAnnotation] = "install-request-v1"
		o.SetAnnotations(a)
		pendingDenied(t, f.customer.Create(f.ctx, o))
		pendingDenied(t, root.Create(f.ctx, pendingRequest(t, f, "root")))
		impersonated := nativeWriterClient(t, f, "customer", http.Header{"Impersonate-User": []string{"customer"}, "Impersonate-Uid": []string{"customer-uid"}})
		pendingDenied(t, impersonated.Create(f.ctx, pendingRequest(t, f, "impersonation")))
		pendingDenied(t, f.customer.Patch(f.ctx, pendingRequest(t, f, "ssa-create"), client.Apply, client.FieldOwner("custody"), client.ForceOwnership))
		require.NoError(t, f.customer.Create(f.ctx, pendingRequest(t, f, "dry-run"), client.DryRunAll))
		empty := pendingRequest(t, f, "dry-run")
		require.True(t, apierrors.IsNotFound(f.customer.Get(f.ctx, client.ObjectKeyFromObject(empty), empty)))
	})
	o := pendingRequest(t, f, "protected")
	require.NoError(t, f.customer.Create(f.ctx, o))
	original := pendingGet(t, f.customer, f, o.GetName())
	t.Run("removed hook cannot mutate pending identity", func(t *testing.T) {
		for _, patch := range []string{`{"metadata":{"finalizers":[]}}`, `{"metadata":{"annotations":{"apps.miloapis.com/request-protocol":"install-request-v1"}}}`, `{"metadata":{"annotations":{"apps.miloapis.com/request-actor-uid":"forged"}}}`, `{"metadata":{"ownerReferences":[{"apiVersion":"v1","kind":"Namespace","name":"foreign","uid":"foreign"}]}}`, `{"spec":{"uninstallPolicy":"Retain"}}`} {
			pendingDenied(t, f.customer.Patch(f.ctx, original.DeepCopy(), client.RawPatch(types.MergePatchType, []byte(patch))))
		}
		pendingDenied(t, f.customer.Patch(f.ctx, original.DeepCopy(), client.RawPatch(types.JSONPatchType, []byte(`[{"op":"remove","path":"/metadata/annotations/apps.miloapis.com~1request-spec-digest"}]`))))
		edit := original.DeepCopy()
		edit.Object["spec"].(map[string]interface{})["uninstallPolicy"] = "Retain"
		pendingDenied(t, f.customer.Update(f.ctx, edit))
		edit.SetManagedFields(nil)
		pendingDenied(t, f.customer.Patch(f.ctx, edit, client.Apply, client.FieldOwner("custody"), client.ForceOwnership))
		require.Equal(t, original, pendingGet(t, f.customer, f, o.GetName()))
	})
	status := original.DeepCopy()
	status.Object["status"] = map[string]interface{}{"projectUID": string(f.project.UID), "admittedGeneration": int64(1), "phase": "Pending"}
	require.NoError(t, f.customer.Status().Update(f.ctx, status))
	original = pendingGet(t, f.customer, f, o.GetName())
	t.Run("accepted status and history denied", func(t *testing.T) {
		bad := original.DeepCopy()
		bad.Object["status"].(map[string]interface{})["phase"] = "Accepted"
		pendingDenied(t, f.customer.Status().Update(f.ctx, bad))
		require.Equal(t, original, pendingGet(t, f.customer, f, o.GetName()))
		history := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": pendingGVK.GroupVersion().String(), "kind": "AppRevision", "metadata": map[string]interface{}{"name": "forged-history"}, "spec": map[string]interface{}{"projectUID": string(f.project.UID), "appName": o.GetName(), "appUID": string(o.GetUID()), "admittedGeneration": int64(1), "sequence": int64(1), "effectiveActorUID": "customer-uid", "acceptedAt": time.Now().UTC().Format(time.RFC3339), "source": "Create", "desiredSpec": o.Object["spec"], "specDigest": o.GetAnnotations()[appsownership.PendingDigestAnnotation]}}}
		pendingDenied(t, f.customer.Create(f.ctx, history))
	})
	t.Run("DELETE requires exact UID and RV", func(t *testing.T) {
		uid, rv := original.GetUID(), original.GetResourceVersion()
		wrongUID, staleRV := uid+"-replacement", o.GetResourceVersion()
		require.NotEqual(t, rv, staleRV, "status write must advance the resource version")
		for _, tc := range []struct {
			name          string
			preconditions *metav1.Preconditions
		}{
			{"missing both", nil},
			{"missing UID", &metav1.Preconditions{ResourceVersion: &rv}},
			{"missing RV", &metav1.Preconditions{UID: &uid}},
			{"wrong UID", &metav1.Preconditions{UID: &wrongUID, ResourceVersion: &rv}},
			{"stale RV", &metav1.Preconditions{UID: &uid, ResourceVersion: &staleRV}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				err := f.customer.Delete(f.ctx, original.DeepCopy(), &client.DeleteOptions{Preconditions: tc.preconditions})
				if tc.name == "wrong UID" || tc.name == "stale RV" {
					require.True(t, apierrors.IsConflict(err) || apierrors.IsForbidden(err), "%v", err)
				} else {
					pendingDenied(t, err)
				}
				require.Equal(t, original, pendingGet(t, f.customer, f, o.GetName()))
			})
		}
		pendingDenied(t, f.customer.DeleteAllOf(f.ctx, pendingRequest(t, f, "")))
		require.Equal(t, original, pendingGet(t, f.customer, f, o.GetName()))
	})
	t.Run("API cancellation and fresh worker recovery", func(t *testing.T) {
		require.NoError(t, f.customer.Delete(f.ctx, original.DeepCopy(), client.Preconditions{UID: ptrUID(original.GetUID()), ResourceVersion: func() *string { v := original.GetResourceVersion(); return &v }()}))
		held := pendingGet(t, f.customer, f, o.GetName())
		require.NotNil(t, held.GetDeletionTimestamp())
		require.Equal(t, original.GetUID(), held.GetUID())
		require.Contains(t, held.GetFinalizers(), appsownership.PendingFinalizer)
		// A fresh native client and a second Milo replica recover only persisted
		// state. This models a restarted cancellation worker, not an Apps process test.
		second := startAPIFixtureReplica(t, f)
		second.seal()
		fresh := nativeWriterClient(t, second, "customer", nil)
		held = pendingGet(t, fresh, second, o.GetName())
		changed := held.DeepCopy()
		changed.SetFinalizers(nil)
		changed.SetLabels(map[string]string{"mixed": "mutation"})
		pendingDenied(t, fresh.Update(f.ctx, changed))
		stale := held.DeepCopy()
		held.SetFinalizers(nil)
		require.NoError(t, fresh.Update(f.ctx, held))
		gone := pendingRequest(t, f, o.GetName())
		require.True(t, apierrors.IsNotFound(fresh.Get(f.ctx, client.ObjectKeyFromObject(gone), gone)))
		replacement := pendingRequest(t, f, o.GetName())
		require.NoError(t, f.customer.Create(f.ctx, replacement))
		require.NotEqual(t, stale.GetUID(), replacement.GetUID())
		stale.SetFinalizers(nil)
		err := fresh.Update(f.ctx, stale)
		require.True(t, apierrors.IsConflict(err) || apierrors.IsForbidden(err))
		staleUID, staleRV := stale.GetUID(), stale.GetResourceVersion()
		err = fresh.Delete(f.ctx, stale.DeepCopy(), client.Preconditions{UID: &staleUID, ResourceVersion: &staleRV})
		require.True(t, apierrors.IsConflict(err) || apierrors.IsForbidden(err), "%v", err)
		require.Equal(t, replacement, pendingGet(t, f.customer, f, o.GetName()))
	})
	t.Run("ordinary native APIs unchanged", func(t *testing.T) {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "ordinary"}, Data: map[string]string{"key": "value"}}
		require.NoError(t, f.customer.Create(f.ctx, cm))
		require.NoError(t, f.customer.Delete(f.ctx, cm))
	})

	t.Run("Project termination cancellation then UID replacement", func(t *testing.T) {
		cancellable := pendingRequest(t, f, "project-termination")
		require.NoError(t, f.customer.Create(f.ctx, cancellable))
		old := f.project.DeepCopy()
		require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(old), old))
		old.Finalizers = []string{"fixture.apps.test/project-hold"}
		require.NoError(t, root.Update(f.ctx, old))
		require.NoError(t, root.Delete(f.ctx, old, client.Preconditions{UID: &old.UID, ResourceVersion: &old.ResourceVersion}))
		require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(old), old))
		require.NotNil(t, old.DeletionTimestamp)
		pendingDenied(t, f.customer.Create(f.ctx, pendingRequest(t, f, "during-project-deletion")))
		require.NoError(t, f.customer.Delete(f.ctx, cancellable, client.Preconditions{UID: ptrUID(cancellable.GetUID()), ResourceVersion: func() *string { v := cancellable.GetResourceVersion(); return &v }()}))
		held := pendingGet(t, f.customer, f, cancellable.GetName())
		held.SetFinalizers(nil)
		require.NoError(t, f.customer.Update(f.ctx, held))
		require.True(t, apierrors.IsNotFound(f.customer.Get(f.ctx, client.ObjectKeyFromObject(held), held)))
		old.Finalizers = nil
		require.NoError(t, root.Update(f.ctx, old))
		require.True(t, apierrors.IsNotFound(root.Get(f.ctx, client.ObjectKeyFromObject(old), &rm.Project{})))
		replacement := &rm.Project{ObjectMeta: metav1.ObjectMeta{Name: old.Name}, Spec: old.Spec}
		require.NoError(t, root.Create(f.ctx, replacement))
		require.NotEqual(t, old.UID, replacement.UID)
		pendingDenied(t, f.customer.Create(f.ctx, pendingRequest(t, f, "stale-project")))
		current := pendingGet(t, f.customer, f, "protected")
		pendingDenied(t, f.customer.Delete(f.ctx, current, client.Preconditions{UID: ptrUID(current.GetUID()), ResourceVersion: func() *string { v := current.GetResourceVersion(); return &v }()}))
	})
}

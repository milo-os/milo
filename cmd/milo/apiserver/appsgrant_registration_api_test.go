package app

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	authv1 "k8s.io/api/authorization/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	regv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

func grantRegistrationAuthorizer(t *testing.T) func([]string) []string {
	t.Helper()
	// Real native webhook authorizer, not an admission fake. It deliberately
	// allows all except one subject; this is chain evidence, not platform IAM.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sar authv1.SubjectAccessReview
		if json.NewDecoder(r.Body).Decode(&sar) != nil {
			http.Error(w, "invalid review", 400)
			return
		}
		sar.Status = authv1.SubjectAccessReviewStatus{Allowed: true}
		if a := sar.Spec.ResourceAttributes; sar.Spec.User == "denied-registration" && a != nil && a.Group == "apiextensions.k8s.io" && a.Resource == "customresourcedefinitions" && (a.Verb == "create" || a.Verb == "update" || a.Verb == "patch") {
			sar.Status = authv1.SubjectAccessReviewStatus{Denied: true, Reason: "fixture registration authorizer denial"}
		}
		sar.TypeMeta = metav1.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SubjectAccessReview"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sar)
	}))
	t.Cleanup(server.Close)
	cfg := clientcmdapi.Config{Clusters: map[string]*clientcmdapi.Cluster{"authorizer": {Server: server.URL, CertificateAuthorityData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"authorizer": {}}, Contexts: map[string]*clientcmdapi.Context{"authorizer": {Cluster: "authorizer", AuthInfo: "authorizer"}}, CurrentContext: "authorizer"}
	raw, err := clientcmd.Write(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "authorizer.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	return func(args []string) []string {
		for i, a := range args {
			if strings.HasPrefix(a, "--authorization-mode=") {
				args[i] = "--authorization-mode=Webhook"
			}
		}
		return append(args, "--authorization-webhook-config-file="+path, "--authorization-webhook-version=v1")
	}
}
func grantRootClient(t *testing.T, f *apiFixture, identity string, headers http.Header) client.Client {
	t.Helper()
	cfg := rest.CopyConfig(f.projectConfig)
	cfg.Host = strings.SplitN(cfg.Host, "/apis/resourcemanager.miloapis.com/", 2)[0]
	cfg.BearerToken = f.tokens[identity]
	require.NotEmpty(t, cfg.BearerToken)
	if headers != nil {
		cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper { return fixtureHeaders{rt, headers} }
	}
	c, err := client.New(cfg, client.Options{Scheme: f.scheme})
	require.NoError(t, err)
	return c
}
func registrationManifest(t *testing.T) *extv1.CustomResourceDefinition {
	t.Helper()
	o := &extv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(grantFixtureCRD, o))
	return o
}
func TestAppsGrantRegistrationAPIFixture(t *testing.T) {
	f := startAPIFixtureWithArgs(t, &appsownership.Configuration{APIVersion: "apiserver.miloapis.com/v1alpha1", Kind: "AppsOwnershipConfiguration", GrantRegistration: appsownership.GrantRegistrationV1}, grantRegistrationAuthorizer(t))
	// No grant CRD or grant was seeded. Seal before the first registration call:
	// production predicates must permit both CREATE and native establishment.
	f.seal()
	root := grantRootClient(t, f, "customer", nil)
	denied := grantRootClient(t, f, "denied-registration", nil)
	t.Run("native authorizer still denies exact create", func(t *testing.T) {
		err := denied.Create(f.ctx, registrationManifest(t))
		require.True(t, apierrors.IsForbidden(err))
		require.Contains(t, err.Error(), "fixture registration authorizer denial")
		require.NotContains(t, err.Error(), "Apps install grant")
	})
	t.Run("root scope and identity", func(t *testing.T) {
		requireGrantDenied(t, f.customer.Create(f.ctx, registrationManifest(t)))
		for _, path := range []string{"/apis/resourcemanager.miloapis.com/v1alpha1/organizations/scope-test/control-plane", "/apis/iam.miloapis.com/v1alpha1/users/scope-test/control-plane"} {
			cfg := rest.CopyConfig(f.projectConfig)
			cfg.Host = strings.SplitN(cfg.Host, "/apis/resourcemanager.miloapis.com/", 2)[0] + path
			cfg.BearerToken = f.tokens["customer"]
			scoped, err := client.New(cfg, client.Options{Scheme: f.scheme})
			require.NoError(t, err)
			requireGrantDenied(t, scoped.Create(f.ctx, registrationManifest(t)))
		}

		noUID := grantRootClient(t, f, "no-uid-registration", nil)
		requireGrantDenied(t, noUID.Create(f.ctx, registrationManifest(t)))
		impersonated := grantRootClient(t, f, "customer", http.Header{"Impersonate-User": []string{"customer"}, "Impersonate-Uid": []string{"customer-uid"}})
		requireGrantDenied(t, impersonated.Create(f.ctx, registrationManifest(t)))
	})
	t.Run("weakening schema cannot bootstrap", func(t *testing.T) {
		o := registrationManifest(t)
		spec := o.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
		e := spec.Properties["envelope"]
		require.NotEmpty(t, e.XValidations)
		e.XValidations = nil
		spec.Properties["envelope"] = e
		o.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = spec
		requireGrantDenied(t, root.Create(f.ctx, o))
	})
	for _, tc := range []struct {
		name   string
		change func(*extv1.CustomResourceDefinition)
	}{
		{"metadata", func(o *extv1.CustomResourceDefinition) { o.Annotations["extra.example.test/annotation"] = "unexpected" }},
		{"labels", func(o *extv1.CustomResourceDefinition) { o.Labels = map[string]string{"unexpected": "label"} }},
		{"finalizer", func(o *extv1.CustomResourceDefinition) { o.Finalizers = []string{"fixtures.apps.test/hold"} }},
		{"ownerref", func(o *extv1.CustomResourceDefinition) {
			o.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Namespace", Name: "default", UID: "foreign"}}
		}},
		{"served version", func(o *extv1.CustomResourceDefinition) {
			v := o.Spec.Versions[0]
			v.Name = "v2"
			v.Storage = false
			o.Spec.Versions = append(o.Spec.Versions, v)
		}},
		{"webhook conversion", func(o *extv1.CustomResourceDefinition) {
			url := "https://conversion.invalid/convert"
			o.Spec.Conversion = &extv1.CustomResourceConversion{Strategy: extv1.WebhookConverter, Webhook: &extv1.WebhookConversion{ClientConfig: &extv1.WebhookClientConfig{URL: &url}, ConversionReviewVersions: []string{"v1"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := registrationManifest(t)
			tc.change(o)
			requireGrantDenied(t, root.Create(f.ctx, o))
		})
	}
	t.Run("SSA create cannot use patch authorization", func(t *testing.T) {
		requireGrantDenied(t, root.Patch(f.ctx, registrationManifest(t), client.Apply, client.FieldOwner("registration-test"), client.ForceOwnership))
	})
	t.Run("dry run has no registration", func(t *testing.T) {
		require.NoError(t, root.Create(f.ctx, registrationManifest(t), client.DryRunAll))
		require.True(t, apierrors.IsNotFound(root.Get(f.ctx, client.ObjectKey{Name: appsownership.GrantCRDName}, &extv1.CustomResourceDefinition{})))
	})
	// Two ordinary native clients racing CREATE get exactly one persisted object.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, c := range []client.Client{root, f.root} {
		wg.Add(1)
		go func(c client.Client) { defer wg.Done(); results <- c.Create(f.ctx, registrationManifest(t)) }(c)
	}
	wg.Wait()
	close(results)
	success, exists := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if apierrors.IsAlreadyExists(err) {
			exists++
		} else {
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, exists)
	crd := &extv1.CustomResourceDefinition{}
	require.Eventually(t, func() bool {
		if root.Get(f.ctx, client.ObjectKey{Name: appsownership.GrantCRDName}, crd) != nil {
			return false
		}
		for _, c := range crd.Status.Conditions {
			if c.Type == extv1.Established && c.Status == extv1.ConditionTrue {
				return true
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond, "production status guard must allow native controllers to establish")
	// Read-only identity verification suitable for recovery, without an update.
	// This slice does not inject a lost response or restart the server process.
	recovered := &extv1.CustomResourceDefinition{}
	require.NoError(t, f.root.Get(f.ctx, client.ObjectKeyFromObject(crd), recovered))
	require.Equal(t, crd.UID, recovered.UID)
	require.Equal(t, crd.Spec, recovered.Spec)
	t.Run("native authorizer still denies status", func(t *testing.T) {
		err := denied.Status().Update(f.ctx, crd.DeepCopy())
		require.True(t, apierrors.IsForbidden(err))
		require.Contains(t, err.Error(), "fixture registration authorizer denial")
	})
	t.Run("safe status and immutable registration", func(t *testing.T) {
		require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
		require.NoError(t, root.Status().Update(f.ctx, crd.DeepCopy()))
		for _, mode := range []string{"put", "merge", "json", "ssa", "delete"} {
			require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
			before := crd.DeepCopy()
			var err error
			switch mode {
			case "put":
				err = root.Update(f.ctx, crd.DeepCopy())
			case "merge":
				err = root.Patch(f.ctx, crd.DeepCopy(), client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"labels":{"change":"forged"}}}`)))
			case "json":
				err = root.Patch(f.ctx, crd.DeepCopy(), client.RawPatch(types.JSONPatchType, []byte(`[{"op":"add","path":"/metadata/annotations/change","value":"forged"}]`)))
			case "ssa":
				o := registrationManifest(t)
				err = root.Patch(f.ctx, o, client.Apply, client.FieldOwner("registration-test"), client.ForceOwnership)
			case "delete":
				err = root.Delete(f.ctx, crd.DeepCopy(), client.Preconditions{UID: &crd.UID, ResourceVersion: &crd.ResourceVersion})
			}
			requireGrantDenied(t, err)
			require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
			require.Equal(t, before, crd)
		}
		for _, change := range []func(*extv1.CustomResourceDefinition){func(o *extv1.CustomResourceDefinition) { o.Status.StoredVersions = []string{"v1alpha1", "v2"} }, func(o *extv1.CustomResourceDefinition) { o.Status.AcceptedNames.Kind = "WrongKind" }, func(o *extv1.CustomResourceDefinition) {
			o.Status.Conditions = append(o.Status.Conditions, extv1.CustomResourceDefinitionCondition{Type: extv1.Terminating, Status: extv1.ConditionTrue, LastTransitionTime: metav1.Now()})
		}, func(o *extv1.CustomResourceDefinition) { o.Labels = map[string]string{"status": "forgery"} }, func(o *extv1.CustomResourceDefinition) { o.Finalizers = []string{"fixtures.apps.test/hold"} }} {
			require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
			before := crd.DeepCopy()
			next := crd.DeepCopy()
			change(next)
			err := root.Status().Update(f.ctx, next)
			if apierrors.IsInvalid(err) {
				t.Log("native status schema rejected this shape before compiled admission; not counted as guard proof")
			} else {
				requireGrantDenied(t, err)
			}
			require.NoError(t, root.Get(f.ctx, client.ObjectKeyFromObject(crd), crd))
			require.Equal(t, before, crd)
		}
	})
	t.Run("root and project native discovery without APIService", func(t *testing.T) {
		require.NoError(t, regv1.AddToScheme(f.scheme))
		require.True(t, apierrors.IsNotFound(f.root.Get(f.ctx, client.ObjectKey{Name: "v1alpha1.apps.miloapis.com"}, &regv1.APIService{})))
		for _, scope := range []string{"root", "project"} {
			t.Run(scope, func(t *testing.T) {
				cfg := rest.CopyConfig(f.projectConfig)
				cfg.BearerToken = f.tokens["customer"]
				if scope == "root" {
					cfg.Host = strings.SplitN(cfg.Host, "/apis/resourcemanager.miloapis.com/", 2)[0]
				}
				dc, err := discovery.NewDiscoveryClientForConfig(cfg)
				require.NoError(t, err)
				require.Eventually(t, func() bool {
					groups, err := dc.ServerGroups()
					if err != nil {
						return false
					}
					for _, g := range groups.Groups {
						if g.Name == "apps.miloapis.com" {
							for _, v := range g.Versions {
								if v.GroupVersion == "apps.miloapis.com/v1alpha1" {
									return true
								}
							}
						}
					}
					return false
				}, 10*time.Second, 100*time.Millisecond, "/apis must actually advertise the native group/version")
				resources, err := dc.ServerResourcesForGroupVersion("apps.miloapis.com/v1alpha1")
				require.NoError(t, err)
				found := false
				for _, r := range resources.APIResources {
					if r.Name == "appinstallgrants" && r.Kind == "AppInstallGrant" && !r.Namespaced {
						found = true
					}
				}
				require.True(t, found)
				httpClient, err := rest.HTTPClientFor(cfg)
				require.NoError(t, err)
				mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
				require.NoError(t, err)
				mapping, err := mapper.RESTMapping(schema.GroupKind{Group: "apps.miloapis.com", Kind: "AppInstallGrant"}, "v1alpha1")
				require.NoError(t, err)
				require.Equal(t, grantFixtureResource, mapping.Resource)
				if scope == "root" {
					req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, cfg.Host+"/readyz", nil)
					require.NoError(t, err)
					resp, err := httpClient.Do(req)
					require.NoError(t, err)
					_ = resp.Body.Close()
					require.Equal(t, http.StatusOK, resp.StatusCode)
				}
			})
		}
	})
	t.Run("no grant issuance exception", func(t *testing.T) {
		spec := map[string]interface{}{}
		require.NoError(t, json.Unmarshal(grantFixtureSpec, &spec))
		o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "apps.miloapis.com/v1alpha1", "kind": "AppInstallGrant", "metadata": map[string]interface{}{"name": "no-issuance"}, "spec": spec}}
		var err error
		require.Eventually(t, func() bool { err = f.customer.Create(f.ctx, o); return apierrors.IsForbidden(err) }, 10*time.Second, 100*time.Millisecond)
		requireGrantDenied(t, err)
	})
}

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/appsownership"
	rm "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/admission"
	apiservercompat "k8s.io/apiserver/pkg/util/compatibility"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/rest"
	basecompatibility "k8s.io/component-base/compatibility"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// Byte-for-byte Compute 88a7101c2b91f7195c2bb5433c50f54c384a90ce CRD.
//
//go:embed testdata/appsownership/workloads.compute.datumapis.com.yaml
var apiFixtureWorkloadCRD []byte

const (
	apiFixtureChildEnv  = "MILO_APPS_API_FIXTURE_CHILD"
	apiFixtureArgsEnv   = "MILO_APPS_API_FIXTURE_ARGS"
	apiFixtureSealEnv   = "MILO_APPS_API_FIXTURE_SEALED"
	apiFixtureNamespace = "api-guard-fixture"
	apiFixtureSetupName = "api-fixture-setup"
	apiFixtureSetupUID  = "api-fixture-setup-uid"
)

// setupGate exists only in this test binary. Its only transition is open->sealed.
// The parent completes all fixture HTTP requests before sealing over stdin;
// there is no API endpoint, request header or production configuration bypass.
type setupGate struct {
	mu     sync.RWMutex
	sealed bool
}

func (g *setupGate) seal() { g.mu.Lock(); defer g.mu.Unlock(); g.sealed = true }
func (g *setupGate) permits(a admission.Attributes) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.sealed || a.GetUserInfo() == nil || a.GetUserInfo().GetName() != apiFixtureSetupName || a.GetUserInfo().GetUID() != apiFixtureSetupUID || a.GetNamespace() != apiFixtureNamespace || a.GetSubresource() != "" {
		return false
	}
	if a.GetResource() == corev1.SchemeGroupVersion.WithResource("configmaps") {
		switch a.GetName() {
		case "held-configmap", "terminating-configmap", "acquire-after-seal", "reacquire":
			return true
		}
	}
	return a.GetResource() == schema.GroupVersionResource{Group: "compute.datumapis.com", Version: "v1alpha", Resource: "workloads"} && a.GetName() == "held-workload"
}

type setupValidator struct {
	admission.Interface
	validator admission.ValidationInterface
	gate      *setupGate
}

func (v *setupValidator) Validate(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces) error {
	if v.gate.permits(a) {
		return nil
	}
	return v.validator.Validate(ctx, a, o)
}

// Child setup uses ordinary Milo options and its supported admission decorator
// interface. Only AppsOwnership is wrapped, and all assertions run after seal.
func TestAppsOwnershipAPIFixtureChild(t *testing.T) {
	if os.Getenv(apiFixtureChildEnv) != "1" {
		return
	}
	var args []string
	require.NoError(t, json.Unmarshal([]byte(os.Getenv(apiFixtureArgsEnv)), &args))
	_ = NewCommand() // initialize ordinary process-wide flag defaults
	opts := NewOptions()
	opts.GenericServerRunOptions.ComponentGlobalsRegistry = basecompatibility.NewComponentGlobalsRegistry()
	opts.GenericServerRunOptions.ComponentGlobalsRegistry.ComponentGlobalsOrRegister(basecompatibility.DefaultKubeComponent, apiservercompat.DefaultBuildEffectiveVersion(), utilfeature.DefaultMutableFeatureGate)
	fs := pflag.NewFlagSet("api-fixture", pflag.ContinueOnError)
	opts.GenericServerRunOptions.AddUniversalFlags(fs)
	opts.Etcd.AddFlags(fs)
	opts.SecureServing.AddFlags(fs)
	opts.Authentication.AddFlags(fs)
	opts.Authorization.AddFlags(fs)
	fs.StringVar(&opts.ServiceAccountSigningKeyFile, "service-account-signing-key-file", opts.ServiceAccountSigningKeyFile, "test signer path")
	require.NoError(t, fs.Parse(args))
	opts.SystemNamespaces = []string{metav1.NamespaceSystem, metav1.NamespaceDefault, SystemNamespace}
	gate := &setupGate{}
	opts.Admission.GenericAdmission.Decorators = append(opts.Admission.GenericAdmission.Decorators, admission.DecoratorFunc(func(handler admission.Interface, name string) admission.Interface {
		if name != appsownership.PluginName {
			return handler
		}
		validator, ok := handler.(admission.ValidationInterface)
		require.True(t, ok)
		return &setupValidator{Interface: handler, validator: validator, gate: gate}
	}))
	go func() {
		var signal [1]byte
		if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != 'S' {
			return
		}
		gate.seal()
		// A local acknowledgement is published only after the one-way latch closes.
		if err := os.WriteFile(os.Getenv(apiFixtureSealEnv), []byte("sealed"), 0600); err != nil {
			panic(err)
		}
	}()
	completed, err := opts.Complete(context.Background(), []string{}, []net.IP{})
	require.NoError(t, err)
	require.Empty(t, completed.Validate())
	require.NoError(t, Run(context.Background(), completed))
}

type fixtureLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *fixtureLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *fixtureLog) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func TestAppsOwnershipAPIFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("starts isolated Milo HTTP server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	require.NotEmpty(t, assets)
	// Only Milo accesses this backend. Fixtures have no KV client or storage key.
	backend := &envtest.Etcd{Path: filepath.Join(assets, "etcd"), StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second}
	require.NoError(t, backend.Start())
	t.Cleanup(func() { require.NoError(t, backend.Stop()) })
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "tokens.csv")
	file, err := os.OpenFile(tokenPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	writer := csv.NewWriter(file)
	tokens := map[string]string{}
	for _, identity := range []string{apiFixtureSetupName, "customer"} {
		var raw [32]byte
		_, err := rand.Read(raw[:])
		require.NoError(t, err)
		tokens[identity] = hex.EncodeToString(raw[:])
		uid := identity + "-uid"
		if identity == apiFixtureSetupName {
			uid = apiFixtureSetupUID
		}
		require.NoError(t, writer.Write([]string{tokens[identity], identity, uid, "system:authenticated"}))
	}
	writer.Flush()
	require.NoError(t, writer.Error())
	require.NoError(t, file.Close())
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "service-account.key")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	args, err := json.Marshal([]string{"--etcd-servers=" + backend.URL.String(), "--bind-address=127.0.0.1", fmt.Sprintf("--secure-port=%d", port), "--cert-dir=" + filepath.Join(dir, "certs"), "--token-auth-file=" + tokenPath, "--anonymous-auth=false", "--authorization-mode=AlwaysAllow", "--service-account-issuer=https://api-fixture.test", "--api-audiences=api-fixture-test", "--service-account-signing-key-file=" + keyPath, "--service-account-key-file=" + keyPath})
	require.NoError(t, err)
	serverCtx, stop := context.WithCancel(ctx)
	command := exec.CommandContext(serverCtx, os.Args[0], "-test.run=^TestAppsOwnershipAPIFixtureChild$")
	ackPath := filepath.Join(dir, "sealed")
	command.Env = append(os.Environ(), apiFixtureChildEnv+"=1", apiFixtureArgsEnv+"="+string(args), apiFixtureSealEnv+"="+ackPath)
	var logs fixtureLog
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Milo child diagnostics: %s", logs.String())
		}
	})
	command.Stdout = &logs
	command.Stderr = &logs
	pipe, err := command.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { stop(); _ = pipe.Close(); <-done })
	root := fmt.Sprintf("https://127.0.0.1:%d", port)
	cfg := &rest.Config{Host: root, BearerToken: tokens[apiFixtureSetupName], TLSClientConfig: rest.TLSClientConfig{Insecure: true}, Timeout: 5 * time.Second}
	httpClient, err := rest.HTTPClientFor(cfg)
	require.NoError(t, err)
	t.Cleanup(httpClient.CloseIdleConnections)
	deadline := time.Now().Add(60 * time.Second)
	for {
		select {
		case err := <-done:
			done <- err
			t.Fatalf("Milo child exited: %v; %s", err, logs.String())
		default:
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, root+"/healthz", nil)
		response, err := httpClient.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Milo did not start: %s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, extv1.AddToScheme(scheme))
	require.NoError(t, rm.AddToScheme(scheme))
	rootClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.Equal(t, "ba38aeadb5d6c7a97c9b05a031d9955099820f5e7edd206a43fe3dd7f031b889", fmt.Sprintf("%x", sha256.Sum256(apiFixtureWorkloadCRD)))
	crd := &extv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(apiFixtureWorkloadCRD, crd))
	require.NoError(t, rootClient.Create(ctx, crd))
	require.Eventually(t, func() bool {
		got := &extv1.CustomResourceDefinition{}
		if rootClient.Get(ctx, client.ObjectKey{Name: crd.Name}, got) != nil {
			return false
		}
		for _, c := range got.Status.Conditions {
			if c.Type == extv1.Established && c.Status == extv1.ConditionTrue {
				return true
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
	project := &rm.Project{ObjectMeta: metav1.ObjectMeta{Name: "api-fixture-project"}, Spec: rm.ProjectSpec{OwnerRef: rm.OwnerReference{Kind: "Organization", Name: "test"}}}
	require.NoError(t, rootClient.Create(ctx, project))
	projectConfig := rest.CopyConfig(cfg)
	projectConfig.Host = root + "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project.Name + "/control-plane"
	seed, err := client.New(projectConfig, client.Options{Scheme: scheme})
	require.NoError(t, err)
	customerConfig := rest.CopyConfig(projectConfig)
	customerConfig.BearerToken = tokens["customer"]
	customer, err := client.New(customerConfig, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: apiFixtureNamespace}}
	require.NoError(t, seed.Create(ctx, ns))
	marks := map[string]string{appsownership.ProjectUIDLabel: string(project.UID), appsownership.AppUIDLabel: "fixture-app-uid"}
	held := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "held-configmap", Labels: marks}, Data: map[string]string{"value": "original"}}
	require.NoError(t, seed.Create(ctx, held))
	workload := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "compute.datumapis.com/v1alpha", "kind": "Workload",
		"metadata": map[string]any{"namespace": apiFixtureNamespace, "name": "held-workload"},
		"spec":     map[string]any{"placements": []any{map[string]any{"name": "default", "locations": []any{map[string]any{"name": "dfw"}}, "scaleSettings": map[string]any{"minReplicas": int64(1), "instanceManagementPolicy": "OrderedReady"}}}, "template": map[string]any{"spec": map[string]any{"runtime": map[string]any{"resources": map[string]any{"instanceType": "datumcloud-d1-standard-2"}}, "networkInterfaces": []any{map[string]any{"network": map[string]any{"name": "probe-network"}}}}}},
	}}
	workload.SetLabels(marks)
	var createErr error
	require.Eventually(t, func() bool {
		createErr = seed.Create(ctx, workload)
		return createErr == nil || !strings.Contains(createErr.Error(), "no matches for kind")
	}, 20*time.Second, 100*time.Millisecond)
	require.NoError(t, createErr)
	terminating := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "terminating-configmap", Labels: marks, Finalizers: []string{"fixtures.apps.test/hold"}}}
	require.NoError(t, seed.Create(ctx, terminating))
	require.NoError(t, seed.Delete(ctx, terminating, client.Preconditions{UID: &terminating.UID, ResourceVersion: &terminating.ResourceVersion}))
	require.NoError(t, seed.Get(ctx, client.ObjectKeyFromObject(terminating), terminating))
	require.False(t, terminating.DeletionTimestamp.IsZero())
	acquire := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "acquire-after-seal"}}
	require.NoError(t, seed.Create(ctx, acquire))
	// All setup requests have completed before closing the one-way process latch.
	_, err = pipe.Write([]byte{'S'})
	require.NoError(t, err)
	require.Eventually(t, func() bool { data, err := os.ReadFile(ackPath); return err == nil && string(data) == "sealed" }, 5*time.Second, 20*time.Millisecond)
	deny := func(t *testing.T, err error) {
		t.Helper()
		require.Truef(t, apierrors.IsForbidden(err), "expected guard denial: %v", err)
		require.Contains(t, err.Error(), "Apps ownership")
	}
	for _, identity := range []struct {
		name string
		c    client.Client
	}{{"customer", customer}, {"setup credentials", seed}} {
		t.Run(identity.name+" cannot reacquire", func(t *testing.T) {
			fresh := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "reacquire", Labels: marks}}
			deny(t, identity.c.Create(ctx, fresh))
			original := acquire.DeepCopy()
			attempt := acquire.DeepCopy()
			attempt.Labels = marks
			deny(t, identity.c.Update(ctx, attempt))
			require.NoError(t, seed.Get(ctx, client.ObjectKeyFromObject(acquire), acquire))
			require.Equal(t, original, acquire)
			require.True(t, apierrors.IsNotFound(seed.Get(ctx, client.ObjectKeyFromObject(fresh), &corev1.ConfigMap{})))
		})
	}
	for _, object := range []client.Object{held, workload, terminating} {
		t.Run(object.GetName()+" unchanged after guarded writes", func(t *testing.T) {
			original := object.DeepCopyObject().(client.Object)
			for _, identity := range []client.Client{customer, seed} {
				update := original.DeepCopyObject().(client.Object)
				update.SetAnnotations(map[string]string{"ordinary": "mutation"})
				deny(t, identity.Update(ctx, update))
				update = original.DeepCopyObject().(client.Object)
				deny(t, identity.Patch(ctx, update, client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"labels":null,"finalizers":[]}}`))))
				update = original.DeepCopyObject().(client.Object)
				update.SetManagedFields(nil)
				if _, ok := update.(*corev1.ConfigMap); ok {
					update.GetObjectKind().SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))
				}
				encoded, err := json.Marshal(update)
				require.NoError(t, err)
				deny(t, identity.Patch(ctx, update, client.RawPatch(types.ApplyPatchType, encoded), client.FieldOwner("guard-fixture"), client.ForceOwnership))
				deny(t, identity.Delete(ctx, original, client.Preconditions{UID: ptrUID(original.GetUID())}))
				after := original.DeepCopyObject().(client.Object)
				require.NoError(t, seed.Get(ctx, client.ObjectKeyFromObject(original), after))
				require.Equal(t, original, after)
			}
		})
	}
	t.Run("unmarked UID and RV preconditions remain native", func(t *testing.T) {
		object := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: "ordinary"}, Data: map[string]string{"v": "one"}}
		require.NoError(t, customer.Create(ctx, object))
		old := object.DeepCopy()
		object.Data["v"] = "two"
		require.NoError(t, customer.Update(ctx, object))
		require.True(t, apierrors.IsConflict(customer.Update(ctx, old.DeepCopy())))
		require.True(t, apierrors.IsConflict(customer.Delete(ctx, old, client.Preconditions{UID: &old.UID, ResourceVersion: &old.ResourceVersion})))
		require.NoError(t, customer.Delete(ctx, object, client.Preconditions{UID: &object.UID, ResourceVersion: &object.ResourceVersion}))
		replacement := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: apiFixtureNamespace, Name: object.Name}}
		require.NoError(t, customer.Create(ctx, replacement))
		require.NotEqual(t, old.UID, replacement.UID)
		require.True(t, apierrors.IsConflict(customer.Delete(ctx, old, client.Preconditions{UID: &old.UID})))
		got := &corev1.ConfigMap{}
		require.NoError(t, customer.Get(ctx, client.ObjectKeyFromObject(replacement), got))
		require.Equal(t, replacement, got)
	})
}
func ptrUID(uid types.UID) *types.UID { return &uid }

package billing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const cacheSyncTimeout = 3 * time.Second

// TestPaymentTermsApplicationEmail_Manager runs the controller in a manager
// against an API server, with and without billing installed. Milo's e2e
// environment has no billing, and a controller watching an unserved API
// stops the whole manager, webhooks included.
func TestPaymentTermsApplicationEmail_Manager(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}

	t.Run("without billing the manager starts and sends nothing", func(t *testing.T) {
		cfg := startEmailEnv(t, nil)
		startEmailManager(t, cfg)
	})

	t.Run("with billing a new application is confirmed to support", func(t *testing.T) {
		cfg := startEmailEnv(t, []*apiextensionsv1.CustomResourceDefinition{
			billingCRD("BillingArrangementApplication", "billingarrangementapplications"),
			billingCRD("BillingAccount", "billingaccounts"),
		})
		c := startEmailManager(t, cfg)
		ctx := context.Background()

		require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-acme"}}))
		application := &billingv1alpha1.BillingArrangementApplication{
			ObjectMeta: metav1.ObjectMeta{Name: "app-1", Namespace: "organization-acme"},
			Spec: billingv1alpha1.BillingArrangementApplicationSpec{
				BillingAccountRef: billingv1alpha1.BillingAccountRef{Name: "ba-1"},
				PaymentOption:     billingv1alpha1.ApplicationPaymentOptionNotSure,
				Situation:         "We pay suppliers by bank transfer.",
			},
		}
		require.NoError(t, c.Create(ctx, application))

		name := types.NamespacedName{
			Namespace: "milo-system",
			Name:      "payment-terms-application-" + string(application.UID) + "-submitted",
		}
		require.Eventually(t, func() bool {
			return c.Get(ctx, name, &notificationv1alpha1.Email{}) == nil
		}, 30*time.Second, 200*time.Millisecond, "the support notice was never created")
	})
}

func startEmailEnv(t *testing.T, crds []*apiextensionsv1.CustomResourceDefinition) *rest.Config {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases", "iam"),
			filepath.Join(root, "config", "crd", "bases", "notification"),
			filepath.Join(root, "config", "crd", "bases", "resourcemanager"),
		},
		CRDs:                  crds,
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })
	return cfg
}

// startEmailManager runs the controller in a manager until the test ends,
// failing the test if the manager stops on its own.
func startEmailManager(t *testing.T, cfg *rest.Config) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, billingv1alpha1.AddToScheme(scheme))
	require.NoError(t, iamv1alpha1.AddToScheme(scheme))
	require.NoError(t, notificationv1alpha1.AddToScheme(scheme))
	require.NoError(t, resourcemanagerv1alpha1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		// A controller watching an unserved API stops the manager once its
		// cache sync times out, two minutes by default.
		Controller: ctrlconfig.Controller{CacheSyncTimeout: cacheSyncTimeout},
	})
	require.NoError(t, err)
	require.NoError(t, (&PaymentTermsApplicationEmailController{
		Client:                 mgr.GetClient(),
		EmailNamespace:         "milo-system",
		ReceivedEmailTemplate:  "received",
		SubmittedEmailTemplate: "submitted",
		ApprovedEmailTemplate:  "approved",
		DeclinedEmailTemplate:  "declined",
		SupportEmailAddress:    "support@example.com",
		PortalURL:              "https://cloud.example.com",
		StaffPortalURL:         "https://staff.example.com",
		MaxEventAge:            time.Hour,
	}).SetupWithManager(mgr))

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.NoError(t, c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "milo-system"}}))

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	var startErr error
	go func() {
		defer close(stopped)
		startErr = mgr.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	require.True(t, mgr.GetCache().WaitForCacheSync(ctx), "the manager's caches never synced")
	select {
	case <-stopped:
		t.Fatalf("the manager stopped: %v", startErr)
	case <-time.After(2 * cacheSyncTimeout):
	}
	return c
}

// billingCRD stands in for one of billing's CRDs: the controller only needs
// the API served, not billing's validation.
func billingCRD(kind, plural string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + billingv1alpha1.GroupVersion.Group},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: billingv1alpha1.GroupVersion.Group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: kind, ListKind: kind + "List", Plural: plural},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    billingv1alpha1.GroupVersion.Version,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type:                   "object",
					XPreserveUnknownFields: ptr.To(true),
				}},
				Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}},
			}},
		},
	}
}

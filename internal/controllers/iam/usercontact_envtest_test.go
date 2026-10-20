package iam

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	notificationwebhook "go.miloapis.com/milo/internal/webhooks/notification/v1alpha1"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// The Contact webhooks Milo serves, read from the generated manifest so the test admits exactly what production does.
var contactWebhookNames = map[string]bool{
	"mcontact.notification.miloapis.com": true,
	"vcontact.notification.miloapis.com": true,
}

// holdFinalizer stands in for the finalizers every production User carries, so a deleted User is first marked
// deleting, as the user-contact-cleanup controller expects.
const holdFinalizer = "test.miloapis.com/hold"

type contactEnv struct {
	ctx    context.Context
	client client.Client // the manager's cached client, as the controller reads
	server client.Reader // the API server itself, for assertions
}

// TestUserContactWithWebhook runs the user-contact controllers in a manager against an API server with Milo's User
// and Contact CRDs and the Contact webhooks, where a Contact's subject cannot be repointed once set. Every step goes
// through the running controllers, as in production: one worker per User, events from the API server.
func TestUserContactWithWebhook(t *testing.T) {
	env := startContactEnv(t)

	t.Run("a Contact naming nobody is claimed", func(t *testing.T) {
		contact := env.newsletterContact(t, "claim@example.com")
		user := env.user(t, "claim-user", "claim@example.com")
		env.waitSubject(t, contact, user.Name)
		env.waitReason(t, user, UserContactSyncedReason)
	})

	t.Run("the same User again changes nothing", func(t *testing.T) {
		user := env.user(t, "same-user", "same@example.com")
		env.waitReason(t, user, UserContactSyncedReason)
		before := env.contactOf(t, user).Generation
		done := reconciles(t, "success")
		env.touch(t, user)
		env.eventually(t, func() bool { return reconciles(t, "success") > done }, "the touched User is reconciled")
		require.Equal(t, before, env.contactOf(t, user).Generation, "no spec write")
	})

	t.Run("an email held by another live User is reported, not retried", func(t *testing.T) {
		holder := env.user(t, "held-holder", "held@example.com")
		contact := env.contactOf(t, holder)
		other := env.user(t, "held-other", "held@example.com")
		env.waitReason(t, other, UserContactEmailHeldReason)
		failed := reconciles(t, "error")
		time.Sleep(3 * time.Second) // [s] long enough for a failing reconcile's first retries, 5 ms apart and doubling
		require.Equal(t, failed, reconciles(t, "error"), "a condition only another object can change is not retried")
		require.Equal(t, holder.Name, env.subjectOf(t, contact))
	})

	t.Run("the holder's deletion frees its claimed Contact for the other User", func(t *testing.T) {
		contact := env.newsletterContact(t, "freed@example.com")
		holder := env.user(t, "freed-holder", "freed@example.com")
		env.waitSubject(t, contact, holder.Name)
		other := env.user(t, "freed-other", "freed@example.com")
		env.waitReason(t, other, UserContactEmailHeldReason)
		env.deleteHeld(t, holder)
		env.waitSubject(t, contact, other.Name)
		env.waitReason(t, other, UserContactSyncedReason)
		env.release(t, holder)
	})

	t.Run("the holder's own Contact, deleted with it, is replaced for the other User", func(t *testing.T) {
		holder := env.user(t, "owned-holder", "owned@example.com")
		contact := env.contactOf(t, holder)
		other := env.user(t, "owned-other", "owned@example.com")
		env.waitReason(t, other, UserContactEmailHeldReason)
		require.NoError(t, env.client.Delete(env.ctx, holder))
		// envtest runs no garbage collector; the Contact names the holder as its owner, so production's deletes it.
		require.NoError(t, env.client.Delete(env.ctx, contact))
		env.waitReason(t, other, UserContactSyncedReason)
		require.Equal(t, other.Name, env.subjectOf(t, env.contactOf(t, other)))
	})
}

func startContactEnv(t *testing.T) *contactEnv {
	t.Helper()
	if os.Getenv("CONTACT_ENV_LOG") != "" {
		ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stderr)))
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS must be set in CI; this test needs an envtest API server")
		}
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	root := filepath.Join("..", "..", "..")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases", "iam"),
			filepath.Join(root, "config", "crd", "bases", "notification"),
		},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: contactWebhooks(t, filepath.Join(root, "config", "webhook", "manifests.yaml")),
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, iamv1alpha1.AddToScheme(scheme))
	require.NoError(t, notificationv1alpha1.AddToScheme(scheme))
	opts := testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		WebhookServer: webhook.NewServer(webhook.Options{
			Host: opts.LocalServingHost, Port: opts.LocalServingPort, CertDir: opts.LocalServingCertDir,
		}),
		Controller: ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	require.NoError(t, notificationwebhook.SetupContactWebhooksWithManager(mgr))
	controller := &UserContactController{Client: mgr.GetClient(), ContactNamespace: testContactNamespace}
	require.NoError(t, controller.SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = mgr.Start(ctx) }()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))
	waitForWebhookServer(t, opts.LocalServingHost, opts.LocalServingPort)

	env := &contactEnv{ctx: ctx, client: mgr.GetClient(), server: mgr.GetAPIReader()}
	require.NoError(t, env.client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testContactNamespace}}))
	return env
}

// contactWebhooks returns the Contact webhooks from Milo's generated manifest, and no other.
func contactWebhooks(t *testing.T, manifest string) envtest.WebhookInstallOptions {
	t.Helper()
	data, err := os.ReadFile(manifest)
	require.NoError(t, err)
	var opts envtest.WebhookInstallOptions
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		raw := runtime.RawExtension{}
		err := decoder.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		opts = appendContactWebhooks(t, opts, raw.Raw)
	}
	require.Len(t, opts.MutatingWebhooks, 1)
	require.Len(t, opts.ValidatingWebhooks, 1)
	return opts
}

// appendContactWebhooks keeps the Contact webhooks of one manifest document.
func appendContactWebhooks(t *testing.T, opts envtest.WebhookInstallOptions, raw []byte) envtest.WebhookInstallOptions {
	t.Helper()
	var kind struct {
		Kind string `json:"kind"`
	}
	require.NoError(t, utilyaml.Unmarshal(raw, &kind))
	switch kind.Kind {
	case "MutatingWebhookConfiguration":
		cfg := &admissionregistrationv1.MutatingWebhookConfiguration{}
		require.NoError(t, utilyaml.Unmarshal(raw, cfg))
		cfg.Webhooks = filterMutating(cfg.Webhooks)
		opts.MutatingWebhooks = append(opts.MutatingWebhooks, cfg)
	case "ValidatingWebhookConfiguration":
		cfg := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		require.NoError(t, utilyaml.Unmarshal(raw, cfg))
		cfg.Webhooks = filterValidating(cfg.Webhooks)
		opts.ValidatingWebhooks = append(opts.ValidatingWebhooks, cfg)
	}
	return opts
}

func filterMutating(in []admissionregistrationv1.MutatingWebhook) []admissionregistrationv1.MutatingWebhook {
	var out []admissionregistrationv1.MutatingWebhook
	for _, w := range in {
		if contactWebhookNames[w.Name] {
			out = append(out, w)
		}
	}
	return out
}

func filterValidating(in []admissionregistrationv1.ValidatingWebhook) []admissionregistrationv1.ValidatingWebhook {
	var out []admissionregistrationv1.ValidatingWebhook
	for _, w := range in {
		if contactWebhookNames[w.Name] {
			out = append(out, w)
		}
	}
	return out
}

func waitForWebhookServer(t *testing.T, host string, port int) {
	t.Helper()
	address := net.JoinHostPort(host, strconv.Itoa(port))
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 30*time.Second, 100*time.Millisecond, "the webhook server never listened")
}

func (e *contactEnv) user(t *testing.T, name, email string) *iamv1alpha1.User {
	t.Helper()
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       iamv1alpha1.UserSpec{Email: email, GivenName: "Test", FamilyName: "User"},
	}
	require.NoError(t, e.client.Create(e.ctx, user))
	e.cached(t, user)
	return user
}

// newsletterContact creates a Contact that names nobody, as a newsletter sign-up does.
func (e *contactEnv) newsletterContact(t *testing.T, email string) *notificationv1alpha1.Contact {
	t.Helper()
	contact := &notificationv1alpha1.Contact{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "newsletter-", Namespace: testContactNamespace},
		Spec:       notificationv1alpha1.ContactSpec{Email: email},
	}
	require.NoError(t, e.client.Create(e.ctx, contact))
	e.cached(t, contact)
	return contact
}

// cached waits until the manager's cache holds obj, so a direct Reconcile reads it. Its version is not compared: the
// running controller may write the object's status first.
func (e *contactEnv) cached(t *testing.T, obj client.Object) {
	t.Helper()
	key := client.ObjectKeyFromObject(obj)
	got := obj.DeepCopyObject().(client.Object)
	e.eventually(t, func() bool { return e.client.Get(e.ctx, key, got) == nil }, "the cache never saw "+key.String())
}

func (e *contactEnv) contactOf(t *testing.T, user *iamv1alpha1.User) *notificationv1alpha1.Contact {
	t.Helper()
	contacts := &notificationv1alpha1.ContactList{}
	e.eventually(t, func() bool {
		return e.client.List(e.ctx, contacts, client.MatchingFields{contactSubjectNameIndexKey: user.Name}) == nil &&
			len(contacts.Items) == 1
	}, "no single Contact names "+user.Name)
	got := &notificationv1alpha1.Contact{}
	require.NoError(t, e.server.Get(e.ctx, client.ObjectKeyFromObject(&contacts.Items[0]), got))
	return got
}

// subjectOf reads a Contact's subject from the API server, not the cache: the assertions follow the server.
func (e *contactEnv) subjectOf(t *testing.T, contact *notificationv1alpha1.Contact) string {
	t.Helper()
	got := &notificationv1alpha1.Contact{}
	require.NoError(t, e.server.Get(e.ctx, client.ObjectKeyFromObject(contact), got))
	if got.Spec.SubjectRef == nil {
		return ""
	}
	return got.Spec.SubjectRef.Name
}

func (e *contactEnv) syncedReason(t *testing.T, user *iamv1alpha1.User) string {
	t.Helper()
	got := &iamv1alpha1.User{}
	require.NoError(t, e.server.Get(e.ctx, client.ObjectKeyFromObject(user), got))
	if c := meta.FindStatusCondition(got.Status.Conditions, UserContactSyncedCondition); c != nil {
		return c.Reason
	}
	return ""
}

// deleteHeld deletes a User that carries the stand-in finalizer, so it stays deleting until release.
func (e *contactEnv) deleteHeld(t *testing.T, user *iamv1alpha1.User) {
	t.Helper()
	e.patchUser(t, user, func(u *iamv1alpha1.User) { controllerutil.AddFinalizer(u, holdFinalizer) })
	require.NoError(t, e.client.Delete(e.ctx, user))
}

func (e *contactEnv) release(t *testing.T, user *iamv1alpha1.User) {
	t.Helper()
	e.patchUser(t, user, func(u *iamv1alpha1.User) { controllerutil.RemoveFinalizer(u, holdFinalizer) })
}

func (e *contactEnv) patchUser(t *testing.T, user *iamv1alpha1.User, change func(*iamv1alpha1.User)) {
	t.Helper()
	got := &iamv1alpha1.User{}
	require.NoError(t, e.client.Get(e.ctx, client.ObjectKeyFromObject(user), got))
	before := got.DeepCopy()
	change(got)
	err := e.client.Patch(e.ctx, got, client.MergeFrom(before))
	if !apierrors.IsNotFound(err) {
		require.NoError(t, err)
	}
}

func (e *contactEnv) eventually(t *testing.T, condition func() bool, what string) {
	t.Helper()
	require.Eventually(t, condition, 30*time.Second, 200*time.Millisecond, what)
}

func (e *contactEnv) waitSubject(t *testing.T, contact *notificationv1alpha1.Contact, want string) {
	t.Helper()
	e.eventually(t, func() bool { return e.subjectOf(t, contact) == want }, "the Contact never named "+want)
}

func (e *contactEnv) waitReason(t *testing.T, user *iamv1alpha1.User, want string) {
	t.Helper()
	e.eventually(t, func() bool { return e.syncedReason(t, user) == want }, user.Name+" never reported "+want)
}

// touch changes a label on the User, which the user-contact controller sees as an update.
func (e *contactEnv) touch(t *testing.T, user *iamv1alpha1.User) {
	t.Helper()
	e.patchUser(t, user, func(u *iamv1alpha1.User) {
		if u.Labels == nil {
			u.Labels = map[string]string{}
		}
		u.Labels["test.miloapis.com/touched"] = strconv.FormatInt(time.Now().UnixNano(), 10)
	})
}

// reconciles reads controller-runtime's own count of the user-contact controller's reconciles with one result: the
// series that shows the retry loop of milo#827.
func reconciles(t *testing.T, result string) float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "controller_runtime_reconcile_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			if hasLabels(m.GetLabel(), "controller", "user-contact", "result", result) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func hasLabels[L interface {
	GetName() string
	GetValue() string
}](labels []L, pairs ...string) bool {
	want := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		want[pairs[i]] = pairs[i+1]
	}
	for _, l := range labels {
		if v, ok := want[l.GetName()]; ok && v == l.GetValue() {
			delete(want, l.GetName())
		}
	}
	return len(want) == 0
}

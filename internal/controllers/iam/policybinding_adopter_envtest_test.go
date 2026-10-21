package iam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"go.miloapis.com/milo/internal/controllers/garbagecollector/gctest"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	adopterNamespace = "organization-acme"
	adopterOther     = "organization-other"
	// testHold keeps a deleted object in storage, deleting, until the test releases it.
	testHold = "test.miloapis.com/hold"
	// settle is how long a binding must keep its state to count as kept: longer than the garbage collector takes to
	// delete a dependent whose owner is absent, which is milliseconds once its graph holds the dependent.
	settle = 5 * time.Second
)

type adopterEnv struct {
	ctx    context.Context
	client client.Client // writes and reads through the API server, as a user would
}

// TestPolicyBindingAdopter runs the adopter in a manager against an API server with Milo's iam and resourcemanager
// CRDs, with Milo's own garbage collector deleting dependents. Each case is one row of the deletion-path table in the
// design note for milo#845.
func TestPolicyBindingAdopter(t *testing.T) {
	env := startAdopterEnv(t, nil)

	t.Run("D1 a binding on a Project is adopted and goes with its Project", func(t *testing.T) {
		project := env.project(t, "d1")
		pb := env.binding(t, "d1", env.refTo(project), nil)
		env.waitOwnedBy(t, pb, project.UID)
		require.Nil(t, pb.OwnerReferences[0].Controller, "the target is an owner, not the binding's controller")
		require.Nil(t, pb.OwnerReferences[0].BlockOwnerDeletion, "the binding never holds its target's deletion")
		require.NoError(t, env.client.Delete(env.ctx, project))
		env.waitGone(t, pb)
	})

	t.Run("D9 a binding on a namespaced object of a core kind in its namespace is adopted and goes with it", func(t *testing.T) {
		cm := env.configMap(t, adopterNamespace, "d9")
		pb := env.binding(t, "d9", iamv1alpha1.ResourceReference{
			Kind: "ConfigMap", Name: cm.Name, UID: string(cm.UID), Namespace: adopterNamespace,
		}, nil)
		env.waitOwnedBy(t, pb, cm.UID)
		require.NoError(t, env.client.Delete(env.ctx, cm))
		env.waitGone(t, pb)
	})

	t.Run("D8 a namespaced target in another namespace is never an owner", func(t *testing.T) {
		cm := env.configMap(t, adopterOther, "d8")
		pb := env.binding(t, "d8", iamv1alpha1.ResourceReference{
			Kind: "ConfigMap", Name: cm.Name, UID: string(cm.UID), Namespace: adopterOther,
		}, nil)
		env.keepsOwners(t, pb, nil)
	})

	t.Run("D19 a status-only update does not reconcile: one Warning for a cross-namespace binding, none per status write", func(t *testing.T) {
		cm := env.configMap(t, adopterOther, "d19")
		pb := env.binding(t, "d19", iamv1alpha1.ResourceReference{
			Kind: "ConfigMap", Name: cm.Name, UID: string(cm.UID), Namespace: adopterOther,
		}, nil)
		env.eventually(t, func() bool { return env.warnings(t, pb) == 1 }, "one Warning for the create")
		for i := range 3 {
			env.writeStatus(t, pb, fmt.Sprintf("status write %d", i))
		}
		time.Sleep(settle)
		require.Equal(t, int32(1), env.warnings(t, pb), "no Warning for a status-only update")
	})

	t.Run("D4 a binding owned by another object is never adopted and goes with that object", func(t *testing.T) {
		project := env.project(t, "d4")
		holder := env.configMap(t, adopterNamespace, "d4-holder")
		owner := metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: holder.Name, UID: holder.UID}
		pb := env.binding(t, "d4", env.refTo(project), []metav1.OwnerReference{owner})
		env.keepsOwners(t, pb, []types.UID{holder.UID})
		require.NoError(t, env.client.Delete(env.ctx, holder))
		env.waitGone(t, pb) // the Project lives on: a second owner would have kept a removed member's grant
	})

	t.Run("D5 a binding naming another uid of a live target is never adopted", func(t *testing.T) {
		project := env.project(t, "d5")
		ref := env.refTo(project)
		ref.UID = string(uuid.NewUUID())
		pb := env.binding(t, "d5", ref, nil)
		env.keepsOwners(t, pb, nil)
	})

	t.Run("D3 a binding naming a target never stored is never adopted and stays", func(t *testing.T) {
		pb := env.binding(t, "d3", iamv1alpha1.ResourceReference{
			APIGroup: resourcemanagerv1alpha1.GroupVersion.Group, Kind: "Project", Name: "d3-never", UID: string(uuid.NewUUID()),
		}, nil)
		env.keepsOwners(t, pb, nil)
	})

	t.Run("D2 a binding on a target being deleted is never adopted, and stays once the target is gone", func(t *testing.T) {
		project := env.project(t, "d2", testHold)
		require.NoError(t, env.client.Delete(env.ctx, project))
		pb := env.binding(t, "d2", env.refTo(project), nil)
		env.keepsOwners(t, pb, nil)
		env.release(t, project)
		env.keepsOwners(t, pb, nil)
	})

	t.Run("D6 an orphaning delete of the target is not undone", func(t *testing.T) {
		project := env.project(t, "d6", testHold)
		pb := env.binding(t, "d6", env.refTo(project), nil)
		env.waitOwnedBy(t, pb, project.UID)
		require.NoError(t, env.client.Delete(env.ctx, project, client.PropagationPolicy(metav1.DeletePropagationOrphan)))
		env.waitOwners(t, pb, nil) // the garbage collector orphans it; the hold keeps the Project deleting
		env.keepsOwners(t, pb, nil)
		env.release(t, project)
		env.keepsOwners(t, pb, nil)
	})

	t.Run("D7 a foreground delete of the target deletes the binding and does not wait on it", func(t *testing.T) {
		project := env.project(t, "d7")
		pb := env.binding(t, "d7", env.refTo(project), nil)
		env.waitOwnedBy(t, pb, project.UID)
		require.NoError(t, env.client.Delete(env.ctx, project, client.PropagationPolicy(metav1.DeletePropagationForeground)))
		env.waitGone(t, pb)
		env.waitGone(t, project)
	})

	t.Run("D11 an owner reference a person removes is added back", func(t *testing.T) {
		project := env.project(t, "d11")
		pb := env.binding(t, "d11", env.refTo(project), nil)
		env.waitOwnedBy(t, pb, project.UID)
		require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := env.client.Get(env.ctx, client.ObjectKeyFromObject(pb), pb); err != nil {
				return err
			}
			pb.OwnerReferences = nil
			return env.client.Update(env.ctx, pb)
		}))
		env.waitOwnedBy(t, pb, project.UID)
	})

	t.Run("D13 a subject edited while the binding is adopted keeps both changes", func(t *testing.T) {
		project := env.project(t, "d13")
		pb := env.binding(t, "d13", env.refTo(project), nil)
		require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := env.client.Get(env.ctx, client.ObjectKeyFromObject(pb), pb); err != nil {
				return err
			}
			pb.Spec.Subjects = append(pb.Spec.Subjects, iamv1alpha1.Subject{Kind: "User", Name: "d13-second", UID: string(uuid.NewUUID())})
			return env.client.Update(env.ctx, pb)
		}))
		env.waitOwnedBy(t, pb, project.UID)
		require.Len(t, pb.Spec.Subjects, 2, "the subject edit survives the adoption")
	})

	t.Run("D21 a User's two self-manage bindings have one owner each and go with the User through its UserPreference", func(t *testing.T) {
		user := &iamv1alpha1.User{ObjectMeta: metav1.ObjectMeta{Name: "d21"}, Spec: iamv1alpha1.UserSpec{Email: "d21@example.com"}}
		require.NoError(t, env.client.Create(env.ctx, user))
		// The User controller makes the User the owner of its UserPreference; this test stands in for it.
		pref := &iamv1alpha1.UserPreference{
			ObjectMeta: metav1.ObjectMeta{Name: "userpreference-d21", OwnerReferences: []metav1.OwnerReference{
				{APIVersion: iamv1alpha1.SchemeGroupVersion.String(), Kind: "User", Name: user.Name, UID: user.UID},
			}},
			Spec: iamv1alpha1.UserPreferenceSpec{UserRef: iamv1alpha1.UserReference{Name: user.Name}},
		}
		require.NoError(t, env.client.Create(env.ctx, pref))
		selfManage := env.bindingIn(t, "milo-system", "user-self-manage-d21", iamv1alpha1.ResourceReference{
			APIGroup: iamv1alpha1.SchemeGroupVersion.Group, Kind: "User", Name: user.Name, UID: string(user.UID),
		})
		prefSelfManage := env.bindingIn(t, "milo-system", "userpreference-self-manage-d21", iamv1alpha1.ResourceReference{
			APIGroup: iamv1alpha1.SchemeGroupVersion.Group, Kind: "UserPreference", Name: pref.Name, UID: string(pref.UID),
		})
		env.waitOwnedBy(t, selfManage, user.UID)
		env.waitOwnedBy(t, prefSelfManage, pref.UID)
		require.NoError(t, env.client.Delete(env.ctx, user))
		env.waitGone(t, pref)
		env.waitGone(t, selfManage)
		env.waitGone(t, prefSelfManage)
	})

	t.Run("stress: a burst of 60 bindings, made serially and in parallel, are all adopted and none is deleted", func(t *testing.T) {
		const n = 30
		var wg sync.WaitGroup
		results := make(chan error, 2*n)
		race := func(name string) {
			defer wg.Done()
			results <- env.projectThenBinding(name)
		}
		for i := range n {
			wg.Add(1)
			race(fmt.Sprintf("burst-serial-%d", i))
		}
		for i := range n {
			wg.Add(1)
			go race(fmt.Sprintf("burst-parallel-%d", i))
		}
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		time.Sleep(settle)
		env.allOwnedByTheirProjects(t, "burst-")
	})
}

// TestPolicyBindingAdopterAdoptsExistingBindings is row D15: bindings made before the adopter runs are adopted by its
// first list, and the garbage collector deletes none of them.
func TestPolicyBindingAdopterAdoptsExistingBindings(t *testing.T) {
	const n = 50
	env := startAdopterEnv(t, func(env *adopterEnv) {
		for i := range n {
			project := env.project(t, fmt.Sprintf("d15-%d", i))
			env.binding(t, fmt.Sprintf("d15-%d", i), env.refTo(project), nil)
		}
	})
	env.eventually(t, func() bool { return env.countOwned(t, "d15-") == n }, "every existing binding is adopted")
	time.Sleep(settle)
	require.Equal(t, n, env.countOwned(t, "d15-"), "none deleted after adoption")
}

// startAdopterEnv starts an API server with Milo's iam and resourcemanager CRDs, Milo's garbage collector, and a
// manager running the adopter. preload, when set, runs after the collector starts and before the manager does.
func startAdopterEnv(t *testing.T, preload func(*adopterEnv)) *adopterEnv {
	t.Helper()
	if os.Getenv("ADOPTER_ENV_LOG") != "" {
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
			filepath.Join(root, "config", "crd", "bases", "resourcemanager"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, iamv1alpha1.AddToScheme(scheme))
	require.NoError(t, resourcemanagerv1alpha1.AddToScheme(scheme))
	direct, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, gctest.Start(ctx, cfg, gctest.Options{}))
	env := &adopterEnv{ctx: ctx, client: direct}
	for _, ns := range []string{adopterNamespace, adopterOther, "milo-system"} {
		require.NoError(t, direct.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	}
	require.NoError(t, direct.Create(ctx, &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"}, Spec: resourcemanagerv1alpha1.OrganizationSpec{Type: "Standard"},
	}))
	if preload != nil {
		preload(env)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	adopter := &PolicyBindingAdopter{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Mapper: mgr.GetRESTMapper(), Scheme: scheme}
	require.NoError(t, adopter.SetupWithManager(mgr))
	go func() { _ = mgr.Start(ctx) }()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))
	return env
}

func (e *adopterEnv) project(t *testing.T, name string, finalizers ...string) *resourcemanagerv1alpha1.Project {
	t.Helper()
	p := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name, Finalizers: finalizers},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: "acme"},
		},
	}
	require.NoError(t, e.client.Create(e.ctx, p))
	return p
}

func (e *adopterEnv) configMap(t *testing.T, namespace, name string) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	require.NoError(t, e.client.Create(e.ctx, cm))
	return cm
}

func (e *adopterEnv) refTo(p *resourcemanagerv1alpha1.Project) iamv1alpha1.ResourceReference {
	return iamv1alpha1.ResourceReference{
		APIGroup: resourcemanagerv1alpha1.GroupVersion.Group, Kind: "Project", Name: p.Name, UID: string(p.UID),
	}
}

func newBinding(name string, ref iamv1alpha1.ResourceReference, owners []metav1.OwnerReference) *iamv1alpha1.PolicyBinding {
	return &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: adopterNamespace, Name: name, OwnerReferences: owners},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef:          iamv1alpha1.RoleReference{Name: "editor", Namespace: "milo-system"},
			Subjects:         []iamv1alpha1.Subject{{Kind: "User", Name: name + "-user", UID: string(uuid.NewUUID())}},
			ResourceSelector: iamv1alpha1.ResourceSelector{ResourceRef: &ref},
		},
	}
}

// bindingIn creates an unowned binding in a namespace other than the tests' organization namespace.
func (e *adopterEnv) bindingIn(t *testing.T, namespace, name string, ref iamv1alpha1.ResourceReference) *iamv1alpha1.PolicyBinding {
	t.Helper()
	pb := newBinding(name, ref, nil)
	pb.Namespace = namespace
	require.NoError(t, e.client.Create(e.ctx, pb))
	return pb
}

func (e *adopterEnv) binding(t *testing.T, name string, ref iamv1alpha1.ResourceReference, owners []metav1.OwnerReference) *iamv1alpha1.PolicyBinding {
	t.Helper()
	pb := newBinding(name, ref, owners)
	require.NoError(t, e.client.Create(e.ctx, pb))
	return pb
}

// projectThenBinding writes a Project and, at once, a binding naming it. The webhook's order, the binding stored first,
// cannot be made here: only the API server knows a Project's uid before it is stored. That race is row D16, tested on
// the adopter's poll with a stubbed reader and on kind against the Project webhook.
func (e *adopterEnv) projectThenBinding(name string) error {
	p := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       resourcemanagerv1alpha1.ProjectSpec{OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: "acme"}},
	}
	if err := e.client.Create(e.ctx, p); err != nil {
		return err
	}
	return e.client.Create(e.ctx, newBinding(name, e.refTo(p), nil))
}

// writeStatus writes a condition to the binding's status, as the authorization provider does on its reconciles.
func (e *adopterEnv) writeStatus(t *testing.T, pb *iamv1alpha1.PolicyBinding, message string) {
	t.Helper()
	require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := e.client.Get(e.ctx, client.ObjectKeyFromObject(pb), pb); err != nil {
			return err
		}
		meta.SetStatusCondition(&pb.Status.Conditions, metav1.Condition{
			Type: "Ready", Status: metav1.ConditionFalse, Reason: "Test", Message: message,
		})
		return e.client.Status().Update(e.ctx, pb)
	}))
}

// warnings counts the adopter's OwnerNotAllowed Warnings on the binding; the recorder folds repeats into one Event's
// count.
func (e *adopterEnv) warnings(t *testing.T, pb *iamv1alpha1.PolicyBinding) int32 {
	t.Helper()
	var events corev1.EventList
	require.NoError(t, e.client.List(e.ctx, &events, client.InNamespace(pb.Namespace)))
	var count int32
	for _, ev := range events.Items {
		if ev.InvolvedObject.Name == pb.Name && ev.Reason == "OwnerNotAllowed" {
			count += max(ev.Count, 1)
		}
	}
	return count
}

func (e *adopterEnv) release(t *testing.T, obj client.Object) {
	t.Helper()
	require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := e.client.Get(e.ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		obj.SetFinalizers(nil)
		return e.client.Update(e.ctx, obj)
	}))
	e.waitGone(t, obj)
}

func ownerUIDs(pb *iamv1alpha1.PolicyBinding) []types.UID {
	var uids []types.UID
	for _, o := range pb.OwnerReferences {
		uids = append(uids, o.UID)
	}
	return uids
}

func (e *adopterEnv) eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 30*time.Second, 100*time.Millisecond, msg)
}

func (e *adopterEnv) waitOwnedBy(t *testing.T, pb *iamv1alpha1.PolicyBinding, uid types.UID) {
	t.Helper()
	e.waitOwners(t, pb, []types.UID{uid})
}

func (e *adopterEnv) waitOwners(t *testing.T, pb *iamv1alpha1.PolicyBinding, want []types.UID) {
	t.Helper()
	e.eventually(t, func() bool {
		return e.client.Get(e.ctx, client.ObjectKeyFromObject(pb), pb) == nil && equalUIDs(ownerUIDs(pb), want)
	}, fmt.Sprintf("%s's owners become %v", pb.Name, want))
}

// keepsOwners asserts that the binding exists with exactly these owners for the whole settle period.
func (e *adopterEnv) keepsOwners(t *testing.T, pb *iamv1alpha1.PolicyBinding, want []types.UID) {
	t.Helper()
	require.Never(t, func() bool {
		err := e.client.Get(e.ctx, client.ObjectKeyFromObject(pb), pb)
		return err != nil || !equalUIDs(ownerUIDs(pb), want)
	}, settle, 100*time.Millisecond, "%s must stay, owned by %v", pb.Name, want)
}

func (e *adopterEnv) waitGone(t *testing.T, obj client.Object) {
	t.Helper()
	e.eventually(t, func() bool {
		return apierrors.IsNotFound(e.client.Get(e.ctx, client.ObjectKeyFromObject(obj), obj))
	}, fmt.Sprintf("%s is deleted", obj.GetName()))
}

func (e *adopterEnv) countOwned(t *testing.T, prefix string) int {
	t.Helper()
	owned := 0
	for _, pb := range e.bindings(t, prefix) {
		if len(pb.OwnerReferences) == 1 && string(pb.OwnerReferences[0].UID) == pb.Spec.ResourceSelector.ResourceRef.UID {
			owned++
		}
	}
	return owned
}

func (e *adopterEnv) allOwnedByTheirProjects(t *testing.T, prefix string) {
	t.Helper()
	bindings := e.bindings(t, prefix)
	require.Len(t, bindings, 60, "no binding lost")
	require.Equal(t, len(bindings), e.countOwned(t, prefix), "every binding owned by its Project")
}

func (e *adopterEnv) bindings(t *testing.T, prefix string) []iamv1alpha1.PolicyBinding {
	t.Helper()
	var list iamv1alpha1.PolicyBindingList
	require.NoError(t, e.client.List(e.ctx, &list, client.InNamespace(adopterNamespace)))
	var out []iamv1alpha1.PolicyBinding
	for _, pb := range list.Items {
		if len(pb.Name) >= len(prefix) && pb.Name[:len(prefix)] == prefix {
			out = append(out, pb)
		}
	}
	return out
}

func equalUIDs(got, want []types.UID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

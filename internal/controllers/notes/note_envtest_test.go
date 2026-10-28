package notes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notesv1alpha1 "go.miloapis.com/milo/pkg/apis/notes/v1alpha1"
)

const (
	noteNamespace     = "project-notes"
	roleNamespace     = "milo-system"
	creatorEditorRole = "notes-creator-editor"
	// providerFinalizer is the OpenFGA provider's finalizer on every binding it reconciles; it holds a deleted binding
	// until the provider has removed the binding's tuples.
	providerFinalizer = "iam.miloapis.com/policybinding"
)

type noteEnv struct {
	ctx    context.Context
	client client.WithWatch // reads and writes through the API server
	user   *iamv1alpha1.User
}

// TestCreatorEditorPolicyBinding reconciles Notes and ClusterNotes against an API server with Milo's iam and notes
// CRDs, so the binding's immutable resource selector and the delete's UID precondition are the API server's own.
// Nothing runs the authorization provider: a test marks a binding Ready, or releases its finalizer, as the provider
// would. Each case is a row of the failure-mode table of the repair.
func TestCreatorEditorPolicyBinding(t *testing.T) {
	env := startNoteEnv(t)

	t.Run("a new Note's binding names its namespace, and the Note is Ready once the binding is", func(t *testing.T) {
		note := env.note(t, "new")
		_, err := env.reconcile(note)
		require.Error(t, err, "the binding is not Ready yet")

		pb := env.binding(t, note)
		require.Equal(t, noteNamespace, pb.Spec.ResourceSelector.ResourceRef.Namespace)
		env.markReady(t, pb)
		_, err = env.reconcile(note)
		require.NoError(t, err)
		require.True(t, env.noteReady(t, note))
	})

	t.Run("F1-F4 a binding made without the namespace is deleted by its UID, waited for, and made again", func(t *testing.T) {
		note := env.note(t, "broken")
		old := env.plantBinding(t, note, withoutNamespace(note), true, providerFinalizer)

		_, err := env.reconcile(note)
		require.Error(t, err, "F1 the Note is not Ready while its binding is replaced")
		held := env.binding(t, note)
		require.Equal(t, old.UID, held.UID)
		require.NotNil(t, held.DeletionTimestamp, "F1 the old binding is deleted")

		_, err = env.reconcile(note)
		require.Error(t, err, "F2 the Note waits while the provider holds the old binding")
		require.Equal(t, old.UID, env.binding(t, note).UID, "F2 nothing is made while the old binding is deleting")
		require.Equal(t, "Waiting for the out-of-date PolicyBinding to be deleted", env.readyMessage(t, note),
			"F2 no second delete")

		env.release(t, held)
		_, err = env.reconcile(note)
		require.Error(t, err, "F3 the new binding is not Ready yet")
		made := env.binding(t, note)
		require.NotEqual(t, old.UID, made.UID)
		require.Equal(t, noteNamespace, made.Spec.ResourceSelector.ResourceRef.Namespace, "F3")
		require.Equal(t, noteManagedByValue, made.Labels[noteManagedByLabel], "F3 the label is kept")
		require.True(t, env.owns(t, note, made), "F3 the Note owns the new binding")

		env.markReady(t, made)
		_, err = env.reconcile(note)
		require.NoError(t, err)
		require.True(t, env.noteReady(t, note), "F4")
	})

	t.Run("F5 a stale read of an old binding does not delete the binding made since", func(t *testing.T) {
		note := env.note(t, "stale-new")
		current := env.plantBinding(t, note, withNamespace(note), true)
		stale := current.DeepCopy()
		stale.UID = types.UID("an-earlier-binding")
		stale.Spec.ResourceSelector.ResourceRef = ptr.To(withoutNamespace(note))

		_, err := env.reconcileWith(env.staleReader(stale), note)
		require.True(t, apierrors.IsConflict(err), "the UID precondition refuses the delete, got %v", err)
		kept := env.binding(t, note)
		require.Equal(t, current.UID, kept.UID)
		require.Nil(t, kept.DeletionTimestamp)
	})

	t.Run("F6 a stale read of a binding already gone counts as deleted, and the next reconcile makes it", func(t *testing.T) {
		note := env.note(t, "stale-gone")
		gone := env.plantBinding(t, note, withoutNamespace(note), true)
		require.NoError(t, env.client.Delete(env.ctx, gone))

		_, err := env.reconcileWith(env.staleReader(gone), note)
		require.False(t, apierrors.IsNotFound(err), "a delete that finds nothing is not an error, got %v", err)
		require.Equal(t, "Replacing PolicyBinding with an out-of-date resource selector", env.readyMessage(t, note))

		_, err = env.reconcile(note)
		require.Error(t, err, "the new binding is not Ready yet")
		require.Equal(t, noteNamespace, env.binding(t, note).Spec.ResourceSelector.ResourceRef.Namespace)
	})

	t.Run("F7 a same-named binding the Note does not own is never deleted", func(t *testing.T) {
		note := env.note(t, "foreign")
		foreign := env.plantBinding(t, note, withoutNamespace(note), false)
		env.reconcileTwiceKeeps(t, note, foreign, "not Ready, as before the fix")
	})

	t.Run("F8 a correct binding is never written", func(t *testing.T) {
		note := env.note(t, "correct")
		pb := env.plantBinding(t, note, withNamespace(note), true)
		env.markReady(t, pb)
		env.reconcileTwiceKeeps(t, note, pb, "")
		require.True(t, env.noteReady(t, note))
	})

	t.Run("F9 a ClusterNote's binding names no namespace and is never replaced", func(t *testing.T) {
		cn := env.clusterNote(t, "cluster")
		_, err := env.reconcile(cn)
		require.Error(t, err, "the binding is not Ready yet")
		pb := env.binding(t, cn)
		require.Empty(t, pb.Spec.ResourceSelector.ResourceRef.Namespace)
		env.markReady(t, pb)
		env.reconcileTwiceKeeps(t, cn, pb, "")
	})
}

// reconcileTwiceKeeps reconciles the note twice and asserts that the binding is the same object, unwritten and not
// deleting. wantErr is the reason the reconcile fails, or empty when it must succeed.
func (e *noteEnv) reconcileTwiceKeeps(t *testing.T, note NoteResource, pb *iamv1alpha1.PolicyBinding, wantErr string) {
	t.Helper()
	before := e.binding(t, note)
	for range 2 {
		_, err := e.reconcile(note)
		if wantErr == "" {
			require.NoError(t, err)
		} else {
			require.Error(t, err, wantErr)
		}
	}
	after := e.binding(t, note)
	require.Equal(t, pb.UID, after.UID)
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "the binding is not written")
	require.Nil(t, after.DeletionTimestamp)
}

// startNoteEnv starts an API server with Milo's iam and notes CRDs and creates the notes' creator.
func startNoteEnv(t *testing.T) *noteEnv {
	t.Helper()
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
			filepath.Join(root, "config", "crd", "bases", "notes"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testEnv.Stop() })

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, iamv1alpha1.AddToScheme(scheme))
	require.NoError(t, notesv1alpha1.AddToScheme(scheme))
	direct, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	for _, ns := range []string{noteNamespace, roleNamespace} {
		require.NoError(t, direct.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	}
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "creator"},
		Spec:       iamv1alpha1.UserSpec{Email: "creator@example.com"},
	}
	require.NoError(t, direct.Create(ctx, user))
	return &noteEnv{ctx: ctx, client: direct, user: user}
}

func (e *noteEnv) note(t *testing.T, name string) *notesv1alpha1.Note {
	t.Helper()
	note := &notesv1alpha1.Note{
		ObjectMeta: metav1.ObjectMeta{Namespace: noteNamespace, Name: name},
		Spec: notesv1alpha1.NoteSpec{
			SubjectRef: notesv1alpha1.SubjectReference{Kind: "ConfigMap", Name: name, Namespace: noteNamespace},
			Content:    "a note",
			CreatorRef: iamv1alpha1.UserReference{Name: e.user.Name},
		},
	}
	require.NoError(t, e.client.Create(e.ctx, note))
	return note
}

func (e *noteEnv) clusterNote(t *testing.T, name string) *notesv1alpha1.ClusterNote {
	t.Helper()
	cn := &notesv1alpha1.ClusterNote{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: notesv1alpha1.NoteSpec{
			SubjectRef: notesv1alpha1.SubjectReference{APIGroup: "iam.miloapis.com", Kind: "User", Name: e.user.Name},
			Content:    "a cluster note",
			CreatorRef: iamv1alpha1.UserReference{Name: e.user.Name},
		},
	}
	require.NoError(t, e.client.Create(e.ctx, cn))
	return cn
}

// staleReader reads every PolicyBinding as stale, as an informer cache can until a later event reaches it; every
// other read and every write goes to the API server.
func (e *noteEnv) staleReader(stale *iamv1alpha1.PolicyBinding) client.Client {
	return interceptor.NewClient(e.client, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if pb, ok := obj.(*iamv1alpha1.PolicyBinding); ok {
				stale.DeepCopyInto(pb)
				return nil
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
}

// reconcile runs one reconcile of the note, as the controller's work queue would.
func (e *noteEnv) reconcile(note NoteResource) (ctrl.Result, error) {
	return e.reconcileWith(e.client, note)
}

func (e *noteEnv) reconcileWith(c client.Client, note NoteResource) (ctrl.Result, error) {
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(note)}
	if _, ok := note.(*notesv1alpha1.ClusterNote); ok {
		r := &ClusterNoteController{Client: c, CreatorEditorRoleName: creatorEditorRole, CreatorEditorRoleNamespace: roleNamespace}
		return r.Reconcile(e.ctx, req)
	}
	r := &NoteController{Client: c, CreatorEditorRoleName: creatorEditorRole, CreatorEditorRoleNamespace: roleNamespace}
	return r.Reconcile(e.ctx, req)
}

// bindingKey is where the controller keeps a note's creator binding: beside a Note, and in the role's namespace for a
// ClusterNote.
func bindingKey(note NoteResource) types.NamespacedName {
	if note.GetNamespace() == "" {
		return types.NamespacedName{Namespace: roleNamespace, Name: "note-creator-editor-" + note.GetName()}
	}
	return types.NamespacedName{
		Namespace: note.GetNamespace(),
		Name:      "note-creator-editor-" + note.GetNamespace() + "-" + note.GetName(),
	}
}

func (e *noteEnv) binding(t *testing.T, note NoteResource) *iamv1alpha1.PolicyBinding {
	t.Helper()
	pb := &iamv1alpha1.PolicyBinding{}
	require.NoError(t, e.client.Get(e.ctx, bindingKey(note), pb))
	return pb
}

// withoutNamespace is the resource reference the controller wrote before the fix.
func withoutNamespace(note NoteResource) iamv1alpha1.ResourceReference {
	return iamv1alpha1.ResourceReference{
		APIGroup: "notes.miloapis.com", Kind: note.GetNoteKind(), Name: note.GetName(), UID: string(note.GetUID()),
	}
}

// withNamespace is the resource reference a Note's binding needs: the note's identity and its namespace.
func withNamespace(note NoteResource) iamv1alpha1.ResourceReference {
	ref := withoutNamespace(note)
	ref.Namespace = note.GetNamespace()
	return ref
}

// plantBinding creates the note's creator binding as an earlier controller or another writer left it.
func (e *noteEnv) plantBinding(t *testing.T, note NoteResource, ref iamv1alpha1.ResourceReference, owned bool, finalizers ...string) *iamv1alpha1.PolicyBinding {
	t.Helper()
	key := bindingKey(note)
	pb := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: key.Namespace, Name: key.Name, Finalizers: finalizers,
			Labels: map[string]string{noteManagedByLabel: noteManagedByValue},
		},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef:          iamv1alpha1.RoleReference{Name: creatorEditorRole, Namespace: roleNamespace},
			Subjects:         []iamv1alpha1.Subject{{Kind: "User", Name: e.user.Name, UID: string(e.user.UID)}},
			ResourceSelector: iamv1alpha1.ResourceSelector{ResourceRef: &ref},
		},
	}
	if owned {
		require.NoError(t, controllerutil.SetOwnerReference(note, pb, e.client.Scheme()))
	}
	require.NoError(t, e.client.Create(e.ctx, pb))
	return pb
}

func (e *noteEnv) owns(t *testing.T, note NoteResource, pb *iamv1alpha1.PolicyBinding) bool {
	t.Helper()
	for _, o := range pb.OwnerReferences {
		if o.UID == note.GetUID() {
			return true
		}
	}
	return false
}

// markReady writes the Ready condition the provider writes once the binding's tuples are in OpenFGA.
func (e *noteEnv) markReady(t *testing.T, pb *iamv1alpha1.PolicyBinding) {
	t.Helper()
	require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := e.client.Get(e.ctx, client.ObjectKeyFromObject(pb), pb); err != nil {
			return err
		}
		meta.SetStatusCondition(&pb.Status.Conditions, metav1.Condition{
			Type: "Ready", Status: metav1.ConditionTrue, Reason: "Test", Message: "tuples written",
		})
		return e.client.Status().Update(e.ctx, pb)
	}))
}

// release removes the binding's finalizers, as the provider does once the tuples are gone, and waits until it is gone.
func (e *noteEnv) release(t *testing.T, pb *iamv1alpha1.PolicyBinding) {
	t.Helper()
	require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := e.client.Get(e.ctx, client.ObjectKeyFromObject(pb), pb); err != nil {
			return err
		}
		pb.Finalizers = nil
		return e.client.Update(e.ctx, pb)
	}))
	require.True(t, apierrors.IsNotFound(e.client.Get(e.ctx, client.ObjectKeyFromObject(pb), pb)))
}

func (e *noteEnv) noteReady(t *testing.T, note *notesv1alpha1.Note) bool {
	t.Helper()
	require.NoError(t, e.client.Get(e.ctx, client.ObjectKeyFromObject(note), note))
	return meta.IsStatusConditionTrue(note.Status.Conditions, noteReadyConditionType)
}

func (e *noteEnv) readyMessage(t *testing.T, note *notesv1alpha1.Note) string {
	t.Helper()
	require.NoError(t, e.client.Get(e.ctx, client.ObjectKeyFromObject(note), note))
	cond := meta.FindStatusCondition(note.Status.Conditions, noteReadyConditionType)
	require.NotNil(t, cond)
	return cond.Message
}

package lifecycle

import (
	"context"
	"slices"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// grantedManualClaim returns a manually created ResourceClaim (no auto-created
// markers) with a Granted=True condition and no owner references.
func grantedManualClaim(name, namespace string) *quotav1alpha1.ResourceClaim {
	claim := manualClaim(name, namespace)
	claim.Status.Conditions = []metav1.Condition{{
		Type:               quotav1alpha1.ResourceClaimGranted,
		Status:             metav1.ConditionTrue,
		Reason:             quotav1alpha1.ResourceClaimGrantedReason,
		LastTransitionTime: metav1.Time{Time: time.Now()},
	}}
	return claim
}

// TestIsAutoCreatedClaim verifies the package-level helper correctly
// distinguishes admission-plugin claims from manually created ones.
func TestIsAutoCreatedClaim(t *testing.T) {
	cases := []struct {
		name  string
		claim *quotav1alpha1.ResourceClaim
		want  bool
	}{
		{
			name:  "both markers present",
			claim: autoClaim("auto", "default", "", "", time.Now()),
			want:  true,
		},
		{
			name:  "manual claim - no markers",
			claim: manualClaim("manual", "default"),
			want:  false,
		},
		{
			name: "label present, annotation missing",
			claim: &quotav1alpha1.ResourceClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "label-only",
					Namespace: "default",
					Labels:    map[string]string{"quota.miloapis.com/auto-created": "true"},
				},
			},
			want: false,
		},
		{
			name: "annotation present, label missing",
			claim: &quotav1alpha1.ResourceClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "annotation-only",
					Namespace:   "default",
					Annotations: map[string]string{"quota.miloapis.com/created-by": "claim-creation-plugin"},
				},
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAutoCreatedClaim(tc.claim); got != tc.want {
				t.Errorf("isAutoCreatedClaim() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResourceClaimOwnershipController_Reconcile verifies that Reconcile skips
// manually created claims and only proceeds with admission-plugin claims.
func TestResourceClaimOwnershipController_Reconcile(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	scheme := testScheme(t)

	cases := []struct {
		name        string
		claim       *quotav1alpha1.ResourceClaim
		wantErr     bool
		wantDeleted bool
	}{
		{
			name:        "manual claim with Granted=True is not touched",
			claim:       grantedManualClaim("manual-granted", "default"),
			wantErr:     false,
			wantDeleted: false,
		},
		{
			name: "claim with auto-created label but missing annotation is not touched",
			claim: func() *quotav1alpha1.ResourceClaim {
				c := grantedManualClaim("label-only", "default")
				c.Labels = map[string]string{"quota.miloapis.com/auto-created": "true"}
				return c
			}(),
			wantErr:     false,
			wantDeleted: false,
		},
		{
			name: "claim with created-by annotation but missing label is not touched",
			claim: func() *quotav1alpha1.ResourceClaim {
				c := grantedManualClaim("annotation-only", "default")
				c.Annotations = map[string]string{"quota.miloapis.com/created-by": "claim-creation-plugin"}
				return c
			}(),
			wantErr:     false,
			wantDeleted: false,
		},
		{
			// Auto-created claim passes the guard and reaches resolveOwner, which
			// returns an error because restMapper is nil in this unit test.
			// This confirms the guard does not block auto-created claims.
			name:        "auto-created granted claim proceeds past guard to owner resolution",
			claim:       autoClaim("auto-granted", "default", "True", quotav1alpha1.ResourceClaimGrantedReason, now.Add(-10*time.Second)),
			wantErr:     true,
			wantDeleted: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.claim).Build()
			mgr := &testManager{cluster: &testCluster{client: cli}}

			r := &ResourceClaimOwnershipController{
				Scheme:  scheme,
				Manager: mgr,
				// restMapper intentionally nil so resolveOwner returns early with
				// "RESTMapper not initialized", which is the expected error for
				// auto-created claims that pass the guard in this unit test.
			}

			req := mcreconcile.Request{
				Request: ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      tc.claim.Name,
						Namespace: tc.claim.Namespace,
					},
				},
			}

			_, err := r.Reconcile(context.Background(), req)
			if (err != nil) != tc.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tc.wantErr)
			}

			var got quotav1alpha1.ResourceClaim
			getErr := cli.Get(context.Background(), client.ObjectKey{Name: tc.claim.Name, Namespace: tc.claim.Namespace}, &got)
			if deleted := (getErr != nil); deleted != tc.wantDeleted {
				t.Errorf("deleted=%v want=%v (get err=%v)", deleted, tc.wantDeleted, getErr)
			}
		})
	}
}

var widgetGVK = schema.GroupVersionKind{Group: "test.miloapis.com", Version: "v1", Kind: "Widget"}

// never is a virtual time no replay reaches.
const never = time.Hour

// ownerFixture is a reconciler on a fake cluster. The owner "widget" answers a read when exists says so.
type ownerFixture struct {
	r     *ResourceClaimOwnershipController
	cli   client.Client
	reads int
}

func newOwnerFixture(t *testing.T, exists func() bool, objs ...client.Object) *ownerFixture {
	t.Helper()
	f := &ownerFixture{cli: fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()}
	owner := &unstructured.Unstructured{}
	owner.SetGroupVersionKind(widgetGVK)
	owner.SetNamespace("default")
	owner.SetName("widget")
	owner.SetUID("widget-uid")
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("get", "widgets", func(clienttesting.Action) (bool, runtime.Object, error) {
		f.reads++
		if exists() {
			return true, owner.DeepCopy(), nil
		}
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: widgetGVK.Group, Resource: "widgets"}, "widget")
	})
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{widgetGVK.GroupVersion()})
	mapper.Add(widgetGVK, meta.RESTScopeNamespace)
	f.r = &ResourceClaimOwnershipController{
		Manager:          &testManager{cluster: &testCluster{client: f.cli}},
		restMapper:       mapper,
		newDynamicClient: func(*rest.Config) (dynamic.Interface, error) { return dyn, nil },
	}
	return f
}

// widgetClaim returns a granted auto-created claim for the owner "widget", created at created.
func widgetClaim(name string, created time.Time) *quotav1alpha1.ResourceClaim {
	claim := autoClaim(name, "default", "True", quotav1alpha1.ResourceClaimGrantedReason, created)
	claim.Spec.ResourceRef = &quotav1alpha1.UnversionedObjectReference{
		APIGroup: widgetGVK.Group, Kind: widgetGVK.Kind, Name: "widget", Namespace: "default",
	}
	return claim
}

func claimRequest(name string) mcreconcile.Request {
	return mcreconcile.Request{Request: ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}}
}

// ownerTimeline replays one claim's reconciles in virtual time, measured from the claim's grant. The owner
// exists from storedAt until goneAt, and each reconcile comes after the wait the previous one returned.
type ownerTimeline struct {
	storedAt, goneAt time.Duration
	now              time.Duration
}

func (tl *ownerTimeline) ownerExists() bool {
	return tl.now >= tl.storedAt && tl.now < tl.goneAt
}

// replay reconciles the claim until it is owned or deleted, or the virtual clock passes limit. It returns
// the claim (nil once deleted), the virtual time of the last reconcile and the number of owner reads.
func (tl *ownerTimeline) replay(t *testing.T, limit time.Duration) (*quotav1alpha1.ResourceClaim, time.Duration, int) {
	t.Helper()
	ctx := context.Background()
	f := newOwnerFixture(t, tl.ownerExists, widgetClaim("claim", time.Now()))
	req := claimRequest("claim")
	for tl.now <= limit {
		var current quotav1alpha1.ResourceClaim
		if err := f.cli.Get(ctx, req.NamespacedName, &current); err != nil {
			t.Fatalf("get claim at %v: %v", tl.now, err)
		}
		// The controller reads the claim's age from the claim, so the claim moves back by the virtual time.
		current.CreationTimestamp = metav1.NewTime(time.Now().Add(-tl.now))
		if err := f.cli.Update(ctx, &current); err != nil {
			t.Fatalf("age claim at %v: %v", tl.now, err)
		}
		res, err := f.r.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile at %v: %v", tl.now, err)
		}
		if res.RequeueAfter == 0 {
			break
		}
		tl.now += res.RequeueAfter
	}

	var final quotav1alpha1.ResourceClaim
	if err := f.cli.Get(ctx, req.NamespacedName, &final); apierrors.IsNotFound(err) {
		return nil, tl.now, f.reads
	} else if err != nil {
		t.Fatalf("get final claim: %v", err)
	}
	return &final, tl.now, f.reads
}

// TestResourceClaimOwnershipController_OwnerTimeline drives the owner lookup through the owner's life: stored
// after the first read (the admission plugin holds the create until the claim is granted), stored late, and
// never stored.
func TestResourceClaimOwnershipController_OwnerTimeline(t *testing.T) {
	cases := []timelineCase{
		{
			// The first read runs before the owner is stored, and the owner is deleted 0.8 s after the grant.
			// A next read 2 s later finds nothing, and the claim holds its quota until the safety net.
			name:        "owner stored just after the first read and deleted at 0.8 s",
			storedAt:    30 * time.Millisecond,
			goneAt:      800 * time.Millisecond,
			wantOwnedBy: 800 * time.Millisecond,
		},
		{
			// Later than any gap measured between a grant and its owner's commit: no wait between reads may
			// exceed the 2 s the controller used before.
			name:        "owner stored at 7 s",
			storedAt:    7 * time.Second,
			goneAt:      never,
			wantOwnedBy: 9 * time.Second,
		},
		{
			// A true orphan (its create failed after the grant): deleted after the grace, with about 20 reads
			// in the grace rather than a loop at the first step.
			name:         "owner never stored",
			storedAt:     never,
			goneAt:       never,
			wantMaxReads: 25,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tl := &ownerTimeline{storedAt: tc.storedAt, goneAt: tc.goneAt}
			claim, at, reads := tl.replay(t, 2*time.Minute)
			tc.check(t, claim, at)
			if tc.wantMaxReads > 0 && reads > tc.wantMaxReads {
				t.Errorf("%d owner reads; want at most %d", reads, tc.wantMaxReads)
			}
		})
	}
}

type timelineCase struct {
	name             string
	storedAt, goneAt time.Duration
	wantOwnedBy      time.Duration // 0: the claim must end deleted, not owned
	wantMaxReads     int           // 0: not checked
}

// check compares the claim at the replay's end, the virtual time at, with the case's outcome.
func (tc timelineCase) check(t *testing.T, claim *quotav1alpha1.ResourceClaim, at time.Duration) {
	t.Helper()
	if tc.wantOwnedBy == 0 {
		if claim != nil {
			t.Fatalf("claim not deleted after %v (owner refs %v)", at, claim.OwnerReferences)
		}
		return
	}
	if claim == nil {
		t.Fatalf("claim deleted at %v; want an owner reference by %v", at, tc.wantOwnedBy)
	}
	if len(claim.OwnerReferences) != 1 || claim.OwnerReferences[0].UID != "widget-uid" {
		t.Fatalf("owner refs %v at %v; want one to widget-uid", claim.OwnerReferences, at)
	}
	if at > tc.wantOwnedBy {
		t.Errorf("owner reference set at %v; want by %v", at, tc.wantOwnedBy)
	}
}

// TestResourceClaimOwnershipController_BackoffEndsWithEveryExit starts each claim one step into its backoff and
// checks that every exit but "owner not stored yet" forgets it, so a later claim of the same name starts again
// at the first wait.
func TestResourceClaimOwnershipController_BackoffEndsWithEveryExit(t *testing.T) {
	now := time.Now()
	owned := widgetClaim("claim", now)
	owned.OwnerReferences = []metav1.OwnerReference{{APIVersion: "test.miloapis.com/v1", Kind: "Widget", Name: "widget", UID: "widget-uid"}}
	deleting := widgetClaim("claim", now)
	deleting.Finalizers = []string{"test.miloapis.com/hold"}
	deleting.DeletionTimestamp = &metav1.Time{Time: now}
	notGranted := widgetClaim("claim", now)
	notGranted.Status.Conditions = nil

	cases := []struct {
		name         string
		claim        *quotav1alpha1.ResourceClaim // nil: the claim is gone
		ownerStored  bool
		noRESTMapper bool
		wantErr      bool
		wantSteps    int
	}{
		{name: "claim gone"},
		{name: "claim deleting", claim: deleting},
		{name: "manual claim", claim: grantedManualClaim("claim", "default")},
		{name: "claim not granted", claim: notGranted},
		{name: "claim already owned", claim: owned},
		{name: "owner found", claim: widgetClaim("claim", now), ownerStored: true},
		{name: "orphan past max age deleted", claim: widgetClaim("claim", now.Add(-time.Minute))},
		{name: "owner read error", claim: widgetClaim("claim", now), noRESTMapper: true, wantErr: true},
		{name: "owner not stored yet", claim: widgetClaim("claim", now), wantSteps: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.claim != nil {
				objs = append(objs, tc.claim)
			}
			f := newOwnerFixture(t, func() bool { return tc.ownerStored }, objs...)
			if tc.noRESTMapper {
				f.r.restMapper = nil
			}
			req := claimRequest("claim")
			f.r.backoffFor(req).next()

			if _, err := f.r.Reconcile(context.Background(), req); (err != nil) != tc.wantErr {
				t.Fatalf("Reconcile() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got := f.r.ownerReadBackoff.NumRequeues(req); got != tc.wantSteps {
				t.Errorf("backoff steps after the reconcile = %d; want %d", got, tc.wantSteps)
			}
		})
	}
}

// TestResourceClaimOwnershipController_BackoffPerCluster checks that claims of the same name in two project
// control planes keep separate backoffs.
func TestResourceClaimOwnershipController_BackoffPerCluster(t *testing.T) {
	ctx := context.Background()
	f := newOwnerFixture(t, func() bool { return false }, widgetClaim("claim", time.Now()))
	a, b := claimRequest("claim"), claimRequest("claim")
	a.ClusterName, b.ClusterName = "project-a", "project-b"

	var waitsA []time.Duration
	for range 2 {
		res, err := f.r.Reconcile(ctx, a)
		if err != nil {
			t.Fatalf("Reconcile(a): %v", err)
		}
		waitsA = append(waitsA, res.RequeueAfter)
	}
	resB, err := f.r.Reconcile(ctx, b)
	if err != nil {
		t.Fatalf("Reconcile(b): %v", err)
	}

	want := []time.Duration{ownerReadFirstWait, 2 * ownerReadFirstWait, ownerReadFirstWait}
	if got := append(waitsA, resB.RequeueAfter); !slices.Equal(got, want) {
		t.Errorf("waits a, a, b = %v; want %v", got, want)
	}
}

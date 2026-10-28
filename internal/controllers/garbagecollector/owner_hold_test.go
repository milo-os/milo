package garbagecollector

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakemetadata "k8s.io/client-go/metadata/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2/ktesting"
)

// newHoldGC builds a collector with a root builder and one project builder over a fake metadata client, as upstream's
// TestConflictingData builds its collector (k8s.io/kubernetes v1.37.1, garbagecollector_test.go).
func newHoldGC(served ServedTypes, narrow bool) (*GarbageCollector, *fakemetadata.FakeMetadataClient) {
	md := fakemetadata.NewSimpleMetadataClient(fakemetadata.NewTestScheme())
	mapper := newTestRESTMapper()
	absent := NewReferenceCache(100)
	builder := func(project string) *GraphBuilder {
		return &GraphBuilder{
			project:          project,
			restMapper:       mapper,
			metadataClient:   md,
			eventRecorder:    record.NewFakeRecorder(100),
			uidToNode:        &concurrentUIDToNode{uidToNodeLock: sync.RWMutex{}, uidToNode: map[types.UID]*node{}},
			absentOwnerCache: absent,
		}
	}
	gc := &GarbageCollector{
		metadataClient:          md,
		restMapper:              mapper,
		absentOwnerCache:        absent,
		dependencyGraphBuilders: []*GraphBuilder{builder(""), builder("p")},
	}
	gc.SetServedTypes(served, narrow)
	return gc, md
}

func ownerRef(r schema.GroupVersionResource, name string) metav1.OwnerReference {
	return metav1.OwnerReference{APIVersion: r.GroupVersion().String(), Kind: kindOf(r), Name: name,
		UID: types.UID(name + "-uid")}
}

// putDependent stores a widget in project p, owned by owner, in the fake API and in p's graph.
func putDependent(t *testing.T, gc *GarbageCollector, md *fakemetadata.FakeMetadataClient, owner metav1.OwnerReference) *node {
	t.Helper()
	n := &node{
		identity: objectReference{
			OwnerReference: metav1.OwnerReference{APIVersion: "example.com/v1", Kind: "Widget", Name: "dep", UID: "dep-uid"},
			Namespace:      "ns1", Project: "p",
		},
		owners:     []metav1.OwnerReference{owner},
		dependents: map[*node]struct{}{},
	}
	obj := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: "example.com/v1", Kind: "Widget"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dep", UID: "dep-uid",
			OwnerReferences: []metav1.OwnerReference{owner}},
	}
	if _, err := md.Resource(widgets).Namespace("ns1").(fakemetadata.MetadataClient).CreateFake(obj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	md.ClearActions()
	gc.builderForProject("p").uidToNode.Write(n)
	return n
}

func deleted(md *fakemetadata.FakeMetadataClient) bool {
	return slices.ContainsFunc(md.Actions(), func(a clienttesting.Action) bool { return a.GetVerb() == "delete" })
}

// TestOwnerOfUnservedKindIsHeld proves F4: with narrowing on, a project does not watch an owner whose kind it does not
// serve, and a lookup of that owner can answer NotFound once the API server refuses the kind there. Such a reference
// is held, never read as gone. Each owner below is absent from the API.
func TestOwnerOfUnservedKindIsHeld(t *testing.T) {
	cases := []struct {
		name         string
		served       *fakeServed
		narrow       bool
		owner        schema.GroupVersionResource
		absentCached bool
		wantHold     bool
	}{
		{name: "narrowing off: an absent owner of a hidden kind is gone, as on main",
			served: newFakeServed(true, gizmos), owner: gizmos},
		{name: "narrowing on: an owner of a hidden kind is held",
			served: newFakeServed(true, gizmos), narrow: true, owner: gizmos, wantHold: true},
		{name: "narrowing on: held although the absent-owner cache names the owner",
			served: newFakeServed(true, gizmos), narrow: true, owner: gizmos, absentCached: true, wantHold: true},
		{name: "narrowing on, registry unsynced: held, because a hidden kind cannot be told from a served one",
			served: newFakeServed(false, gizmos), narrow: true, owner: sprites, wantHold: true},
		{name: "narrowing on: an absent owner of a served kind is gone",
			served: newFakeServed(true, gizmos), narrow: true, owner: sprites},
	}
	registerUnservedMetrics()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dependentHolds.Reset()
			_, ctx := ktesting.NewTestContext(t)
			gc, md := newHoldGC(tc.served, tc.narrow)
			owner := ownerRef(tc.owner, "owner")
			if tc.absentCached {
				gc.absentOwnerCache.Add(objectReference{OwnerReference: ownerReferenceCoordinates(owner), Namespace: "ns1"})
			}
			dep := putDependent(t, gc, md, owner)

			assertHeld(t, gc, md, dep, gc.attemptToDeleteItem(ctx, dep), tc.wantHold)
			if tc.wantHold {
				if action := gc.attemptToDeleteWorker(ctx, dep); action != requeueItem {
					t.Fatalf("a held dependent must be requeued, got %v", action)
				}
				assertSeries(t, "garbagecollector_dependent_holds_total",
					map[string]float64{tc.owner.Group + "/" + tc.owner.Resource: 1})
			}
		})
	}
}

// TestLogItemErrorCountsHoldsByOwnerType proves the counter of the holds met: each hold counts once by its owner's type,
// so a dependent retried with backoff counts each attempt, and an error that is not a hold counts nothing.
func TestLogItemErrorCountsHoldsByOwnerType(t *testing.T) {
	registerUnservedMetrics()
	dependentHolds.Reset()
	logger, _ := ktesting.NewTestContext(t)
	dep := &node{identity: objectReference{OwnerReference: metav1.OwnerReference{Name: "dep", UID: "dep-uid"}}}

	for _, err := range []error{
		&unservedOwnerError{owner: gizmos.GroupResource()},
		&unservedOwnerError{owner: gizmos.GroupResource()},
		&unservedOwnerError{owner: sprites.GroupResource()},
		newRESTMappingError("Gizmo", "example.com/v1"),
		errors.New("not a hold"),
	} {
		logItemError(logger, dep, err)
	}
	assertSeries(t, "garbagecollector_dependent_holds_total", map[string]float64{
		gizmos.Group + "/" + gizmos.Resource: 2, sprites.Group + "/" + sprites.Resource: 1})
}

// assertHeld checks attemptToDeleteItem's outcome: a hold returns unservedOwnerError and deletes nothing; otherwise the
// dependent of an absent owner is deleted.
func assertHeld(t *testing.T, gc *GarbageCollector, md *fakemetadata.FakeMetadataClient, dep *node, err error, wantHold bool) {
	t.Helper()
	var hold *unservedOwnerError
	if got := errors.As(err, &hold); got != wantHold {
		t.Fatalf("held = %v, want %v (err: %v)", got, wantHold, err)
	}
	if !wantHold && err != nil {
		t.Fatalf("attemptToDeleteItem: %v", err)
	}
	if got := deleted(md); got == wantHold {
		t.Fatalf("dependent deleted = %v, want %v; actions: %v", got, !wantHold, actionsOf(md))
	}
}

func actionsOf(md *fakemetadata.FakeMetadataClient) []string {
	var out []string
	for _, a := range md.Actions() {
		out = append(out, fmt.Sprintf("%s %s", a.GetVerb(), a.GetResource().Resource))
	}
	return out
}

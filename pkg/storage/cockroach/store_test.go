package cockroach

import (
	"testing"

	storagetesting "k8s.io/apiserver/pkg/storage/testing"
)

// Conformance with storage.Interface: the upstream storage tests that etcd3's
// store_test.go runs. See watch_test.go for the watch tests and
// corrupt_test.go for the tests of unreadable objects.
//
// The ones etcd3 runs and this store doesn't:
//   - RunOptionalTestProgressNotify: only the watch cache asks for progress
//     notifications, and no watch cache sits in front of these stores.
//   - RunBenchmarkStoreList, RunBenchmarkStoreStats,
//     RunBenchmarkWriteThroughput: benchmarks, not conformance.

func TestCreate(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestCreate(ctx, t, s, checkStorageInvariants(s))
}

func TestCreateWithKeyExist(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestCreateWithKeyExist(ctx, t, s)
}

func TestGet(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGet(ctx, t, s)
}

func TestUnconditionalDelete(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestUnconditionalDelete(ctx, t, s)
}

func TestConditionalDelete(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestConditionalDelete(ctx, t, s)
}

func TestDeleteWithSuggestion(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestDeleteWithSuggestion(ctx, t, s)
}

func TestDeleteWithSuggestionAndConflict(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestDeleteWithSuggestionAndConflict(ctx, t, s)
}

func TestDeleteWithConflict(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestDeleteWithConflict(ctx, t, s)
}

func TestDeleteWithSuggestionOfDeletedObject(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestDeleteWithSuggestionOfDeletedObject(ctx, t, s)
}

func TestValidateDeletionWithSuggestion(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestValidateDeletionWithSuggestion(ctx, t, s)
}

func TestValidateDeletionWithOnlySuggestionValid(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestValidateDeletionWithOnlySuggestionValid(ctx, t, s)
}

func TestPreconditionalDeleteWithSuggestion(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestPreconditionalDeleteWithSuggestion(ctx, t, s)
}

func TestPreconditionalDeleteWithOnlySuggestionPass(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestPreconditionalDeleteWithOnlySuggestionPass(ctx, t, s)
}

func TestList(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestList(ctx, t, s, compaction(s), false, nil)
}

func TestConsistentList(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestConsistentList(ctx, t, s, increaseRV(s), false, true, false)
}

func TestGetListNonRecursive(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGetListNonRecursive(ctx, t, increaseRV(s), s)
}

func TestGetListRecursivePrefix(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGetListRecursivePrefix(ctx, t, s)
}

func TestListContinuation(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestListContinuation(ctx, t, s, nil)
}

func TestListPaginationRareObject(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestListPaginationRareObject(ctx, t, s, nil)
}

func TestListContinuationWithFilter(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestListContinuationWithFilter(ctx, t, s, nil)
}

func TestListResourceVersionMatch(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestListResourceVersionMatch(ctx, t, s)
}

func TestGuaranteedUpdate(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGuaranteedUpdate(ctx, t, s, checkStorageInvariants(s))
}

func TestGuaranteedUpdateChecksStoredData(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGuaranteedUpdateChecksStoredData(ctx, t, s)
}

func TestGuaranteedUpdateWithConflict(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGuaranteedUpdateWithConflict(ctx, t, s)
}

func TestGuaranteedUpdateWithSuggestionAndConflict(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGuaranteedUpdateWithSuggestionAndConflict(ctx, t, s)
}

func TestTransformationFailure(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestTransformationFailure(ctx, t, s)
}

func TestStats(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestStats(ctx, t, s, s.codec, s.transformer, false)
}

func TestListPaging(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestListPaging(ctx, t, s)
}

func TestNamespaceScopedList(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestNamespaceScopedList(ctx, t, s)
}

func TestListInconsistentContinuation(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestListInconsistentContinuation(ctx, t, s, compaction(s))
}

func TestCreateWithTTL(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestCreateWithTTL(ctx, t, sweeping(t, s))
}

func TestGuaranteedUpdateWithTTL(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestGuaranteedUpdateWithTTL(ctx, t, sweeping(t, s))
}

func TestCompactRevision(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestCompactRevision(ctx, t, s, increaseRV(s), compaction(s))
}

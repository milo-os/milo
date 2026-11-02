package cockroach

import (
	"testing"

	storagetesting "k8s.io/apiserver/pkg/storage/testing"
)

// Conformance with storage.Interface: the upstream tests of creates, gets,
// updates and deletes that etcd3's store_test.go runs.

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

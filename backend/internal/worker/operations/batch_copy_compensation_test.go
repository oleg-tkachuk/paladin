package operations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// ─── fakes (only the methods copyOne touches do real work) ───────────────

type copyFakeRepo struct{ created objecth.Object }

func (f *copyFakeRepo) CreateObject(context.Context, objecth.CreateObjectArgs) (objecth.Object, error) {
	return f.created, nil
}

// unused-by-copyOne Repository methods:
func (*copyFakeRepo) FindByName(context.Context, uuid.UUID, string, string) (objecth.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) FindByIDs(context.Context, uuid.UUID, []uuid.UUID) ([]objecth.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) ObjectLock(context.Context, uuid.UUID, uuid.UUID) (objecth.ObjectLock, error) {
	panic("unused")
}
func (*copyFakeRepo) FindByPath(context.Context, uuid.UUID, string, string) (objecth.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) UpdateMetadata(context.Context, objecth.UpdateMetadataArgs) (objecth.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) ListObjects(context.Context, objecth.ListObjectsArgs) ([]objecth.Object, string, error) {
	panic("unused")
}
func (*copyFakeRepo) CountObjects(context.Context, objecth.CountObjectsArgs) (int64, bool, error) {
	panic("unused")
}
func (*copyFakeRepo) ListDistinctTags(context.Context, uuid.UUID, string, string, int32, int32) (objecth.DistinctTagPage, error) {
	panic("unused")
}
func (*copyFakeRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	panic("unused")
}
func (*copyFakeRepo) LookupBucketMeta(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
	panic("unused")
}
func (*copyFakeRepo) UpdateMetadataTx(context.Context, pgx.Tx, objecth.UpdateMetadataArgs) (objecth.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) HardDeleteTx(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int64) error {
	panic("unused")
}
func (*copyFakeRepo) HardDeleteWithBypassTx(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int64) error {
	panic("unused")
}
func (*copyFakeRepo) RunInTx(context.Context, func(context.Context, pgx.Tx) error) error {
	panic("unused")
}

// Purge debt is a no-op in these fakes: the permanent-delete path is
// covered end-to-end in tests/integration/components, where a real pending_purges
// row is the assertion.
func (*copyFakeRepo) EnqueuePurgeTx(context.Context, pgx.Tx, objecth.PurgeDebt) error { return nil }
func (*copyFakeRepo) PurgeBytesTx(context.Context, pgx.Tx, uuid.UUID, string, string, func() error) (bool, error) {
	panic("unused")
}
func (*copyFakeRepo) SettlePurgeTx(context.Context, pgx.Tx, uuid.UUID) error { return nil }
func (*copyFakeRepo) LiveCollision(context.Context, uuid.UUID, string, string) (bool, error) {
	panic("unused")
}

type copyFakeStorage struct{ copyErr error }

func (f *copyFakeStorage) CopyObject(context.Context, objecth.Location, objecth.Location) error {
	return f.copyErr
}
func (*copyFakeStorage) PresignPut(context.Context, objecth.PresignPutArgs) (string, map[string]string, time.Time, error) {
	panic("unused")
}
func (*copyFakeStorage) PresignPost(context.Context, objecth.PresignPostArgs) (string, map[string]string, time.Time, error) {
	panic("unused")
}
func (*copyFakeStorage) PresignGet(context.Context, objecth.PresignGetArgs) (string, map[string]string, time.Time, error) {
	panic("unused")
}
func (*copyFakeStorage) Head(context.Context, string, string, uuid.UUID, string, string, string) (string, int64, string, string, error) {
	panic("unused")
}
func (*copyFakeStorage) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	panic("unused")
}

type copyFakeTransitioner struct {
	markFailedErr error
	markFailedHit bool
	promoteHit    bool
}

func (f *copyFakeTransitioner) MarkFailed(context.Context, uuid.UUID, string) error {
	f.markFailedHit = true
	return f.markFailedErr
}
func (f *copyFakeTransitioner) PromoteToAvailable(context.Context, uuid.UUID, string, int64, string, string, statemachine.Source) (bool, error) {
	f.promoteHit = true
	return true, nil
}
func (*copyFakeTransitioner) SoftDelete(context.Context, uuid.UUID, int64) error { panic("unused") }
func (*copyFakeTransitioner) Restore(context.Context, uuid.UUID) error           { panic("unused") }

// ─── tests ───────────────────────────────────────────────────────────────

func availableSrc() objecth.Object {
	return objecth.Object{ObjectID: uuid.New(), State: statemachine.StateAvailable, Key: "k"}
}

// Storage copy fails → compensate by MarkFailed; the returned error names
// the storage failure and MarkFailed is invoked, promote is NOT.
func TestCopyOneCompensatesOnStorageFailure(t *testing.T) {
	tr := &copyFakeTransitioner{}
	e := &BatchCopyExecutor{
		Objects:     &copyFakeRepo{created: objecth.Object{ObjectID: uuid.New()}},
		Storage:     &copyFakeStorage{copyErr: errors.New("s3 down")},
		Transitions: tr,
	}
	err := e.copyOne(context.Background(), batchh.BatchCopyArgs{TenantID: uuid.New()}, availableSrc(), "", "src", "", "dst", time.Minute)
	if err == nil || !strings.Contains(err.Error(), "storage copy") {
		t.Fatalf("want storage copy error, got %v", err)
	}
	if !tr.markFailedHit {
		t.Error("compensation MarkFailed was not called")
	}
	if tr.promoteHit {
		t.Error("promote should not run after a failed copy")
	}
}

// Storage copy fails AND the compensating MarkFailed also fails → the
// error must surface BOTH so the operator sees the orphaned PENDING row.
func TestCopyOneDoubleFailureSurfacesBoth(t *testing.T) {
	tr := &copyFakeTransitioner{markFailedErr: errors.New("mark failed too")}
	e := &BatchCopyExecutor{
		Objects:     &copyFakeRepo{created: objecth.Object{ObjectID: uuid.New()}},
		Storage:     &copyFakeStorage{copyErr: errors.New("s3 down")},
		Transitions: tr,
	}
	err := e.copyOne(context.Background(), batchh.BatchCopyArgs{TenantID: uuid.New()}, availableSrc(), "", "src", "", "dst", time.Minute)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "s3 down") || !strings.Contains(err.Error(), "compensation also failed") {
		t.Errorf("error should name both failures, got %v", err)
	}
}

// A copy executor wired without a PENDING lifetime used to fall back to a
// hard-coded 15 minutes; it now refuses, because the value has exactly one
// source — limits.presign.put_ttl — and a silent default hid a missing wire.
func TestExecuteRefusesWithoutPendingTTL(t *testing.T) {
	e := &BatchCopyExecutor{
		Objects:     &copyFakeRepo{},
		Storage:     &copyFakeStorage{},
		Transitions: &copyFakeTransitioner{},
	}
	_, err := e.Execute(context.Background(), operationh.Operation{})
	if err == nil || !strings.Contains(err.Error(), "PendingTTL") {
		t.Fatalf("want PendingTTL error, got %v", err)
	}
}

package operations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// ─── fakes (only the methods copyOne touches do real work) ───────────────

type copyFakeRepo struct{ created object.Object }

func (f *copyFakeRepo) CreateObject(context.Context, object.CreateObjectArgs) (object.Object, error) {
	return f.created, nil
}

// unused-by-copyOne Repository methods:
func (*copyFakeRepo) FindByName(context.Context, uuid.UUID, string, string) (object.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) FindByIDs(context.Context, uuid.UUID, []uuid.UUID) ([]object.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) ObjectLock(context.Context, uuid.UUID, uuid.UUID) (object.ObjectLock, error) {
	panic("unused")
}
func (*copyFakeRepo) FindByPath(context.Context, uuid.UUID, string, string) (object.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) UpdateMetadata(context.Context, object.UpdateMetadataArgs) (object.Object, error) {
	panic("unused")
}
func (*copyFakeRepo) ListObjects(context.Context, object.ListObjectsArgs) ([]object.Object, string, error) {
	panic("unused")
}
func (*copyFakeRepo) CountObjects(context.Context, object.CountObjectsArgs) (int64, bool, error) {
	panic("unused")
}
func (*copyFakeRepo) LookupBucket(context.Context, uuid.UUID, string) (string, error) {
	panic("unused")
}
func (*copyFakeRepo) LookupBucketMeta(context.Context, uuid.UUID, string) (object.BucketMeta, error) {
	panic("unused")
}
func (*copyFakeRepo) HardDelete(context.Context, uuid.UUID, uuid.UUID, int64) error { panic("unused") }
func (*copyFakeRepo) HardDeleteWithBypass(context.Context, uuid.UUID, uuid.UUID, int64) error {
	panic("unused")
}
func (*copyFakeRepo) LiveCollision(context.Context, uuid.UUID, string, string) (bool, error) {
	panic("unused")
}

type copyFakeStorage struct{ copyErr error }

func (f *copyFakeStorage) CopyObject(context.Context, object.Location, object.Location) error {
	return f.copyErr
}
func (*copyFakeStorage) PresignPut(context.Context, object.PresignPutArgs) (string, map[string]string, time.Time, error) {
	panic("unused")
}
func (*copyFakeStorage) PresignPost(context.Context, object.PresignPostArgs) (string, map[string]string, time.Time, error) {
	panic("unused")
}
func (*copyFakeStorage) PresignGet(context.Context, object.PresignGetArgs) (string, map[string]string, time.Time, error) {
	panic("unused")
}
func (*copyFakeStorage) Head(context.Context, string, uuid.UUID, string, string) (string, int64, string, string, error) {
	panic("unused")
}
func (*copyFakeStorage) DeleteObject(context.Context, string, uuid.UUID, string, string) error {
	panic("unused")
}
func (*copyFakeStorage) CompletionMode(string) object.CompletionMode { panic("unused") }

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

func availableSrc() object.Object {
	return object.Object{ObjectID: uuid.New(), State: statemachine.StateAvailable, Key: "k"}
}

// Storage copy fails → compensate by MarkFailed; the returned error names
// the storage failure and MarkFailed is invoked, promote is NOT.
func TestCopyOneCompensatesOnStorageFailure(t *testing.T) {
	tr := &copyFakeTransitioner{}
	e := &BatchCopyExecutor{
		Objects:     &copyFakeRepo{created: object.Object{ObjectID: uuid.New()}},
		Storage:     &copyFakeStorage{copyErr: errors.New("s3 down")},
		Transitions: tr,
	}
	err := e.copyOne(context.Background(), batch.BatchCopyArgs{TenantID: uuid.New()}, availableSrc(), "src", "dst", time.Minute)
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
		Objects:     &copyFakeRepo{created: object.Object{ObjectID: uuid.New()}},
		Storage:     &copyFakeStorage{copyErr: errors.New("s3 down")},
		Transitions: tr,
	}
	err := e.copyOne(context.Background(), batch.BatchCopyArgs{TenantID: uuid.New()}, availableSrc(), "src", "dst", time.Minute)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "s3 down") || !strings.Contains(err.Error(), "compensation also failed") {
		t.Errorf("error should name both failures, got %v", err)
	}
}

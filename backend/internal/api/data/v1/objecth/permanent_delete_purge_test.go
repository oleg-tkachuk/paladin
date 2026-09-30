package objecth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	cedar "github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// Permanent delete removes the DB row first and the S3 bytes second. That
// ordering is deliberate — the reverse could delete a live object's bytes and
// then fail to remove its row — but it means a failed byte-delete leaves bytes
// with nothing pointing at them. The row is gone, the client's retry would get
// NotFound, and no table recorded where the bytes were.
//
// These tests cover the branch that let that survive: the happy path always
// worked, and the failure path only wrote a log line. Nothing asserted that a
// retry handle existed, so there was nothing to notice when it did not.
//
// Only the permanent branch is exercised here. The soft-delete branch returns
// well before the purge code and needs a real statemachine.Transitioner (and
// therefore a pool) to reach at all, so its "creates no debt" property is
// structural rather than something a unit test can add to.

// purgeRepo records the purge-debt calls the handler makes and lets a test
// drive the delete path without a database.
type purgeRepo struct {
	fakeObjectRepo
	obj Object

	enqueued []PurgeDebt
	settled  []uuid.UUID
}

func (r *purgeRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.obj, nil
}

func (r *purgeRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	return "backend-1", "bucket-1", nil
}

func (r *purgeRepo) ObjectLock(context.Context, uuid.UUID, uuid.UUID) (ObjectLock, error) {
	return ObjectLock{}, nil
}

func (r *purgeRepo) HardDeleteTx(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int64) error {
	return nil
}

// RunInTx runs fn with a nil tx — every repo call inside is a recorder here.
func (r *purgeRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}

func (r *purgeRepo) EnqueuePurgeTx(_ context.Context, _ pgx.Tx, p PurgeDebt) error {
	r.enqueued = append(r.enqueued, p)
	return nil
}

func (r *purgeRepo) SettlePurgeTx(_ context.Context, _ pgx.Tx, id uuid.UUID) error {
	r.settled = append(r.settled, id)
	return nil
}

// failingDeleteStorage is noopStorage with the one call under test broken.
type failingDeleteStorage struct {
	noopStorage
	err   error
	calls int
}

func (s *failingDeleteStorage) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	s.calls++
	return s.err
}

type allowAll struct{}

func (allowAll) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

func newPurgeHandler(t *testing.T, repo *purgeRepo, storage Storage) *Handler {
	t.Helper()
	return &Handler{
		repo:    repo,
		storage: storage,
		policy:  allowAll{},
		presign: PresignConfig{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour},
	}
}

func purgeCtx(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(),
		&auth.Principal{Subject: "u1", TenantID: tenantID})
}

// TestPermanentDeleteQueuesPurgeDebtWhenStorageFails is the regression test.
// The delete is committed — so it must still report success, or the client
// retries an operation that already happened — but the bytes must remain owed,
// not forgotten.
func TestPermanentDeleteQueuesPurgeDebtWhenStorageFails(t *testing.T) {
	tenantID := uuid.New()
	objectID := uuid.New()
	repo := &purgeRepo{obj: Object{ObjectID: objectID, TenantID: tenantID, Collection: "docs", Key: "a.txt"}}
	storage := &failingDeleteStorage{err: errors.New("backend unreachable")}
	h := newPurgeHandler(t, repo, storage)

	err := h.DeleteObject(purgeCtx(tenantID), "docs", objectID.String(), "", true, false)
	if err != nil {
		t.Fatalf("DeleteObject = %v; the row delete committed, so the call must "+
			"report success — a retry would get NotFound", err)
	}

	if len(repo.enqueued) != 1 {
		t.Fatalf("purge debt rows enqueued = %d, want 1 — without one the bytes "+
			"are unreachable: the object row is gone and nothing records where "+
			"they live", len(repo.enqueued))
	}
	d := repo.enqueued[0]
	if d.BackendID != "backend-1" || d.BucketName != "bucket-1" ||
		d.Collection != "docs" || d.Key != "a.txt" || d.TenantID != tenantID {
		t.Errorf("purge debt is missing routing needed to find the bytes: %+v", d)
	}
	if len(repo.settled) != 0 {
		t.Errorf("debt was settled despite the storage delete failing: %v — "+
			"settling here would discard the only retry handle", repo.settled)
	}
	if storage.calls != 1 {
		t.Errorf("storage delete attempted %d times, want 1", storage.calls)
	}
}

// TestPermanentDeleteSettlesDebtWhenStorageSucceeds pins the fast path: the
// common case stays synchronous and leaves no debt behind, so a non-empty
// pending_purges table is always a real backlog.
func TestPermanentDeleteSettlesDebtWhenStorageSucceeds(t *testing.T) {
	tenantID := uuid.New()
	repo := &purgeRepo{obj: Object{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "a.txt"}}
	h := newPurgeHandler(t, repo, &failingDeleteStorage{err: nil})

	if err := h.DeleteObject(purgeCtx(tenantID), "docs", repo.obj.ObjectID.String(), "", true, false); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	if len(repo.enqueued) != 1 {
		t.Fatalf("debt enqueued = %d, want 1 (written before the attempt, always)", len(repo.enqueued))
	}
	if len(repo.settled) != 1 {
		t.Fatalf("debt settled = %d, want 1 — a successful reclaim must clear it", len(repo.settled))
	}
	if repo.settled[0] != repo.enqueued[0].PurgeID {
		t.Errorf("settled %v but enqueued %v", repo.settled[0], repo.enqueued[0].PurgeID)
	}
}

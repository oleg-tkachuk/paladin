package objectkey

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EnsureCollection is idempotent: an already-present key is a no-op success
// (created=false) and no CreateTx is attempted.
func TestEnsureCollection_ExistingIsNoOp(t *testing.T) {
	tid := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000c1")
	fr := &fakeRepo{
		getFn: func(_ context.Context, _ uuid.UUID, key string) (Collection, error) {
			return fullKey(tid, key), nil // exists
		},
	}
	h := NewHandler(fr, allowAll())
	created, err := h.EnsureCollection(authedCtx(tid), CreateCollectionArgs{
		Collection: "docs", BackendID: "b", BucketId: "cab",
	})
	if err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if created {
		t.Error("created = true, want false (already existed)")
	}
	if fr.runInTxCalls != 0 {
		t.Error("must not open a create tx when the key already exists")
	}
}

// EnsureCollection creates a missing key under the CALLER's tenant, ignoring any
// tenant on the args (self-scoping).
func TestEnsureCollection_CreatesUnderCallerTenant(t *testing.T) {
	caller := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000c2")
	other := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000c3")
	fr := &fakeRepo{
		getFn: func(_ context.Context, _ uuid.UUID, _ string) (Collection, error) {
			return Collection{}, pgx.ErrNoRows // missing
		},
		createTxFn: func(_ context.Context, args CreateCollectionArgs) (Collection, error) {
			return fullKey(args.TenantID, args.Collection), nil
		},
	}
	h := NewHandler(fr, allowAll())
	created, err := h.EnsureCollection(authedCtx(caller), CreateCollectionArgs{
		TenantID:   other, // must be overridden with the caller's tenant
		Collection: "docs", BackendID: "b", BucketId: "cab",
	})
	if err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if fr.lastCreate.TenantID != caller {
		t.Errorf("create tenant = %s, want caller %s (self-scoped, request tenant ignored)",
			fr.lastCreate.TenantID, caller)
	}
}

// A missing backend/bucket binding is rejected before any write.
func TestEnsureCollection_RequiresBinding(t *testing.T) {
	tid := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000c4")
	h := NewHandler(&fakeRepo{}, allowAll())
	if _, err := h.EnsureCollection(authedCtx(tid), CreateCollectionArgs{Collection: "docs"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

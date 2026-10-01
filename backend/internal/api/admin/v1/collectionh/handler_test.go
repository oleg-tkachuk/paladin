package collectionh

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// fakeRepo is a configurable in-memory Repository. It records the args the
// handler forwards so tests can assert the tenant-stamping / arg-forwarding
// contract without a database. The plain (non-Tx) Create/Update/Delete methods
// exist only to satisfy the interface — the handler drives the *Tx variants
// through RunInTx.
type fakeRepo struct {
	createTxFn func(ctx context.Context, args CreateCollectionArgs) (Collection, error)
	getFn      func(ctx context.Context, tenantID uuid.UUID, collection string) (Collection, error)
	updateTxFn func(ctx context.Context, args UpdateCollectionArgs) (Collection, error)
	deleteTxFn func(ctx context.Context, tenantID uuid.UUID, collection string, expectedVersion int64) error
	listFn     func(ctx context.Context, args ListCollectionsArgs) ([]Collection, string, error)
	statsFn    func(ctx context.Context, tenantID uuid.UUID, collection string) (CollectionStats, error)
	rebindFn   func(ctx context.Context, tenantID uuid.UUID, collection, backendID, bucketName string, expectedVersion int64) error

	lastCreate   CreateCollectionArgs
	lastUpdate   UpdateCollectionArgs
	lastList     ListCollectionsArgs
	runInTxCalls int
	lastGet      struct {
		tenantID   uuid.UUID
		collection string
	}
	lastDelete struct {
		tenantID   uuid.UUID
		collection string
		version    int64
	}
	lastStats struct {
		tenantID   uuid.UUID
		collection string
	}
	lastRebind struct {
		tenantID   uuid.UUID
		collection string
		backendID  string
		bucketName string
		version    int64
	}
}

// RunInTx drives the caller's closure with a nil pgx.Tx. Safe because the
// handler's dispatchEventTx is a no-op when no EventProducer is set (tests
// never call SetEventProducer), so the nil tx is never touched.
func (f *fakeRepo) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	f.runInTxCalls++
	return fn(ctx, nil)
}

func (f *fakeRepo) CreateTx(ctx context.Context, _ pgx.Tx, args CreateCollectionArgs) (Collection, error) {
	f.lastCreate = args
	if f.createTxFn == nil {
		return Collection{}, nil
	}
	return f.createTxFn(ctx, args)
}

func (f *fakeRepo) UpdateTx(ctx context.Context, _ pgx.Tx, args UpdateCollectionArgs) (Collection, error) {
	f.lastUpdate = args
	if f.updateTxFn == nil {
		return Collection{}, nil
	}
	return f.updateTxFn(ctx, args)
}

func (f *fakeRepo) DeleteTx(ctx context.Context, _ pgx.Tx, tenantID uuid.UUID, collection string, expectedVersion int64) error {
	f.lastDelete.tenantID = tenantID
	f.lastDelete.collection = collection
	f.lastDelete.version = expectedVersion
	if f.deleteTxFn == nil {
		return nil
	}
	return f.deleteTxFn(ctx, tenantID, collection, expectedVersion)
}

func (f *fakeRepo) Get(ctx context.Context, tenantID uuid.UUID, collection string) (Collection, error) {
	f.lastGet.tenantID = tenantID
	f.lastGet.collection = collection
	if f.getFn == nil {
		return Collection{}, nil
	}
	return f.getFn(ctx, tenantID, collection)
}

func (f *fakeRepo) List(ctx context.Context, args ListCollectionsArgs) ([]Collection, string, error) {
	f.lastList = args
	if f.listFn == nil {
		return nil, "", nil
	}
	return f.listFn(ctx, args)
}

func (f *fakeRepo) Stats(ctx context.Context, tenantID uuid.UUID, collection string) (CollectionStats, error) {
	f.lastStats.tenantID = tenantID
	f.lastStats.collection = collection
	if f.statsFn == nil {
		return CollectionStats{}, nil
	}
	return f.statsFn(ctx, tenantID, collection)
}

func (f *fakeRepo) Rebind(ctx context.Context, tenantID uuid.UUID, collection, backendID, bucketName string, expectedVersion int64) error {
	f.lastRebind.tenantID = tenantID
	f.lastRebind.collection = collection
	f.lastRebind.backendID = backendID
	f.lastRebind.bucketName = bucketName
	f.lastRebind.version = expectedVersion
	if f.rebindFn == nil {
		return nil
	}
	return f.rebindFn(ctx, tenantID, collection, backendID, bucketName, expectedVersion)
}

// Non-Tx variants — unused by the handler, present only for the interface.
func (f *fakeRepo) Create(ctx context.Context, args CreateCollectionArgs) (Collection, error) {
	return f.CreateTx(ctx, nil, args)
}

func (f *fakeRepo) Update(ctx context.Context, args UpdateCollectionArgs) (Collection, error) {
	return f.UpdateTx(ctx, nil, args)
}

func (f *fakeRepo) Delete(ctx context.Context, tenantID uuid.UUID, collection string, expectedVersion int64) error {
	return f.DeleteTx(ctx, nil, tenantID, collection, expectedVersion)
}

// fakeAuthorizer is a recording cedar.Authorizer. Its zero value DENIES
// (DecisionDeny is the iota zero), so a bare &fakeAuthorizer{} exercises the
// PermissionDenied branch; allowAll() flips it to allow.
type fakeAuthorizer struct {
	decision cedar.Decision
	err      error

	calls         int
	lastAction    string
	lastResource  cedar.Resource
	lastPrincipal cedar.Principal
}

func (f *fakeAuthorizer) IsAuthorized(_ context.Context, p *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.calls++
	f.lastAction = action
	if p != nil {
		f.lastPrincipal = *p
	}
	if r != nil {
		f.lastResource = *r
	}
	return f.decision, f.err
}

func allowAll() *fakeAuthorizer { return &fakeAuthorizer{decision: cedar.DecisionAllow} }

// fakeEvents records the last event dispatched on the caller's tx so tests can
// assert the ADR-0003 outbox contract (canonical resource name, stamped
// tenant, actor subject from the principal).
type fakeEvents struct {
	txCalls      int
	lastTx       worker.Event
	lastTxTenant string
	err          error
}

func (f *fakeEvents) Dispatch(_ context.Context, _ string, _ worker.Event) (int, error) {
	return 0, nil
}

func (f *fakeEvents) DispatchTx(_ context.Context, _ pgx.Tx, tenantID string, evt worker.Event) (int, error) {
	f.txCalls++
	f.lastTxTenant = tenantID
	f.lastTx = evt
	return 1, f.err
}

// --- context helpers (mirrors object_tag/handler_test.go) ---

func authedCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tid})
}

func adminCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject:  "admin1",
		TenantID: tid,
		Roles:    []string{apiutil.RolePlatformAdmin},
	})
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("error code: got %v, want %v (err=%v)", got, want, err)
	}
}

// fullKey builds a populated Collection — every component non-empty so the
// handler's CanonicalName() calls don't panic on the happy paths.
func fullKey(tid uuid.UUID, key string) Collection {
	return Collection{
		TenantID:   tid,
		Collection: key,
		BackendID:  "aws-eu",
		BucketName: "paladin-prod",
	}
}

func TestCreateCollection(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.CreateCollection(context.Background(), CreateCollectionArgs{Collection: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("cross-tenant without platform.admin → permission denied", func(t *testing.T) {
		// allowAll authorizer isolates the role-gate: the denial must come
		// from the cross-tenant check, not Cedar.
		h := NewHandler(&fakeRepo{}, allowAll())
		other := uuid.New()
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{
			TenantID: other, Collection: "k", BackendID: "aws-eu",
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	// The repository classifies a duplicate name as ErrCollectionExists. This
	// is the half that decides whether the caller ever sees it: the handler
	// used to wrap every failure of the create transaction in a hardcoded
	// CodeInternal, which threw the classification away and returned a 500
	// carrying `duplicate key value violates unique constraint
	// "collections_tenant_id_name_key" (SQLSTATE 23505)`.
	//
	// Worth its own test because the adapter-level one cannot see it: that
	// test asserts the sentinel comes out of the repository, and passes just
	// as happily while the handler discards it on the way to the client.
	t.Run("duplicate name → already exists", func(t *testing.T) {
		h := NewHandler(&fakeRepo{
			createTxFn: func(context.Context, CreateCollectionArgs) (Collection, error) {
				return Collection{}, fmt.Errorf("create collection: %w", ErrCollectionExists)
			},
		}, allowAll())
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{
			TenantID: tid, Collection: "k", BackendID: "aws-eu", BucketName: "b",
		})
		wantCode(t, err, connect.CodeAlreadyExists)
	})

	// A bucket that is not registered, or was deleted after the caller listed
	// it, is a missing resource. It reached the caller as a 500 carrying a
	// NOT NULL violation on collections.bucket_id.
	t.Run("unknown bucket → not found", func(t *testing.T) {
		h := NewHandler(&fakeRepo{
			createTxFn: func(context.Context, CreateCollectionArgs) (Collection, error) {
				return Collection{}, fmt.Errorf("%w: aws-eu/gone", ErrBucketNotFound)
			},
		}, allowAll())
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{
			TenantID: tid, Collection: "k", BackendID: "aws-eu", BucketName: "gone",
		})
		wantCode(t, err, connect.CodeNotFound)
	})

	// An error the registry does not know still has to be a 500 — MapError's
	// fallback, not something the change above quietly widened.
	t.Run("unclassified failure → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{
			createTxFn: func(context.Context, CreateCollectionArgs) (Collection, error) {
				return Collection{}, errors.New("connection reset by peer")
			},
		}, allowAll())
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{
			TenantID: tid, Collection: "k", BackendID: "aws-eu", BucketName: "b",
		})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("missing backend_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy engine error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{err: errors.New("engine down")})
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(context.Context, CreateCollectionArgs) (Collection, error) {
			return Collection{}, errors.New("boom")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok stamps caller tenant + forwards to Cedar", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
			return fullKey(a.TenantID, a.Collection), nil
		}}
		authz := allowAll()
		h := NewHandler(fr, authz)
		// TenantID left Nil → handler stamps caller tenant.
		got, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "assets", BackendID: "aws-eu"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreate.TenantID != tid {
			t.Fatalf("tenant not stamped from ctx: got %v want %v", fr.lastCreate.TenantID, tid)
		}
		if got.Collection != "assets" {
			t.Fatalf("returned collection: got %q want assets", got.Collection)
		}
		if authz.lastAction != cedar.ActionManageCollection {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionManageCollection)
		}
		if authz.lastResource.BackendID != "aws-eu" {
			t.Fatalf("cedar resource backend: got %q want aws-eu", authz.lastResource.BackendID)
		}
		if fr.runInTxCalls != 1 {
			t.Fatalf("RunInTx calls: got %d want 1", fr.runInTxCalls)
		}
	})

	t.Run("platform.admin cross-tenant override honored", func(t *testing.T) {
		other := uuid.New()
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
			return fullKey(a.TenantID, a.Collection), nil
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.CreateCollection(adminCtx(tid), CreateCollectionArgs{
			TenantID: other, Collection: "k", BackendID: "aws-eu",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreate.TenantID != other {
			t.Fatalf("admin override not honored: got %v want %v", fr.lastCreate.TenantID, other)
		}
	})
}

func TestGetCollection(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.GetCollection(context.Background(), uuid.Nil, "k")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("cross-tenant without platform.admin → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.GetCollection(authedCtx(tid), uuid.New(), "k")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.GetCollection(authedCtx(tid), uuid.Nil, "k")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo error → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID, string) (Collection, error) {
			return Collection{}, errors.New("missing")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.GetCollection(authedCtx(tid), uuid.Nil, "k")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok defaults tenant to caller + forwards key", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
			return fullKey(tenant, key), nil
		}}
		h := NewHandler(fr, allowAll())
		got, err := h.GetCollection(authedCtx(tid), uuid.Nil, "assets")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastGet.tenantID != tid || fr.lastGet.collection != "assets" {
			t.Fatalf("forwarded (%v,%q), want (%v,assets)", fr.lastGet.tenantID, fr.lastGet.collection, tid)
		}
		if got.Collection != "assets" {
			t.Fatalf("returned collection: got %q", got.Collection)
		}
	})

	t.Run("platform.admin reads other tenant", func(t *testing.T) {
		other := uuid.New()
		fr := &fakeRepo{getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
			return fullKey(tenant, key), nil
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.GetCollection(adminCtx(tid), other, "k")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastGet.tenantID != other {
			t.Fatalf("admin target tenant: got %v want %v", fr.lastGet.tenantID, other)
		}
	})
}

func TestUpdateCollection(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.UpdateCollection(context.Background(), UpdateCollectionArgs{Collection: "k"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.UpdateCollection(authedCtx(tid), UpdateCollectionArgs{Collection: "k"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{updateTxFn: func(context.Context, UpdateCollectionArgs) (Collection, error) {
			return Collection{}, ErrVersionMismatch
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.UpdateCollection(authedCtx(tid), UpdateCollectionArgs{Collection: "k", ExpectedVersion: 3})
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok stamps tenant + forwards expected version", func(t *testing.T) {
		fr := &fakeRepo{updateTxFn: func(_ context.Context, a UpdateCollectionArgs) (Collection, error) {
			return fullKey(a.TenantID, a.Collection), nil
		}}
		h := NewHandler(fr, allowAll())
		got, err := h.UpdateCollection(authedCtx(tid), UpdateCollectionArgs{Collection: "assets", ExpectedVersion: 7})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastUpdate.TenantID != tid {
			t.Fatalf("tenant not stamped: got %v want %v", fr.lastUpdate.TenantID, tid)
		}
		if fr.lastUpdate.ExpectedVersion != 7 {
			t.Fatalf("expected version: got %d want 7", fr.lastUpdate.ExpectedVersion)
		}
		if got.Collection != "assets" {
			t.Fatalf("returned collection: got %q", got.Collection)
		}
	})
}

func TestDeleteCollection(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		err := h.DeleteCollection(context.Background(), uuid.Nil, "k", 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		err := h.DeleteCollection(authedCtx(tid), uuid.Nil, "k", 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
				return fullKey(tenant, key), nil
			},
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return ErrVersionMismatch },
		}
		h := NewHandler(fr, allowAll())
		err := h.DeleteCollection(authedCtx(tid), uuid.Nil, "k", 2)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok forwards tenant/key/version (canonical pre-read)", func(t *testing.T) {
		fr := &fakeRepo{
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
				return fullKey(tenant, key), nil
			},
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return nil },
		}
		h := NewHandler(fr, allowAll())
		if err := h.DeleteCollection(authedCtx(tid), uuid.Nil, "assets", 5); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastDelete.tenantID != tid || fr.lastDelete.collection != "assets" || fr.lastDelete.version != 5 {
			t.Fatalf("forwarded %+v", fr.lastDelete)
		}
	})

	t.Run("ok when pre-read fails (falls back to C-shape name)", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:      func(context.Context, uuid.UUID, string) (Collection, error) { return Collection{}, errors.New("gone") },
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return nil },
		}
		h := NewHandler(fr, allowAll())
		if err := h.DeleteCollection(authedCtx(tid), uuid.Nil, "assets", 1); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastDelete.collection != "assets" {
			t.Fatalf("delete still forwards key: got %q", fr.lastDelete.collection)
		}
	})
}

func TestListCollections(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListCollections(context.Background(), ListCollectionsArgs{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("cross-tenant bucket list without platform.admin → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListCollections(authedCtx(tid), ListCollectionsArgs{
			BackendID: "aws-eu", BucketName: "paladin-prod", // TenantID Nil + full filter
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("explicit foreign tenant without platform.admin → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListCollections(authedCtx(tid), ListCollectionsArgs{TenantID: uuid.New()})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, _, err := h.ListCollections(authedCtx(tid), ListCollectionsArgs{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok pins caller tenant + passes through result", func(t *testing.T) {
		fr := &fakeRepo{listFn: func(_ context.Context, args ListCollectionsArgs) ([]Collection, string, error) {
			return []Collection{fullKey(args.TenantID, "a")}, "next", nil
		}}
		h := NewHandler(fr, allowAll())
		keys, next, err := h.ListCollections(authedCtx(tid), ListCollectionsArgs{PageSize: 25, PageToken: "cursor"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastList.TenantID != tid {
			t.Fatalf("tenant not pinned: got %v want %v", fr.lastList.TenantID, tid)
		}
		if len(keys) != 1 || keys[0].Collection != "a" || next != "next" {
			t.Fatalf("got keys=%v next=%q", keys, next)
		}
	})

	t.Run("platform.admin cross-tenant bucket list allowed", func(t *testing.T) {
		fr := &fakeRepo{listFn: func(_ context.Context, args ListCollectionsArgs) ([]Collection, string, error) {
			return nil, "", nil
		}}
		h := NewHandler(fr, allowAll())
		_, _, err := h.ListCollections(adminCtx(tid), ListCollectionsArgs{BackendID: "aws-eu", BucketName: "paladin-prod"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastList.BackendID != "aws-eu" || fr.lastList.BucketName != "paladin-prod" {
			t.Fatalf("filter not forwarded: %+v", fr.lastList)
		}
	})
}

func TestGetCollectionStats(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.GetCollectionStats(context.Background(), "k")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.GetCollectionStats(authedCtx(tid), "k")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		fr := &fakeRepo{statsFn: func(context.Context, uuid.UUID, string) (CollectionStats, error) {
			return CollectionStats{}, errors.New("boom")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.GetCollectionStats(authedCtx(tid), "k")
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok forwards tenant/key + returns stats", func(t *testing.T) {
		fr := &fakeRepo{statsFn: func(_ context.Context, tenant uuid.UUID, key string) (CollectionStats, error) {
			return CollectionStats{ObjectCountAvailable: 42, SizeBytesAvailable: 1024}, nil
		}}
		h := NewHandler(fr, allowAll())
		got, err := h.GetCollectionStats(authedCtx(tid), "assets")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastStats.tenantID != tid || fr.lastStats.collection != "assets" {
			t.Fatalf("forwarded (%v,%q)", fr.lastStats.tenantID, fr.lastStats.collection)
		}
		if got.ObjectCountAvailable != 42 || got.SizeBytesAvailable != 1024 {
			t.Fatalf("stats passthrough: got %+v", *got)
		}
	})
}

func TestBindCollectionToBucket(t *testing.T) {
	tid := uuid.New()
	const validBucket = "storageBackends/aws-eu/buckets/paladin-prod"

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.BindCollectionToBucket(context.Background(), uuid.Nil, "k", validBucket, 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("malformed bucket name → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil, "k", "buckets/only", 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil, "k", validBucket, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("rebind version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error {
			return ErrVersionMismatch
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil, "k", validBucket, 4)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("rebind other error → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error {
			return errors.New("bucket tenancy violation")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil, "k", validBucket, 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("rebind ok but re-read fails → internal", func(t *testing.T) {
		fr := &fakeRepo{
			rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error { return nil },
			getFn:    func(context.Context, uuid.UUID, string) (Collection, error) { return Collection{}, errors.New("gone") },
		}
		h := NewHandler(fr, allowAll())
		_, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil, "k", validBucket, 0)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok forwards parsed backend+bucket + returns re-read row", func(t *testing.T) {
		fr := &fakeRepo{
			rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error { return nil },
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
				return fullKey(tenant, key), nil
			},
		}
		authz := allowAll()
		h := NewHandler(fr, authz)
		got, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil, "assets", validBucket, 9)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastRebind.backendID != "aws-eu" || fr.lastRebind.bucketName != "paladin-prod" {
			t.Fatalf("rebind target: got backend=%q bucket=%q", fr.lastRebind.backendID, fr.lastRebind.bucketName)
		}
		if fr.lastRebind.tenantID != tid || fr.lastRebind.collection != "assets" || fr.lastRebind.version != 9 {
			t.Fatalf("rebind forwarded %+v", fr.lastRebind)
		}
		if authz.lastAction != cedar.ActionBindCollectionToBucket {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionBindCollectionToBucket)
		}
		if got.Collection != "assets" {
			t.Fatalf("returned collection: got %q", got.Collection)
		}
	})
}

// TestEventDispatch exercises the ADR-0003 outbox seam: when an EventProducer
// is wired via SetEventProducer, a successful Create fans out a canonical
// event on the caller's tx, and a producer error rolls the whole op back.
func TestEventDispatch(t *testing.T) {
	tid := uuid.New()

	t.Run("create fans out canonical event with stamped tenant + actor", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
			return fullKey(a.TenantID, a.Collection), nil
		}}
		ev := &fakeEvents{}
		h := NewHandler(fr, allowAll())
		h.SetEventProducer(ev)
		h.SetLogger(nil) // nil-safe guard: must not replace the default logger

		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "assets", BackendID: "aws-eu"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ev.txCalls != 1 {
			t.Fatalf("DispatchTx calls: got %d want 1", ev.txCalls)
		}
		if ev.lastTx.Type != "paladin.collection.created" {
			t.Fatalf("event type: got %q", ev.lastTx.Type)
		}
		if ev.lastTxTenant != tid.String() || ev.lastTx.TenantID != tid.String() {
			t.Fatalf("event tenant: got tx=%q evt=%q want %s", ev.lastTxTenant, ev.lastTx.TenantID, tid)
		}
		if ev.lastTx.ActorSubject != "u1" {
			t.Fatalf("actor subject: got %q want u1", ev.lastTx.ActorSubject)
		}
		wantName := CanonicalName("aws-eu", "paladin-prod", tid, "assets")
		if ev.lastTx.ResourceName != wantName {
			t.Fatalf("resource name: got %q want %q", ev.lastTx.ResourceName, wantName)
		}
		if ev.lastTx.Payload["collection"] != "assets" {
			t.Fatalf("payload collection: got %v", ev.lastTx.Payload["collection"])
		}
	})

	t.Run("producer error rolls the op back → internal", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
			return fullKey(a.TenantID, a.Collection), nil
		}}
		ev := &fakeEvents{err: errors.New("outbox down")}
		h := NewHandler(fr, allowAll())
		h.SetEventProducer(ev)

		_, err := h.CreateCollection(authedCtx(tid), CreateCollectionArgs{Collection: "assets", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeInternal)
	})
}

// A C-shape name says which tenant's collection it is. Delete used to ignore
// that and operate on the caller's own tenant: for a platform admin naming
// another tenant's collection, the call addressed a row that exists and acted
// on a different one — or, with nothing by that name in the caller's tenant,
// reported a version mismatch, which says the row moved rather than that it
// was never looked at. The e2e fixture teardown found it that way.
func TestDeleteCollectionHonoursTheTenantInTheName(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()

	t.Run("platform admin deletes the named tenant's collection", func(t *testing.T) {
		var sawTenant uuid.UUID
		fr := &fakeRepo{
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
				return fullKey(tenant, key), nil
			},
			deleteTxFn: func(_ context.Context, tenant uuid.UUID, _ string, _ int64) error {
				sawTenant = tenant
				return nil
			},
		}
		h := NewHandler(fr, allowAll())
		if err := h.DeleteCollection(adminCtx(caller), other, "assets", 1); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if sawTenant != other {
			t.Errorf("deleted in tenant %s, want the one named in the request (%s)",
				sawTenant, other)
		}
	})

	t.Run("a tenant admin cannot reach across", func(t *testing.T) {
		fr := &fakeRepo{
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error {
				t.Error("delete ran for a caller with no cross-tenant right")
				return nil
			},
		}
		h := NewHandler(fr, allowAll())
		err := h.DeleteCollection(authedCtx(caller), other, "assets", 1)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("naming your own tenant is not a cross-tenant call", func(t *testing.T) {
		var sawTenant uuid.UUID
		fr := &fakeRepo{
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
				return fullKey(tenant, key), nil
			},
			deleteTxFn: func(_ context.Context, tenant uuid.UUID, _ string, _ int64) error {
				sawTenant = tenant
				return nil
			},
		}
		h := NewHandler(fr, allowAll())
		if err := h.DeleteCollection(authedCtx(caller), caller, "assets", 1); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if sawTenant != caller {
			t.Errorf("deleted in tenant %s, want %s", sawTenant, caller)
		}
	})
}

package objectkey

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// fakeRepo is a configurable in-memory Repository. It records the args the
// handler forwards so tests can assert the tenant-stamping / arg-forwarding
// contract without a database. The plain (non-Tx) Create/Update/Delete methods
// exist only to satisfy the interface — the handler drives the *Tx variants
// through RunInTx.
type fakeRepo struct {
	createTxFn func(ctx context.Context, args CreateObjectKeyArgs) (ObjectKey, error)
	getFn      func(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKey, error)
	updateTxFn func(ctx context.Context, args UpdateObjectKeyArgs) (ObjectKey, error)
	deleteTxFn func(ctx context.Context, tenantID uuid.UUID, objectKey string, expectedVersion int64) error
	listFn     func(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error)
	statsFn    func(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKeyStats, error)
	rebindFn   func(ctx context.Context, tenantID uuid.UUID, objectKey, backendID, bucketName string, expectedVersion int64) error

	lastCreate   CreateObjectKeyArgs
	lastUpdate   UpdateObjectKeyArgs
	lastList     ListObjectKeysArgs
	runInTxCalls int
	lastGet      struct {
		tenantID  uuid.UUID
		objectKey string
	}
	lastDelete struct {
		tenantID  uuid.UUID
		objectKey string
		version   int64
	}
	lastStats struct {
		tenantID  uuid.UUID
		objectKey string
	}
	lastRebind struct {
		tenantID   uuid.UUID
		objectKey  string
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

func (f *fakeRepo) CreateTx(ctx context.Context, _ pgx.Tx, args CreateObjectKeyArgs) (ObjectKey, error) {
	f.lastCreate = args
	if f.createTxFn == nil {
		return ObjectKey{}, nil
	}
	return f.createTxFn(ctx, args)
}

func (f *fakeRepo) UpdateTx(ctx context.Context, _ pgx.Tx, args UpdateObjectKeyArgs) (ObjectKey, error) {
	f.lastUpdate = args
	if f.updateTxFn == nil {
		return ObjectKey{}, nil
	}
	return f.updateTxFn(ctx, args)
}

func (f *fakeRepo) DeleteTx(ctx context.Context, _ pgx.Tx, tenantID uuid.UUID, objectKey string, expectedVersion int64) error {
	f.lastDelete.tenantID = tenantID
	f.lastDelete.objectKey = objectKey
	f.lastDelete.version = expectedVersion
	if f.deleteTxFn == nil {
		return nil
	}
	return f.deleteTxFn(ctx, tenantID, objectKey, expectedVersion)
}

func (f *fakeRepo) Get(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKey, error) {
	f.lastGet.tenantID = tenantID
	f.lastGet.objectKey = objectKey
	if f.getFn == nil {
		return ObjectKey{}, nil
	}
	return f.getFn(ctx, tenantID, objectKey)
}

func (f *fakeRepo) List(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error) {
	f.lastList = args
	if f.listFn == nil {
		return nil, "", nil
	}
	return f.listFn(ctx, args)
}

func (f *fakeRepo) Stats(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKeyStats, error) {
	f.lastStats.tenantID = tenantID
	f.lastStats.objectKey = objectKey
	if f.statsFn == nil {
		return ObjectKeyStats{}, nil
	}
	return f.statsFn(ctx, tenantID, objectKey)
}

func (f *fakeRepo) Rebind(ctx context.Context, tenantID uuid.UUID, objectKey, backendID, bucketName string, expectedVersion int64) error {
	f.lastRebind.tenantID = tenantID
	f.lastRebind.objectKey = objectKey
	f.lastRebind.backendID = backendID
	f.lastRebind.bucketName = bucketName
	f.lastRebind.version = expectedVersion
	if f.rebindFn == nil {
		return nil
	}
	return f.rebindFn(ctx, tenantID, objectKey, backendID, bucketName, expectedVersion)
}

// Non-Tx variants — unused by the handler, present only for the interface.
func (f *fakeRepo) Create(ctx context.Context, args CreateObjectKeyArgs) (ObjectKey, error) {
	return f.CreateTx(ctx, nil, args)
}

func (f *fakeRepo) Update(ctx context.Context, args UpdateObjectKeyArgs) (ObjectKey, error) {
	return f.UpdateTx(ctx, nil, args)
}

func (f *fakeRepo) Delete(ctx context.Context, tenantID uuid.UUID, objectKey string, expectedVersion int64) error {
	return f.DeleteTx(ctx, nil, tenantID, objectKey, expectedVersion)
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

// fullKey builds a populated ObjectKey — every component non-empty so the
// handler's CanonicalName() calls don't panic on the happy paths.
func fullKey(tid uuid.UUID, key string) ObjectKey {
	return ObjectKey{
		TenantID:   tid,
		ObjectKey:  key,
		BackendID:  "aws-eu",
		BucketName: "paladin-prod",
	}
}

func TestCreateObjectKey(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.CreateObjectKey(context.Background(), CreateObjectKeyArgs{ObjectKey: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("cross-tenant without platform.admin → permission denied", func(t *testing.T) {
		// allowAll authorizer isolates the role-gate: the denial must come
		// from the cross-tenant check, not Cedar.
		h := NewHandler(&fakeRepo{}, allowAll())
		other := uuid.New()
		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{
			TenantID: other, ObjectKey: "k", BackendID: "aws-eu",
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("missing backend_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "k"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy engine error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{err: errors.New("engine down")})
		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(context.Context, CreateObjectKeyArgs) (ObjectKey, error) {
			return ObjectKey{}, errors.New("boom")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "k", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok stamps caller tenant + forwards to Cedar", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateObjectKeyArgs) (ObjectKey, error) {
			return fullKey(a.TenantID, a.ObjectKey), nil
		}}
		authz := allowAll()
		h := NewHandler(fr, authz)
		// TenantID left Nil → handler stamps caller tenant.
		got, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "assets", BackendID: "aws-eu"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreate.TenantID != tid {
			t.Fatalf("tenant not stamped from ctx: got %v want %v", fr.lastCreate.TenantID, tid)
		}
		if got.ObjectKey != "assets" {
			t.Fatalf("returned object_key: got %q want assets", got.ObjectKey)
		}
		if authz.lastAction != cedar.ActionManageObjectKey {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionManageObjectKey)
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
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateObjectKeyArgs) (ObjectKey, error) {
			return fullKey(a.TenantID, a.ObjectKey), nil
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.CreateObjectKey(adminCtx(tid), CreateObjectKeyArgs{
			TenantID: other, ObjectKey: "k", BackendID: "aws-eu",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreate.TenantID != other {
			t.Fatalf("admin override not honored: got %v want %v", fr.lastCreate.TenantID, other)
		}
	})
}

func TestGetObjectKey(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.GetObjectKey(context.Background(), uuid.Nil, "k")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("cross-tenant without platform.admin → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.GetObjectKey(authedCtx(tid), uuid.New(), "k")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.GetObjectKey(authedCtx(tid), uuid.Nil, "k")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo error → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID, string) (ObjectKey, error) {
			return ObjectKey{}, errors.New("missing")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.GetObjectKey(authedCtx(tid), uuid.Nil, "k")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok defaults tenant to caller + forwards key", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, tenant uuid.UUID, key string) (ObjectKey, error) {
			return fullKey(tenant, key), nil
		}}
		h := NewHandler(fr, allowAll())
		got, err := h.GetObjectKey(authedCtx(tid), uuid.Nil, "assets")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastGet.tenantID != tid || fr.lastGet.objectKey != "assets" {
			t.Fatalf("forwarded (%v,%q), want (%v,assets)", fr.lastGet.tenantID, fr.lastGet.objectKey, tid)
		}
		if got.ObjectKey != "assets" {
			t.Fatalf("returned object_key: got %q", got.ObjectKey)
		}
	})

	t.Run("platform.admin reads other tenant", func(t *testing.T) {
		other := uuid.New()
		fr := &fakeRepo{getFn: func(_ context.Context, tenant uuid.UUID, key string) (ObjectKey, error) {
			return fullKey(tenant, key), nil
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.GetObjectKey(adminCtx(tid), other, "k")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastGet.tenantID != other {
			t.Fatalf("admin target tenant: got %v want %v", fr.lastGet.tenantID, other)
		}
	})
}

func TestUpdateObjectKey(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.UpdateObjectKey(context.Background(), UpdateObjectKeyArgs{ObjectKey: "k"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.UpdateObjectKey(authedCtx(tid), UpdateObjectKeyArgs{ObjectKey: "k"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{updateTxFn: func(context.Context, UpdateObjectKeyArgs) (ObjectKey, error) {
			return ObjectKey{}, ErrVersionMismatch
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.UpdateObjectKey(authedCtx(tid), UpdateObjectKeyArgs{ObjectKey: "k", ExpectedVersion: 3})
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok stamps tenant + forwards expected version", func(t *testing.T) {
		fr := &fakeRepo{updateTxFn: func(_ context.Context, a UpdateObjectKeyArgs) (ObjectKey, error) {
			return fullKey(a.TenantID, a.ObjectKey), nil
		}}
		h := NewHandler(fr, allowAll())
		got, err := h.UpdateObjectKey(authedCtx(tid), UpdateObjectKeyArgs{ObjectKey: "assets", ExpectedVersion: 7})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastUpdate.TenantID != tid {
			t.Fatalf("tenant not stamped: got %v want %v", fr.lastUpdate.TenantID, tid)
		}
		if fr.lastUpdate.ExpectedVersion != 7 {
			t.Fatalf("expected version: got %d want 7", fr.lastUpdate.ExpectedVersion)
		}
		if got.ObjectKey != "assets" {
			t.Fatalf("returned object_key: got %q", got.ObjectKey)
		}
	})
}

func TestDeleteObjectKey(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		err := h.DeleteObjectKey(context.Background(), "k", 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		err := h.DeleteObjectKey(authedCtx(tid), "k", 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (ObjectKey, error) {
				return fullKey(tenant, key), nil
			},
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return ErrVersionMismatch },
		}
		h := NewHandler(fr, allowAll())
		err := h.DeleteObjectKey(authedCtx(tid), "k", 2)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok forwards tenant/key/version (canonical pre-read)", func(t *testing.T) {
		fr := &fakeRepo{
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (ObjectKey, error) {
				return fullKey(tenant, key), nil
			},
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return nil },
		}
		h := NewHandler(fr, allowAll())
		if err := h.DeleteObjectKey(authedCtx(tid), "assets", 5); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastDelete.tenantID != tid || fr.lastDelete.objectKey != "assets" || fr.lastDelete.version != 5 {
			t.Fatalf("forwarded %+v", fr.lastDelete)
		}
	})

	t.Run("ok when pre-read fails (falls back to C-shape name)", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:      func(context.Context, uuid.UUID, string) (ObjectKey, error) { return ObjectKey{}, errors.New("gone") },
			deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return nil },
		}
		h := NewHandler(fr, allowAll())
		if err := h.DeleteObjectKey(authedCtx(tid), "assets", 1); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastDelete.objectKey != "assets" {
			t.Fatalf("delete still forwards key: got %q", fr.lastDelete.objectKey)
		}
	})
}

func TestListObjectKeys(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListObjectKeys(context.Background(), ListObjectKeysArgs{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("cross-tenant bucket list without platform.admin → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListObjectKeys(authedCtx(tid), ListObjectKeysArgs{
			BackendID: "aws-eu", BucketName: "paladin-prod", // TenantID Nil + full filter
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("explicit foreign tenant without platform.admin → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, _, err := h.ListObjectKeys(authedCtx(tid), ListObjectKeysArgs{TenantID: uuid.New()})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, _, err := h.ListObjectKeys(authedCtx(tid), ListObjectKeysArgs{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok pins caller tenant + passes through result", func(t *testing.T) {
		fr := &fakeRepo{listFn: func(_ context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error) {
			return []ObjectKey{fullKey(args.TenantID, "a")}, "next", nil
		}}
		h := NewHandler(fr, allowAll())
		keys, next, err := h.ListObjectKeys(authedCtx(tid), ListObjectKeysArgs{PageSize: 25, PageToken: "cursor"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastList.TenantID != tid {
			t.Fatalf("tenant not pinned: got %v want %v", fr.lastList.TenantID, tid)
		}
		if len(keys) != 1 || keys[0].ObjectKey != "a" || next != "next" {
			t.Fatalf("got keys=%v next=%q", keys, next)
		}
	})

	t.Run("platform.admin cross-tenant bucket list allowed", func(t *testing.T) {
		fr := &fakeRepo{listFn: func(_ context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error) {
			return nil, "", nil
		}}
		h := NewHandler(fr, allowAll())
		_, _, err := h.ListObjectKeys(adminCtx(tid), ListObjectKeysArgs{BackendID: "aws-eu", BucketName: "paladin-prod"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastList.BackendID != "aws-eu" || fr.lastList.BucketName != "paladin-prod" {
			t.Fatalf("filter not forwarded: %+v", fr.lastList)
		}
	})
}

func TestGetObjectKeyStats(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.GetObjectKeyStats(context.Background(), "k")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.GetObjectKeyStats(authedCtx(tid), "k")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		fr := &fakeRepo{statsFn: func(context.Context, uuid.UUID, string) (ObjectKeyStats, error) {
			return ObjectKeyStats{}, errors.New("boom")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.GetObjectKeyStats(authedCtx(tid), "k")
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok forwards tenant/key + returns stats", func(t *testing.T) {
		fr := &fakeRepo{statsFn: func(_ context.Context, tenant uuid.UUID, key string) (ObjectKeyStats, error) {
			return ObjectKeyStats{ObjectCountAvailable: 42, SizeBytesAvailable: 1024}, nil
		}}
		h := NewHandler(fr, allowAll())
		got, err := h.GetObjectKeyStats(authedCtx(tid), "assets")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastStats.tenantID != tid || fr.lastStats.objectKey != "assets" {
			t.Fatalf("forwarded (%v,%q)", fr.lastStats.tenantID, fr.lastStats.objectKey)
		}
		if got.ObjectCountAvailable != 42 || got.SizeBytesAvailable != 1024 {
			t.Fatalf("stats passthrough: got %+v", *got)
		}
	})
}

func TestBindObjectKeyToBucket(t *testing.T) {
	tid := uuid.New()
	const validBucket = "storageBackends/aws-eu/buckets/paladin-prod"

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.BindObjectKeyToBucket(context.Background(), "k", validBucket, 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("malformed bucket name → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAll())
		_, err := h.BindObjectKeyToBucket(authedCtx(tid), "k", "buckets/only", 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeAuthorizer{decision: cedar.DecisionDeny})
		_, err := h.BindObjectKeyToBucket(authedCtx(tid), "k", validBucket, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("rebind version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error {
			return ErrVersionMismatch
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.BindObjectKeyToBucket(authedCtx(tid), "k", validBucket, 4)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("rebind other error → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error {
			return errors.New("bucket tenancy violation")
		}}
		h := NewHandler(fr, allowAll())
		_, err := h.BindObjectKeyToBucket(authedCtx(tid), "k", validBucket, 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("rebind ok but re-read fails → internal", func(t *testing.T) {
		fr := &fakeRepo{
			rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error { return nil },
			getFn:    func(context.Context, uuid.UUID, string) (ObjectKey, error) { return ObjectKey{}, errors.New("gone") },
		}
		h := NewHandler(fr, allowAll())
		_, err := h.BindObjectKeyToBucket(authedCtx(tid), "k", validBucket, 0)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok forwards parsed backend+bucket + returns re-read row", func(t *testing.T) {
		fr := &fakeRepo{
			rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error { return nil },
			getFn: func(_ context.Context, tenant uuid.UUID, key string) (ObjectKey, error) {
				return fullKey(tenant, key), nil
			},
		}
		authz := allowAll()
		h := NewHandler(fr, authz)
		got, err := h.BindObjectKeyToBucket(authedCtx(tid), "assets", validBucket, 9)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastRebind.backendID != "aws-eu" || fr.lastRebind.bucketName != "paladin-prod" {
			t.Fatalf("rebind target: got backend=%q bucket=%q", fr.lastRebind.backendID, fr.lastRebind.bucketName)
		}
		if fr.lastRebind.tenantID != tid || fr.lastRebind.objectKey != "assets" || fr.lastRebind.version != 9 {
			t.Fatalf("rebind forwarded %+v", fr.lastRebind)
		}
		if authz.lastAction != cedar.ActionBindObjectKeyToBucket {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionBindObjectKeyToBucket)
		}
		if got.ObjectKey != "assets" {
			t.Fatalf("returned object_key: got %q", got.ObjectKey)
		}
	})
}

// TestEventDispatch exercises the ADR-0003 outbox seam: when an EventProducer
// is wired via SetEventProducer, a successful Create fans out a canonical
// event on the caller's tx, and a producer error rolls the whole op back.
func TestEventDispatch(t *testing.T) {
	tid := uuid.New()

	t.Run("create fans out canonical event with stamped tenant + actor", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateObjectKeyArgs) (ObjectKey, error) {
			return fullKey(a.TenantID, a.ObjectKey), nil
		}}
		ev := &fakeEvents{}
		h := NewHandler(fr, allowAll())
		h.SetEventProducer(ev)
		h.SetLogger(nil) // nil-safe guard: must not replace the default logger

		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "assets", BackendID: "aws-eu"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ev.txCalls != 1 {
			t.Fatalf("DispatchTx calls: got %d want 1", ev.txCalls)
		}
		if ev.lastTx.Type != "paladin.object_key.created" {
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
		if ev.lastTx.Payload["object_key"] != "assets" {
			t.Fatalf("payload object_key: got %v", ev.lastTx.Payload["object_key"])
		}
	})

	t.Run("producer error rolls the op back → internal", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(_ context.Context, a CreateObjectKeyArgs) (ObjectKey, error) {
			return fullKey(a.TenantID, a.ObjectKey), nil
		}}
		ev := &fakeEvents{err: errors.New("outbox down")}
		h := NewHandler(fr, allowAll())
		h.SetEventProducer(ev)

		_, err := h.CreateObjectKey(authedCtx(tid), CreateObjectKeyArgs{ObjectKey: "assets", BackendID: "aws-eu"})
		wantCode(t, err, connect.CodeInternal)
	})
}

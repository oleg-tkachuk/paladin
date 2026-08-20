package tenant

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// ─── fakes ──────────────────────────────────────────────────────────────────

// fakeAuthz is a recording cedar.Authorizer. The zero value denies (Decision
// zero == DecisionDeny), so happy paths must construct it with allow().
type fakeAuthz struct {
	decision cedar.Decision
	err      error

	lastAction   string
	lastResource uuid.UUID
	calls        int
}

func allow() *fakeAuthz { return &fakeAuthz{decision: cedar.DecisionAllow} }
func deny() *fakeAuthz  { return &fakeAuthz{decision: cedar.DecisionDeny} }

func (f *fakeAuthz) IsAuthorized(_ context.Context, _ *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.calls++
	f.lastAction = action
	if r != nil {
		f.lastResource = r.TenantID
	}
	return f.decision, f.err
}

// fakeRepo is a configurable in-memory Repository. Only the func fields a test
// wires are exercised; the rest return zero values so the full interface stays
// satisfied without a database.
type fakeRepo struct {
	createFn         func(context.Context, CreateTenantArgs) (Tenant, error)
	getDefBindingFn  func(context.Context, uuid.UUID) (DefaultBinding, error)
	setDefBindingFn  func(ctx context.Context, tenantID uuid.UUID, backendID, bucketName, setBy string) (DefaultBinding, error)
	clearDefBindFn   func(context.Context, uuid.UUID) error
	getFn            func(context.Context, uuid.UUID) (Tenant, error)
	getBySlugFn      func(context.Context, string) (Tenant, error)
	updateFn         func(context.Context, UpdateTenantArgs) (Tenant, error)
	renameFn         func(context.Context, RenameTenantSlugArgs) (Tenant, error)
	lookupRenamedFn  func(ctx context.Context, oldSlug string, window time.Duration) (RenamedSlug, bool, error)
	listFn           func(context.Context, ListTenantsArgs) ([]Tenant, string, error)
	createTxFn       func(context.Context, pgx.Tx, CreateTenantArgs) error
	updateTxFn       func(context.Context, pgx.Tx, UpdateTenantArgs) (Tenant, error)
	softDeleteTxFn   func(context.Context, pgx.Tx, uuid.UUID, int64) error
	hardDeleteTxFn   func(context.Context, pgx.Tx, uuid.UUID, int64) error
	restoreTxFn      func(context.Context, pgx.Tx, uuid.UUID) (Tenant, error)
	startMigrationFn func(context.Context, StartStorageMigrationArgs) (StorageMigration, error)
	getMigrationFn   func(context.Context, uuid.UUID) (StorageMigration, error)
	sourceBucketFn   func(context.Context, uuid.UUID) (string, string, error)

	// recorded args for forwarding assertions
	lastCreateTx   CreateTenantArgs
	lastUpdateTx   UpdateTenantArgs
	lastSetBinding struct {
		tenantID                     uuid.UUID
		backendID, bucketName, setBy string
	}
	lastListArgs   ListTenantsArgs
	lastRename     RenameTenantSlugArgs
	lastStartArgs  StartStorageMigrationArgs
	lastSoftDelete struct {
		tenantID uuid.UUID
		version  int64
	}
	lastHardDelete struct {
		tenantID uuid.UUID
		version  int64
	}
	lastClearBinding uuid.UUID
}

func (f *fakeRepo) Create(ctx context.Context, a CreateTenantArgs) (Tenant, error) {
	if f.createFn != nil {
		return f.createFn(ctx, a)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) GetDefaultBinding(ctx context.Context, id uuid.UUID) (DefaultBinding, error) {
	if f.getDefBindingFn != nil {
		return f.getDefBindingFn(ctx, id)
	}
	return DefaultBinding{}, nil
}

func (f *fakeRepo) SetDefaultBinding(ctx context.Context, id uuid.UUID, backendID, bucketName, setBy string) (DefaultBinding, error) {
	f.lastSetBinding.tenantID = id
	f.lastSetBinding.backendID = backendID
	f.lastSetBinding.bucketName = bucketName
	f.lastSetBinding.setBy = setBy
	if f.setDefBindingFn != nil {
		return f.setDefBindingFn(ctx, id, backendID, bucketName, setBy)
	}
	return DefaultBinding{}, nil
}

func (f *fakeRepo) ClearDefaultBinding(ctx context.Context, id uuid.UUID) error {
	f.lastClearBinding = id
	if f.clearDefBindFn != nil {
		return f.clearDefBindFn(ctx, id)
	}
	return nil
}

func (f *fakeRepo) Get(ctx context.Context, id uuid.UUID) (Tenant, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) GetBySlug(ctx context.Context, slug string) (Tenant, error) {
	if f.getBySlugFn != nil {
		return f.getBySlugFn(ctx, slug)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) Update(ctx context.Context, a UpdateTenantArgs) (Tenant, error) {
	if f.updateFn != nil {
		return f.updateFn(ctx, a)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) SoftDelete(context.Context, uuid.UUID, int64) error { return nil }
func (f *fakeRepo) HardDelete(context.Context, uuid.UUID, int64) error { return nil }
func (f *fakeRepo) Restore(context.Context, uuid.UUID) (Tenant, error) { return Tenant{}, nil }

func (f *fakeRepo) List(ctx context.Context, a ListTenantsArgs) ([]Tenant, string, error) {
	f.lastListArgs = a
	if f.listFn != nil {
		return f.listFn(ctx, a)
	}
	return nil, "", nil
}

func (f *fakeRepo) Rename(ctx context.Context, a RenameTenantSlugArgs) (Tenant, error) {
	f.lastRename = a
	if f.renameFn != nil {
		return f.renameFn(ctx, a)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) LookupRenamedSlug(ctx context.Context, oldSlug string, window time.Duration) (RenamedSlug, bool, error) {
	if f.lookupRenamedFn != nil {
		return f.lookupRenamedFn(ctx, oldSlug, window)
	}
	return RenamedSlug{}, false, nil
}

// RunInTx runs fn with a nil tx — none of the fake *Tx methods touch it and the
// handler's event producer is left unset (nil-safe no-op dispatch).
func (f *fakeRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}

func (f *fakeRepo) CreateTx(ctx context.Context, tx pgx.Tx, a CreateTenantArgs) error {
	f.lastCreateTx = a
	if f.createTxFn != nil {
		return f.createTxFn(ctx, tx, a)
	}
	return nil
}

func (f *fakeRepo) UpdateTx(ctx context.Context, tx pgx.Tx, a UpdateTenantArgs) (Tenant, error) {
	f.lastUpdateTx = a
	if f.updateTxFn != nil {
		return f.updateTxFn(ctx, tx, a)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) SoftDeleteTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, v int64) error {
	f.lastSoftDelete.tenantID = id
	f.lastSoftDelete.version = v
	if f.softDeleteTxFn != nil {
		return f.softDeleteTxFn(ctx, tx, id, v)
	}
	return nil
}

func (f *fakeRepo) HardDeleteTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, v int64) error {
	f.lastHardDelete.tenantID = id
	f.lastHardDelete.version = v
	if f.hardDeleteTxFn != nil {
		return f.hardDeleteTxFn(ctx, tx, id, v)
	}
	return nil
}

func (f *fakeRepo) RestoreTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Tenant, error) {
	if f.restoreTxFn != nil {
		return f.restoreTxFn(ctx, tx, id)
	}
	return Tenant{}, nil
}

func (f *fakeRepo) StartStorageMigration(ctx context.Context, a StartStorageMigrationArgs) (StorageMigration, error) {
	f.lastStartArgs = a
	if f.startMigrationFn != nil {
		return f.startMigrationFn(ctx, a)
	}
	return StorageMigration{}, nil
}

func (f *fakeRepo) GetStorageMigration(ctx context.Context, id uuid.UUID) (StorageMigration, error) {
	if f.getMigrationFn != nil {
		return f.getMigrationFn(ctx, id)
	}
	return StorageMigration{}, nil
}

func (f *fakeRepo) TenantSourceBucket(ctx context.Context, id uuid.UUID) (string, string, error) {
	if f.sourceBucketFn != nil {
		return f.sourceBucketFn(ctx, id)
	}
	return "", "", nil
}

// ─── context + assertion helpers ────────────────────────────────────────────

// principalCtx returns an authed context for tenant tid carrying the given roles.
func principalCtx(tid uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject:  "u1",
		TenantID: tid,
		Roles:    roles,
	})
}

// adminCtx carries the platform.admin role required by the lifecycle RPCs.
func adminCtx(tid uuid.UUID) context.Context {
	return principalCtx(tid, apiutil.RolePlatformAdmin)
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

func strptr(s string) *string { return &s }

// ─── NewHandler ─────────────────────────────────────────────────────────────

func TestNewHandler_NilPolicyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when policy authorizer is nil")
		}
	}()
	NewHandler(&fakeRepo{}, nil)
}

// ─── GetDefaultBinding ──────────────────────────────────────────────────────

func TestGetDefaultBinding(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.GetDefaultBinding(context.Background(), tid)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		az := deny()
		h := NewHandler(&fakeRepo{}, az)
		_, err := h.GetDefaultBinding(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
		if az.lastAction != cedar.ActionReadTenant {
			t.Errorf("gated on %q, want ReadTenant", az.lastAction)
		}
	})

	t.Run("repo error is passed through", func(t *testing.T) {
		sentinel := errors.New("db down")
		h := NewHandler(&fakeRepo{getDefBindingFn: func(context.Context, uuid.UUID) (DefaultBinding, error) {
			return DefaultBinding{}, sentinel
		}}, allow())
		_, err := h.GetDefaultBinding(principalCtx(tid), tid)
		if !errors.Is(err, sentinel) {
			t.Fatalf("want passthrough of sentinel, got %v", err)
		}
	})

	t.Run("ok forwards tenant + returns binding", func(t *testing.T) {
		var gotID uuid.UUID
		h := NewHandler(&fakeRepo{getDefBindingFn: func(_ context.Context, id uuid.UUID) (DefaultBinding, error) {
			gotID = id
			return DefaultBinding{TenantID: id, BackendID: "b1", BucketId: "bk1"}, nil
		}}, allow())
		got, err := h.GetDefaultBinding(principalCtx(tid), tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotID != tid || got.BackendID != "b1" || got.BucketName != "bk1" {
			t.Fatalf("got %+v (forwarded id %v)", got, gotID)
		}
	})
}

// ─── SetDefaultBinding ──────────────────────────────────────────────────────

func TestSetDefaultBinding(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.SetDefaultBinding(context.Background(), tid, "b", "bk")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		az := deny()
		h := NewHandler(&fakeRepo{}, az)
		_, err := h.SetDefaultBinding(principalCtx(tid), tid, "b", "bk")
		wantCode(t, err, connect.CodePermissionDenied)
		if az.lastAction != cedar.ActionManageTenant {
			t.Errorf("gated on %q, want ManageTenant", az.lastAction)
		}
	})

	t.Run("ok forwards args + stamps setBy from subject", func(t *testing.T) {
		fr := &fakeRepo{setDefBindingFn: func(_ context.Context, id uuid.UUID, backendID, bucketName, setBy string) (DefaultBinding, error) {
			return DefaultBinding{TenantID: id, BackendID: backendID, BucketId: bucketName, SetBy: setBy}, nil
		}}
		got, err := NewHandler(fr, allow()).SetDefaultBinding(principalCtx(tid), tid, "backend-9", "bucket-9")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastSetBinding.tenantID != tid || fr.lastSetBinding.backendID != "backend-9" || fr.lastSetBinding.bucketName != "bucket-9" {
			t.Fatalf("forwarded %+v", fr.lastSetBinding)
		}
		if fr.lastSetBinding.setBy != "u1" {
			t.Fatalf("setBy: got %q, want u1 (principal subject)", fr.lastSetBinding.setBy)
		}
		if got.SetBy != "u1" {
			t.Fatalf("returned setBy %q", got.SetBy)
		}
	})
}

// ─── ClearDefaultBinding ────────────────────────────────────────────────────

func TestClearDefaultBinding(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}, allow()).ClearDefaultBinding(context.Background(), tid)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}, deny()).ClearDefaultBinding(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok forwards tenant", func(t *testing.T) {
		fr := &fakeRepo{}
		if err := NewHandler(fr, allow()).ClearDefaultBinding(principalCtx(tid), tid); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastClearBinding != tid {
			t.Fatalf("forwarded %v, want %v", fr.lastClearBinding, tid)
		}
	})
}

// ─── CreateTenant ───────────────────────────────────────────────────────────

func TestCreateTenant(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(context.Background(), CreateTenantArgs{Slug: "acme"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("non platform-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(principalCtx(tid), CreateTenantArgs{Slug: "acme"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("empty slug → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(adminCtx(tid), CreateTenantArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("malformed slug → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "AB"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("half-formed default binding → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(adminCtx(tid),
			CreateTenantArgs{Slug: "acme", DefaultBackendID: "b1"}) // bucket missing
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("dedicated layout without backend → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(adminCtx(tid),
			CreateTenantArgs{Slug: "acme", StorageLayout: "dedicated"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("dedicated layout with explicit bucket → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(adminCtx(tid),
			CreateTenantArgs{Slug: "acme", StorageLayout: "dedicated", DefaultBackendID: "b1", DefaultBucketName: "bk1"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("unknown storage layout → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).CreateTenant(adminCtx(tid),
			CreateTenantArgs{Slug: "acme", StorageLayout: "weird"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		az := deny()
		_, err := NewHandler(&fakeRepo{}, az).CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme"})
		wantCode(t, err, connect.CodePermissionDenied)
		if az.lastAction != cedar.ActionManageTenant {
			t.Errorf("gated on %q, want ManageTenant", az.lastAction)
		}
	})

	t.Run("create tx conflict → mapped code", func(t *testing.T) {
		fr := &fakeRepo{createTxFn: func(context.Context, pgx.Tx, CreateTenantArgs) error {
			return ErrSlugConflict
		}}
		_, err := NewHandler(fr, allow()).CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme"})
		wantCode(t, err, connect.CodeAlreadyExists)
	})

	t.Run("read-back failure → internal", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID) (Tenant, error) {
			return Tenant{}, errors.New("gone")
		}}
		_, err := NewHandler(fr, allow()).CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok generates id, defaults display name, seeds policy", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id, Slug: "acme", DisplayName: "acme"}, nil
		}}
		got, err := NewHandler(fr, allow()).CreateTenant(adminCtx(tid),
			CreateTenantArgs{Slug: "acme"}) // no TenantID, no DisplayName, no policy
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreateTx.TenantID == uuid.Nil {
			t.Fatal("expected server-generated tenant_id, got nil UUID")
		}
		if fr.lastCreateTx.DisplayName != "acme" {
			t.Errorf("display_name default: got %q, want slug 'acme'", fr.lastCreateTx.DisplayName)
		}
		if fr.lastCreateTx.InheritedCedarPolicy == "" {
			t.Error("expected default cedar policy to be seeded")
		}
		if fr.lastCreateTx.StorageLayout != "shared" {
			t.Errorf("storage_layout default: got %q, want 'shared'", fr.lastCreateTx.StorageLayout)
		}
		if got.Slug != "acme" {
			t.Errorf("returned slug %q", got.Slug)
		}
	})

	t.Run("dedicated layout consumes backend, derives bucket", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id}, nil
		}}
		_, err := NewHandler(fr, allow()).CreateTenant(adminCtx(tid),
			CreateTenantArgs{Slug: "acme", StorageLayout: "dedicated", DefaultBackendID: "b1"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreateTx.DedicatedBackend != "b1" {
			t.Errorf("dedicated backend: got %q, want b1", fr.lastCreateTx.DedicatedBackend)
		}
		if fr.lastCreateTx.DefaultBackendID != "" {
			t.Errorf("shared backend field should be cleared, got %q", fr.lastCreateTx.DefaultBackendID)
		}
	})
}

// ─── GetTenant ──────────────────────────────────────────────────────────────

func TestGetTenant(t *testing.T) {
	tid := uuid.New()
	foreign := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).GetTenant(context.Background(), tid)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("non-admin reading foreign tenant → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).GetTenant(principalCtx(tid), foreign)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("own tenant but policy denies → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, deny()).GetTenant(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo miss → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID) (Tenant, error) {
			return Tenant{}, errors.New("no row")
		}}
		_, err := NewHandler(fr, allow()).GetTenant(principalCtx(tid), tid)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("own tenant ok forwards id", func(t *testing.T) {
		var gotID uuid.UUID
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			gotID = id
			return Tenant{TenantID: id, Slug: "acme"}, nil
		}}
		got, err := NewHandler(fr, allow()).GetTenant(principalCtx(tid), tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotID != tid || got.Slug != "acme" {
			t.Fatalf("got %+v (forwarded id %v)", got, gotID)
		}
	})

	t.Run("platform-admin may read foreign tenant", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id}, nil
		}}
		got, err := NewHandler(fr, allow()).GetTenant(adminCtx(tid), foreign)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.TenantID != foreign {
			t.Fatalf("got %v, want %v", got.TenantID, foreign)
		}
	})
}

// ─── GetTenantBySlug ────────────────────────────────────────────────────────

func TestGetTenantBySlug(t *testing.T) {
	tid := uuid.New()
	foreign := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		fr := &fakeRepo{getBySlugFn: func(context.Context, string) (Tenant, error) {
			return Tenant{TenantID: tid, Slug: "acme"}, nil
		}}
		_, err := NewHandler(fr, allow()).GetTenantBySlug(context.Background(), "acme")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("lookup miss collapses to not found", func(t *testing.T) {
		fr := &fakeRepo{getBySlugFn: func(context.Context, string) (Tenant, error) {
			return Tenant{}, errors.New("no such slug")
		}}
		_, err := NewHandler(fr, allow()).GetTenantBySlug(principalCtx(tid), "ghost")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("foreign tenant non-admin → not found (anti-enumeration)", func(t *testing.T) {
		fr := &fakeRepo{getBySlugFn: func(context.Context, string) (Tenant, error) {
			return Tenant{TenantID: foreign, Slug: "other"}, nil
		}}
		_, err := NewHandler(fr, allow()).GetTenantBySlug(principalCtx(tid), "other")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("policy denies collapses to not found", func(t *testing.T) {
		fr := &fakeRepo{getBySlugFn: func(context.Context, string) (Tenant, error) {
			return Tenant{TenantID: tid, Slug: "acme"}, nil
		}}
		_, err := NewHandler(fr, deny()).GetTenantBySlug(principalCtx(tid), "acme")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("own tenant ok returns row", func(t *testing.T) {
		fr := &fakeRepo{getBySlugFn: func(_ context.Context, slug string) (Tenant, error) {
			return Tenant{TenantID: tid, Slug: slug}, nil
		}}
		got, err := NewHandler(fr, allow()).GetTenantBySlug(principalCtx(tid), "acme")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.TenantID != tid || got.Slug != "acme" {
			t.Fatalf("got %+v", got)
		}
	})
}

// ─── UpdateTenant ───────────────────────────────────────────────────────────

func TestUpdateTenant(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).UpdateTenant(principalCtx(tid), UpdateTenantArgs{TenantID: tid})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, deny()).UpdateTenant(adminCtx(tid), UpdateTenantArgs{TenantID: tid})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("blank display_name → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).UpdateTenant(adminCtx(tid),
			UpdateTenantArgs{TenantID: tid, DisplayName: strptr("   ")})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{updateTxFn: func(context.Context, pgx.Tx, UpdateTenantArgs) (Tenant, error) {
			return Tenant{}, ErrVersionMismatch
		}}
		_, err := NewHandler(fr, allow()).UpdateTenant(adminCtx(tid),
			UpdateTenantArgs{TenantID: tid, ExpectedVersion: 4})
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok trims display_name and forwards", func(t *testing.T) {
		fr := &fakeRepo{updateTxFn: func(_ context.Context, _ pgx.Tx, a UpdateTenantArgs) (Tenant, error) {
			return Tenant{TenantID: a.TenantID, DisplayName: *a.DisplayName}, nil
		}}
		got, err := NewHandler(fr, allow()).UpdateTenant(adminCtx(tid),
			UpdateTenantArgs{TenantID: tid, DisplayName: strptr("  New Name  ")})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastUpdateTx.DisplayName == nil || *fr.lastUpdateTx.DisplayName != "New Name" {
			t.Fatalf("display_name not trimmed before forward: %+v", fr.lastUpdateTx.DisplayName)
		}
		if got.DisplayName != "New Name" {
			t.Fatalf("returned display_name %q", got.DisplayName)
		}
	})
}

// ─── DeleteTenant ───────────────────────────────────────────────────────────

func TestDeleteTenant(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}, allow()).DeleteTenant(principalCtx(tid), tid, 0, false)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}, deny()).DeleteTenant(adminCtx(tid), tid, 0, false)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("soft-delete version mismatch → aborted", func(t *testing.T) {
		fr := &fakeRepo{softDeleteTxFn: func(context.Context, pgx.Tx, uuid.UUID, int64) error {
			return ErrVersionMismatch
		}}
		err := NewHandler(fr, allow()).DeleteTenant(adminCtx(tid), tid, 9, false)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok soft-delete calls SoftDeleteTx with version", func(t *testing.T) {
		fr := &fakeRepo{}
		if err := NewHandler(fr, allow()).DeleteTenant(adminCtx(tid), tid, 7, false); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastSoftDelete.tenantID != tid || fr.lastSoftDelete.version != 7 {
			t.Fatalf("soft-delete forwarded %+v", fr.lastSoftDelete)
		}
	})

	t.Run("ok force uses HardDeleteTx", func(t *testing.T) {
		fr := &fakeRepo{}
		if err := NewHandler(fr, allow()).DeleteTenant(adminCtx(tid), tid, 3, true); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastHardDelete.tenantID != tid || fr.lastHardDelete.version != 3 {
			t.Fatalf("hard-delete forwarded %+v", fr.lastHardDelete)
		}
	})
}

// ─── RestoreTenant ──────────────────────────────────────────────────────────

func TestRestoreTenant(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).RestoreTenant(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("not trashed → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{restoreTxFn: func(context.Context, pgx.Tx, uuid.UUID) (Tenant, error) {
			return Tenant{}, ErrNotTrashed
		}}
		_, err := NewHandler(fr, allow()).RestoreTenant(adminCtx(tid), tid)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("ok returns restored row", func(t *testing.T) {
		fr := &fakeRepo{restoreTxFn: func(_ context.Context, _ pgx.Tx, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id, Slug: "acme"}, nil
		}}
		got, err := NewHandler(fr, allow()).RestoreTenant(adminCtx(tid), tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.TenantID != tid || got.Slug != "acme" {
			t.Fatalf("got %+v", got)
		}
	})
}

// ─── PurgeTenant ────────────────────────────────────────────────────────────

func TestPurgeTenant(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}, allow()).PurgeTenant(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo miss → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID) (Tenant, error) {
			return Tenant{}, errors.New("no row")
		}}
		err := NewHandler(fr, allow()).PurgeTenant(adminCtx(tid), tid)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("active tenant → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id}, nil // DeletedAt zero == active
		}}
		err := NewHandler(fr, allow()).PurgeTenant(adminCtx(tid), tid)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("ok purges trashed tenant with version 0", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id, DeletedAt: time.Now()}, nil
		}}
		if err := NewHandler(fr, allow()).PurgeTenant(adminCtx(tid), tid); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastHardDelete.tenantID != tid || fr.lastHardDelete.version != 0 {
			t.Fatalf("purge hard-delete forwarded %+v", fr.lastHardDelete)
		}
	})
}

// ─── RenameTenantSlug ───────────────────────────────────────────────────────

func TestRenameTenantSlug(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).RenameTenantSlug(principalCtx(tid),
			RenameTenantSlugArgs{TenantID: tid, NewSlug: "new-name"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, deny()).RenameTenantSlug(adminCtx(tid),
			RenameTenantSlugArgs{TenantID: tid, NewSlug: "new-name"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("invalid new slug → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).RenameTenantSlug(adminCtx(tid),
			RenameTenantSlugArgs{TenantID: tid, NewSlug: "NO"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("slug conflict → already exists", func(t *testing.T) {
		fr := &fakeRepo{renameFn: func(context.Context, RenameTenantSlugArgs) (Tenant, error) {
			return Tenant{}, ErrSlugConflict
		}}
		_, err := NewHandler(fr, allow()).RenameTenantSlug(adminCtx(tid),
			RenameTenantSlugArgs{TenantID: tid, NewSlug: "taken-slug"})
		wantCode(t, err, connect.CodeAlreadyExists)
	})

	t.Run("ok forwards args + returns renamed row", func(t *testing.T) {
		fr := &fakeRepo{renameFn: func(_ context.Context, a RenameTenantSlugArgs) (Tenant, error) {
			return Tenant{TenantID: a.TenantID, Slug: a.NewSlug, ResourceVersion: a.ExpectedVersion + 1}, nil
		}}
		got, err := NewHandler(fr, allow()).RenameTenantSlug(adminCtx(tid),
			RenameTenantSlugArgs{TenantID: tid, NewSlug: "new-name", ExpectedVersion: 2})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastRename.NewSlug != "new-name" || fr.lastRename.ExpectedVersion != 2 {
			t.Fatalf("forwarded %+v", fr.lastRename)
		}
		if got.Slug != "new-name" {
			t.Fatalf("returned slug %q", got.Slug)
		}
	})
}

// ─── ListTenants ────────────────────────────────────────────────────────────

func TestListTenants(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, _, err := NewHandler(&fakeRepo{}, allow()).ListTenants(principalCtx(tid), ListTenantsArgs{PageSize: 10}, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		_, _, err := NewHandler(&fakeRepo{}, deny()).ListTenants(adminCtx(tid), ListTenantsArgs{PageSize: 10}, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("malformed page token → invalid argument", func(t *testing.T) {
		_, _, err := NewHandler(&fakeRepo{}, allow()).ListTenants(adminCtx(tid), ListTenantsArgs{PageSize: 10}, "not-a-uuid")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("ok parses token into AfterID and passes result through", func(t *testing.T) {
		after := uuid.New()
		fr := &fakeRepo{listFn: func(_ context.Context, a ListTenantsArgs) ([]Tenant, string, error) {
			return []Tenant{{TenantID: tid, Slug: "acme"}}, "next-cursor", nil
		}}
		rows, next, err := NewHandler(fr, allow()).ListTenants(adminCtx(tid),
			ListTenantsArgs{PageSize: 25, IncludeTrashed: true}, after.String())
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastListArgs.AfterID != after {
			t.Fatalf("AfterID not parsed from token: got %v want %v", fr.lastListArgs.AfterID, after)
		}
		if !fr.lastListArgs.IncludeTrashed {
			t.Error("IncludeTrashed flag not forwarded")
		}
		if len(rows) != 1 || rows[0].Slug != "acme" || next != "next-cursor" {
			t.Fatalf("got rows=%v next=%q", rows, next)
		}
	})
}

// ─── MigrateTenantStorageLayout ─────────────────────────────────────────────

func TestMigrateTenantStorageLayout(t *testing.T) {
	tid := uuid.New()
	sharedTenant := func(_ context.Context, id uuid.UUID) (Tenant, error) {
		return Tenant{TenantID: id, StorageLayout: "shared"}, nil
	}

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).MigrateTenantStorageLayout(principalCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, deny()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo miss → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, uuid.UUID) (Tenant, error) {
			return Tenant{}, errors.New("no row")
		}}
		_, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("already dedicated → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id, StorageLayout: "dedicated"}, nil
		}}
		_, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("source bucket undeterminable → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:          sharedTenant,
			sourceBucketFn: func(context.Context, uuid.UUID) (string, string, error) { return "", "", ErrSourceBucketAmbiguous },
		}
		_, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("migration already exists → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:          sharedTenant,
			sourceBucketFn: func(context.Context, uuid.UUID) (string, string, error) { return "b1", "bk1", nil },
			startMigrationFn: func(context.Context, StartStorageMigrationArgs) (StorageMigration, error) {
				return StorageMigration{}, ErrStorageMigrationExists
			},
		}
		_, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("other start error → internal", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:          sharedTenant,
			sourceBucketFn: func(context.Context, uuid.UUID) (string, string, error) { return "b1", "bk1", nil },
			startMigrationFn: func(context.Context, StartStorageMigrationArgs) (StorageMigration, error) {
				return StorageMigration{}, errors.New("boom")
			},
		}
		_, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok derives target, bucket, retention default", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:          sharedTenant,
			sourceBucketFn: func(context.Context, uuid.UUID) (string, string, error) { return "src-backend", "src-bucket", nil },
			startMigrationFn: func(_ context.Context, a StartStorageMigrationArgs) (StorageMigration, error) {
				return StorageMigration{TenantID: a.TenantID, State: "provisioning"}, nil
			},
		}
		got, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "", 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		a := fr.lastStartArgs
		if a.SourceBackendID != "src-backend" || a.SourceBucketName != "src-bucket" {
			t.Errorf("source not forwarded: %+v", a)
		}
		if a.TargetBackendID != "src-backend" {
			t.Errorf("target backend should default to source, got %q", a.TargetBackendID)
		}
		if a.TargetBucketName != "paladin-"+tid.String() {
			t.Errorf("target bucket derived wrong: %q", a.TargetBucketName)
		}
		if a.CleanupRetentionSeconds != 86400 {
			t.Errorf("retention default: got %d, want 86400", a.CleanupRetentionSeconds)
		}
		if got.State != "provisioning" {
			t.Errorf("returned state %q", got.State)
		}
	})

	t.Run("ok honours explicit target backend + retention", func(t *testing.T) {
		fr := &fakeRepo{
			getFn:          sharedTenant,
			sourceBucketFn: func(context.Context, uuid.UUID) (string, string, error) { return "src-backend", "src-bucket", nil },
			startMigrationFn: func(_ context.Context, a StartStorageMigrationArgs) (StorageMigration, error) {
				return StorageMigration{TenantID: a.TenantID}, nil
			},
		}
		_, err := NewHandler(fr, allow()).MigrateTenantStorageLayout(adminCtx(tid), tid, "dst-backend", 3600)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastStartArgs.TargetBackendID != "dst-backend" {
			t.Errorf("explicit target backend not used: %q", fr.lastStartArgs.TargetBackendID)
		}
		if fr.lastStartArgs.CleanupRetentionSeconds != 3600 {
			t.Errorf("explicit retention not used: %d", fr.lastStartArgs.CleanupRetentionSeconds)
		}
	})
}

// ─── GetTenantStorageMigration ──────────────────────────────────────────────

func TestGetTenantStorageMigration(t *testing.T) {
	tid := uuid.New()

	t.Run("non-admin → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, allow()).GetTenantStorageMigration(principalCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, deny()).GetTenantStorageMigration(adminCtx(tid), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo miss → not found", func(t *testing.T) {
		fr := &fakeRepo{getMigrationFn: func(context.Context, uuid.UUID) (StorageMigration, error) {
			return StorageMigration{}, errors.New("none")
		}}
		_, err := NewHandler(fr, allow()).GetTenantStorageMigration(adminCtx(tid), tid)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok returns status", func(t *testing.T) {
		fr := &fakeRepo{getMigrationFn: func(_ context.Context, id uuid.UUID) (StorageMigration, error) {
			return StorageMigration{TenantID: id, State: "copying", ObjectsTotal: 10, ObjectsCopied: 4}, nil
		}}
		got, err := NewHandler(fr, allow()).GetTenantStorageMigration(adminCtx(tid), tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got.State != "copying" || got.ObjectsCopied != 4 {
			t.Fatalf("got %+v", got)
		}
	})
}

// ─── ResolveRenamedSlug ─────────────────────────────────────────────────────

func TestResolveRenamedSlug(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		fr := &fakeRepo{lookupRenamedFn: func(context.Context, string, time.Duration) (RenamedSlug, bool, error) {
			return RenamedSlug{TenantID: tid, NewSlug: "new"}, true, nil
		}}
		_, _, err := NewHandler(fr, allow()).ResolveRenamedSlug(context.Background(), "old")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies collapses to not found", func(t *testing.T) {
		fr := &fakeRepo{lookupRenamedFn: func(context.Context, string, time.Duration) (RenamedSlug, bool, error) {
			return RenamedSlug{TenantID: tid, NewSlug: "new"}, true, nil
		}}
		_, _, err := NewHandler(fr, deny()).ResolveRenamedSlug(principalCtx(tid), "old")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("lookup error → internal", func(t *testing.T) {
		fr := &fakeRepo{lookupRenamedFn: func(context.Context, string, time.Duration) (RenamedSlug, bool, error) {
			return RenamedSlug{}, false, errors.New("db fault")
		}}
		_, _, err := NewHandler(fr, allow()).ResolveRenamedSlug(principalCtx(tid), "old")
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("no history → not found", func(t *testing.T) {
		fr := &fakeRepo{lookupRenamedFn: func(context.Context, string, time.Duration) (RenamedSlug, bool, error) {
			return RenamedSlug{}, false, nil
		}}
		_, _, err := NewHandler(fr, allow()).ResolveRenamedSlug(principalCtx(tid), "old")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok returns current slug + rotation time", func(t *testing.T) {
		when := time.Now().Add(-time.Hour).UTC()
		fr := &fakeRepo{lookupRenamedFn: func(context.Context, string, time.Duration) (RenamedSlug, bool, error) {
			return RenamedSlug{TenantID: tid, NewSlug: "fresh-slug", RenamedAt: when}, true, nil
		}}
		slug, at, err := NewHandler(fr, allow()).ResolveRenamedSlug(principalCtx(tid), "stale-slug")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if slug != "fresh-slug" || !at.Equal(when) {
			t.Fatalf("got slug=%q at=%v", slug, at)
		}
	})
}

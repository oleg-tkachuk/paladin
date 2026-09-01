package tenant

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// CreateTenant must run its transaction AS the tenant it is creating, not as
// the platform admin who asked for it.
//
// The RLS pool stamps `paladin.tenant_id` from auth.EffectiveTenant at connection
// acquire time (store/postgres/rls.go), and two of the tables this tx writes are
// policed by `WITH CHECK (tenant_id = paladin_session_tenant_id())` — a check
// that admits the cross-tenant flag nowhere, deliberately. So a caller acting as
// itself writes rows Postgres refuses:
//
//	ERROR: new row violates row-level security policy
//	for table "tenant_default_bindings" (SQLSTATE 42501)
//
// which is what every CreateTenant carrying a default binding did, in both
// spellings — `default_binding` on a shared tenant and the derived bucket of a
// dedicated one — from migration 016 until this test was written. `tenants` and
// `buckets` have no RLS, so the ordinary no-binding create went on working and
// the break stayed invisible.
//
// The assertion is on the context rather than on a database, because the context
// is the whole mechanism: the pool reads it once, per acquire, and nothing later
// in the transaction can correct it.
func TestCreateTenantActsAsTheTenantItCreates(t *testing.T) {
	caller := uuid.New()
	created := uuid.New()

	var seen uuid.UUID
	var sawActing bool
	repo := &fakeRepo{
		createTxFn: func(ctx context.Context, _ pgx.Tx, _ CreateTenantArgs) error {
			seen, _ = auth.EffectiveTenant(ctx)
			_, sawActing = auth.ActingTenant(ctx)
			return nil
		},
	}

	_, err := NewHandler(repo, allow()).CreateTenant(adminCtx(caller), CreateTenantArgs{
		TenantID:          created,
		Slug:              "acme",
		DefaultBackendID:  "primary",
		DefaultBucketName: "paladin-primary",
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if !sawActing {
		t.Error("no acting tenant on the tx context — the pool will stamp the caller's own")
	}
	if seen != created {
		t.Errorf("tx ran as %v, want the new tenant %v (caller was %v)", seen, created, caller)
	}
}

// The dedicated layout reaches the same binding insert by a different route —
// it derives the bucket name instead of taking one — so it needs the same swap
// and is asserted separately rather than assumed to follow.
func TestCreateDedicatedTenantActsAsTheTenantItCreates(t *testing.T) {
	caller := uuid.New()
	created := uuid.New()

	var seen uuid.UUID
	repo := &fakeRepo{
		createTxFn: func(ctx context.Context, _ pgx.Tx, _ CreateTenantArgs) error {
			seen, _ = auth.EffectiveTenant(ctx)
			return nil
		},
	}

	_, err := NewHandler(repo, allow()).CreateTenant(adminCtx(caller), CreateTenantArgs{
		TenantID:         created,
		Slug:             "acme-dedicated",
		StorageLayout:    "dedicated",
		DefaultBackendID: "primary",
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if seen != created {
		t.Errorf("tx ran as %v, want the new tenant %v (caller was %v)", seen, created, caller)
	}
}

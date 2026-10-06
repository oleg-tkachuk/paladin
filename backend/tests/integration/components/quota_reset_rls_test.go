//go:build integration

package components

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/quotah"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// quotaResetAllow lets every Cedar check through: these tests are about which
// row the reset reaches under RLS, not about policy.
type quotaResetAllow struct{}

func (quotaResetAllow) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

// ResetQuotaUsage ran as the platform admin's own tenant. Under RLS another
// tenant's quota row was filtered out of the UPDATE rather than refused, so
// the RPC reported success and reset nothing. The suite's superuser pool
// cannot see this; the reset has to run through a NOBYPASSRLS pool.
func TestResetQuotaUsage_ReachesAnotherTenantsQuotaUnderRLS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	callerTenant, _ := mkTenant(t, ctx, admin, "shared")
	ownerTenant, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)

	const usedToday = 5
	tenantQuota := uuid.New()
	mustExec(t, ctx, admin,
		`INSERT INTO quotas (id, tenant_id, usage_bytes_today, usage_objects_today)
		 VALUES ($1, $2, $3, $3)`, tenantQuota, ownerTenant, usedToday)
	bucketQuota := uuid.New()
	mustExec(t, ctx, admin,
		`INSERT INTO quotas (id, bucket_id, usage_bytes_today)
		 SELECT $1, b.id, $2 FROM buckets b WHERE b.name = 'acting-bucket'`, bucketQuota, usedToday)

	pool := rlsPool(t, ctx, admin)
	h := quotah.NewHandler(adapters.NewQuotaRepoV2(sqlc.New(pool), pool), quotaResetAllow{})
	asPlatformAdmin := auth.WithPrincipal(ctx, &auth.Principal{
		Subject:  "operator",
		TenantID: callerTenant,
		Roles:    []string{apiutil.RolePlatformAdmin},
	})

	t.Run("another tenant's quota is reset", func(t *testing.T) {
		if err := h.ResetUsage(asPlatformAdmin, tenantQuota); err != nil {
			t.Fatalf("ResetUsage: %v", err)
		}
		var bytesToday, objectsToday int64
		if err := admin.QueryRow(ctx,
			`SELECT usage_bytes_today, usage_objects_today FROM quotas WHERE id = $1`,
			tenantQuota).Scan(&bytesToday, &objectsToday); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if bytesToday != 0 || objectsToday != 0 {
			t.Errorf("today's usage = (%d bytes, %d objects), want 0 — the reset reported "+
				"success without reaching the row", bytesToday, objectsToday)
		}
	})

	t.Run("an unknown quota is NotFound", func(t *testing.T) {
		err := h.ResetUsage(asPlatformAdmin, uuid.New())
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
		}
	})

	t.Run("a bucket-scoped quota is refused, not silently skipped", func(t *testing.T) {
		err := h.ResetUsage(asPlatformAdmin, bucketQuota)
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
		}
	})
}

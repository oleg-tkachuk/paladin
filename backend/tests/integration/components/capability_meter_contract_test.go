//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/metertest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// contractTTL is how long the capabilities the contract checks record live.
const contractTTL = time.Hour

// The module's Meter contract, on the app role under row-level security, as
// the server runs the store, on a clock the suite moves.
func TestMeterContract(t *testing.T) {
	metertest.Run(t, func(t *testing.T) metertest.Env[pgx.Tx] {
		t.Helper()
		ctx := context.Background()
		admin := startPostgres(t)
		tenant, _ := mkTenant(t, ctx, admin, "shared")
		records := newCapStore(t, admin)
		pool := rlsPool(t, ctx, admin)
		clock := newFakeClock()
		// Every capability of one Env expires together, so a child never
		// outlives its parent.
		expires := clock.Now().Add(contractTTL)
		return metertest.Env[pgx.Tx]{
			Ctx:    auth.WithActingTenant(ctx, tenant),
			Usage:  capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop(), capstore.WithUsageClock(clock.Now)),
			Tenant: tenant,
			// Each tenant's rows are written as that tenant, and the list
			// across tenants is a platform-wide read, as the admin plane does.
			NewTenant: func() (uuid.UUID, error) {
				id, _ := mkTenant(t, ctx, admin, "shared")
				return id, nil
			},
			TenantCtx: func(tenant uuid.UUID) context.Context { return auth.WithActingTenant(ctx, tenant) },
			ListCtx:   auth.WithCrossTenantRead(ctx),
			Advance:   clock.Advance,
			NewCapability: func(spec metertest.CapabilitySpec) (uuid.UUID, error) {
				owner := tenant
				if spec.Tenant != uuid.Nil {
					owner = spec.Tenant
				}
				c := mkCap(owner, "agent:"+uuid.NewString()[:8], expires)
				c.ParentID = spec.Parent
				c.Caveats.Requests = spec.Requests
				c.Caveats.Budget = spec.Budget
				return c.ID, records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer})
			},
			// As Store.Purge removes an expired record: the meter's rows stay
			// behind for PurgeOrphans.
			DeleteCapability: func(id uuid.UUID) error {
				_, err := admin.Exec(ctx, `DELETE FROM capability_records WHERE id = $1`, id)
				return err
			},
		}
	})
}

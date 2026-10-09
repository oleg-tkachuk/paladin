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
// the server runs the store.
func TestMeterContract(t *testing.T) {
	metertest.Run(t, func(t *testing.T) metertest.Env[pgx.Tx] {
		t.Helper()
		ctx := context.Background()
		admin := startPostgres(t)
		tenant, _ := mkTenant(t, ctx, admin, "shared")
		records := newCapStore(t, admin)
		pool := rlsPool(t, ctx, admin)
		return metertest.Env[pgx.Tx]{
			Ctx:    auth.WithActingTenant(ctx, tenant),
			Usage:  capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop()),
			Tenant: tenant,
			NewCapability: func(parent uuid.UUID, maxBudget limes.Nanos) (uuid.UUID, error) {
				c := mkCap(tenant, "agent:"+uuid.NewString()[:8], time.Now().Add(contractTTL))
				c.ParentID = parent
				c.Caveats.MaxBudgetAmount = maxBudget
				c.Caveats.MaxRequests = 0
				return c.ID, records.Insert(ctx, c, seedIssuer)
			},
		}
	})
}

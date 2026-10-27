//go:build integration

package components

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// smallPoolConns is a pool smaller than the burst of charges below.
const smallPoolConns = 2

// refusedBurst is how many charges arrive at once, each past the ceiling.
const refusedBurst = 8

// refusedBurstDeadline bounds the burst: refusals that each held a
// connection while waiting for another never finished at all.
const refusedBurstDeadline = 30 * time.Second

// A burst of charges refused at their ceiling finishes on a pool smaller
// than the burst: a refusal gives its connection back before it reads the
// spend for its receipt.
func TestRefusedChargesDoNotDrainThePool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	records := newCapStore(t, admin)
	c := mkCap(tenant, "agent:burst", time.Now().Add(time.Hour))
	c.Caveats.Budget = limes.BudgetCeiling{UnitCode: testUnit}
	if err := records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
		t.Fatal(err)
	}
	subject, err := limes.StoredMeterSubject(ctx, records, c.ID)
	if err != nil {
		t.Fatal(err)
	}

	cfg := admin.Config().Copy()
	cfg.MaxConns = smallPoolConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	usage := newUsageStore(t, pool)

	burstCtx, cancel := context.WithTimeout(ctx, refusedBurstDeadline)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, refusedBurst)
	for range refusedBurst {
		wg.Go(func() {
			_, err := usage.Charge(burstCtx, meter.ChargeRequest{
				Subject: subject, Amount: limes.NanosPerUnit, ExternalRef: uuid.New().String(),
			}, nil)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, limes.ErrBudgetExceeded) {
			t.Errorf("charge past a ceiling of 0: %v, want ErrBudgetExceeded", err)
		}
	}
}

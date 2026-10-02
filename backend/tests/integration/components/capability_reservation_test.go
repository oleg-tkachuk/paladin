//go:build integration

package components

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/capability"
)

// rlsUsage is a usage store on the app role, without BYPASSRLS, as the
// server runs it — every reservation path then has to work under the
// tenant scoping the interceptor's ledger context sets.
func rlsUsage(t *testing.T, ctx context.Context, f lineageFixture) *capstore.UsageStore {
	t.Helper()
	pool := rlsPool(t, ctx, f.pool)
	return capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop())
}

func TestReservationLifecycleUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	reserve := func(id uuid.UUID, amount float64) (capability.Reservation, error) {
		return f.usage.Reserve(ledgerCtx, capability.ReserveRequest{
			CapabilityID: id, TenantID: f.tenant, Amount: amount, MaxBudget: 20, UnitCode: "USD", Op: "llm", Actor: "agent",
		})
	}

	// The child holds 15 of its parent's 25; the sibling cannot hold 15 more.
	r, err := reserve(f.child, 15)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := reserve(f.sibling, 15); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("sibling hold past the parent: err = %v, want ErrBudgetExceeded", err)
	}
	// Nor can it charge into the held room.
	if _, err := f.usage.Charge(ledgerCtx, capability.ChargeRequest{
		CapabilityID: f.sibling, TenantID: f.tenant, Amount: 15, MaxBudget: 20, UnitCode: "USD",
	}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("sibling charge into held room: err = %v, want ErrBudgetExceeded", err)
	}

	// Settling above what the child's own ceiling allows is refused, and the
	// hold survives it.
	if _, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: r.ID, Amount: 21, MaxBudget: 20}, nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("settle past the ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if u, _ := f.usage.Get(ledgerCtx, f.root); !closeEnough(u.ReservedAmount, 15) {
		t.Fatalf("parent hold after a refused settle = %v, want 15", u.ReservedAmount)
	}

	receipt, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: r.ID, Amount: 4, MaxBudget: 20}, nil)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	root, _ := f.usage.Get(ledgerCtx, f.root)
	if !closeEnough(root.SpentAmount, 4) || root.ReservedAmount != 0 {
		t.Errorf("parent after settle = %+v, want 4 spent, 0 held", root)
	}
	var op, actor string
	if err := f.pool.QueryRow(ctx, `SELECT op, actor_subject FROM charges WHERE id = $1`, receipt.ChargeID).Scan(&op, &actor); err != nil {
		t.Fatalf("settled charge not in the ledger: %v", err)
	}
	if op != "llm" || actor != "agent" {
		t.Errorf("ledger row op=%q actor=%q, want the reservation's", op, actor)
	}
	if _, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: r.ID, Amount: 1, MaxBudget: 20}, nil); !errors.Is(err, capability.ErrReservationNotFound) {
		t.Errorf("second settle: err = %v, want ErrReservationNotFound", err)
	}

	// Release is idempotent and frees the room.
	r2, err := reserve(f.sibling, 10)
	if err != nil {
		t.Fatalf("reserve after settle: %v", err)
	}
	for range 2 {
		if err := f.usage.Release(ledgerCtx, r2.ID); err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	if u, _ := f.usage.Get(ledgerCtx, f.root); u.ReservedAmount != 0 {
		t.Errorf("parent held after release = %v, want 0", u.ReservedAmount)
	}
}

// The sweep runs with no request tenant, on the app role: it lists across
// tenants and releases each reservation under that reservation's tenant.
func TestExpiredReservationsAreReleased(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	r, err := f.usage.Reserve(ledgerCtx, capability.ReserveRequest{
		CapabilityID: f.child, TenantID: f.tenant, Amount: 20, MaxBudget: 20, UnitCode: "USD", TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	if _, err := f.usage.Settle(ledgerCtx, capability.SettleRequest{ReservationID: r.ID, Amount: 1, MaxBudget: 20}, nil); !errors.Is(err, capability.ErrReservationNotFound) {
		t.Fatalf("settle after expiry: err = %v, want ErrReservationNotFound", err)
	}
	n, err := f.usage.ReleaseExpired(auth.WithCrossTenantRead(ctx))
	if err != nil || n != 1 {
		t.Fatalf("ReleaseExpired = %d, %v; want 1", n, err)
	}
	for _, id := range []uuid.UUID{f.child, f.root} {
		if u, _ := f.usage.Get(ledgerCtx, id); u.ReservedAmount != 0 {
			t.Errorf("%s still holds %v after the sweep", id, u.ReservedAmount)
		}
	}
}

// Holds are counted under the counter row's lock, so concurrent reservations
// cannot together pass a ceiling.
func TestConcurrentReservationsNeverOvercommit(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.usage.Reserve(ledgerCtx, capability.ReserveRequest{
				CapabilityID: f.child, TenantID: f.tenant, Amount: 3, MaxBudget: 20, UnitCode: "USD",
			})
			if err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			} else if !errors.Is(err, capability.ErrBudgetExceeded) {
				t.Errorf("reserve: %v", err)
			}
		}()
	}
	wg.Wait()
	if granted != 6 {
		t.Fatalf("granted %d holds of 3 under a ceiling of 20, want exactly 6", granted)
	}
}

//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// rlsUsage is a usage store on the app role, without BYPASSRLS, as the
// server runs it — every reservation path then has to work under the
// tenant scoping the interceptor's ledger context sets.
func rlsUsage(t *testing.T, ctx context.Context, f lineageFixture) *capstore.UsageStore {
	t.Helper()
	return newUsageStore(t, rlsPool(t, ctx, f.pool))
}

func TestReservationLifecycleUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	child, sibling := f.subject(t, ctx, f.child), f.subject(t, ctx, f.sibling)
	reserve := func(s limes.MeterSubject, amount string) (meter.Reservation, error) {
		return f.usage.Reserve(ledgerCtx, meter.ReserveRequest{
			Subject: s, Amount: limes.MustParseAmount(amount), Op: "llm", Actor: "agent",
		})
	}

	// The child holds 15 of its parent's 25; the sibling cannot hold 15 more.
	r, err := reserve(child, "15")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := reserve(sibling, "15"); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("sibling hold past the parent: err = %v, want ErrBudgetExceeded", err)
	}
	// Nor can it charge into the held room.
	if _, err := f.usage.Charge(ledgerCtx, meter.ChargeRequest{
		Subject: sibling, Amount: limes.MustParseAmount("15"),
	}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("sibling charge into held room: err = %v, want ErrBudgetExceeded", err)
	}

	// Only the capability that made the hold settles it: another one's
	// subject finds no reservation, and the hold survives it.
	if _, err := f.usage.Settle(ledgerCtx, meter.SettleRequest{Subject: sibling, ReservationID: r.ID, Amount: limes.MustParseAmount("4")}, nil); !errors.Is(err, meter.ErrReservationNotFound) {
		t.Fatalf("settle by another capability: err = %v, want ErrReservationNotFound", err)
	}

	// Settling above what the child's own ceiling allows is refused, and the
	// hold survives it.
	if _, err := f.usage.Settle(ledgerCtx, meter.SettleRequest{Subject: child, ReservationID: r.ID, Amount: limes.MustParseAmount("21")}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("settle past the ceiling: err = %v, want ErrBudgetExceeded", err)
	}
	if u, _ := f.usage.GetUsage(ledgerCtx, f.root); u.ReservedAmount != limes.MustParseAmount("15") {
		t.Fatalf("parent hold after a refused settle = %v, want 15", u.ReservedAmount)
	}

	receipt, err := f.usage.Settle(ledgerCtx, meter.SettleRequest{Subject: child, ReservationID: r.ID, Amount: limes.MustParseAmount("4")}, nil)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	root, _ := f.usage.GetUsage(ledgerCtx, f.root)
	if root.SpentAmount != limes.MustParseAmount("4") || root.ReservedAmount != 0 {
		t.Errorf("parent after settle = %+v, want 4 spent, 0 held", root)
	}
	var op, actor string
	if err := f.pool.QueryRow(ctx, `SELECT op, actor FROM limes.charges WHERE id = $1`, receipt.ChargeID).Scan(&op, &actor); err != nil {
		t.Fatalf("settled charge not in the ledger: %v", err)
	}
	if op != "llm" || actor != "agent" {
		t.Errorf("ledger row op=%q actor=%q, want the reservation's", op, actor)
	}
	// Settling again for the same amount is a replay; for another it is a
	// conflict, not a quiet answer with the first receipt.
	again, err := f.usage.Settle(ledgerCtx, meter.SettleRequest{Subject: child, ReservationID: r.ID, Amount: limes.MustParseAmount("4")}, nil)
	if err != nil || !again.Replayed || again.ChargeID != receipt.ChargeID || again.SpentAmount != limes.MustParseAmount("4") {
		t.Errorf("second settle = %+v, %v; want a replay of %s at spend 4", again, err, receipt.ChargeID)
	}
	if _, err := f.usage.Settle(ledgerCtx, meter.SettleRequest{Subject: child, ReservationID: r.ID, Amount: limes.MustParseAmount("1")}, nil); !errors.Is(err, meter.ErrSettleConflict) {
		t.Errorf("a settle for another amount = %v, want ErrSettleConflict", err)
	}

	// Release is idempotent and frees the room — for the capability that
	// made the hold only: another one's release leaves it held.
	r2, err := reserve(sibling, "10")
	if err != nil {
		t.Fatalf("reserve after settle: %v", err)
	}
	if err := f.usage.Release(ledgerCtx, meter.ReleaseRequest{Subject: child, ReservationID: r2.ID}); err != nil {
		t.Fatalf("release by another capability: %v", err)
	}
	if u, _ := f.usage.GetUsage(ledgerCtx, f.root); u.ReservedAmount != limes.MustParseAmount("10") {
		t.Fatalf("parent hold after another capability's release = %v, want 10", u.ReservedAmount)
	}
	for range 2 {
		if err := f.usage.Release(ledgerCtx, meter.ReleaseRequest{Subject: sibling, ReservationID: r2.ID}); err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	if u, _ := f.usage.GetUsage(ledgerCtx, f.root); u.ReservedAmount != 0 {
		t.Errorf("parent held after release = %v, want 0", u.ReservedAmount)
	}
}

// shortHoldTTL is how long the sweep test's hold lives, and holdLapse how
// long the test waits, past it, before the sweep.
const (
	shortHoldTTL = time.Millisecond
	holdLapse    = 20 * time.Millisecond
)

// The sweep runs with no request tenant, on the app role, scoped to write
// every tenant as the purger scopes it; a platform-wide read is refused.
func TestExpiredReservationsAreReleased(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)

	child := f.subject(t, ctx, f.child)
	r, err := f.usage.Reserve(ledgerCtx, meter.ReserveRequest{
		Subject: child, Amount: limes.MustParseAmount("20"), TTL: shortHoldTTL,
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	time.Sleep(holdLapse)

	if _, err := f.usage.Settle(ledgerCtx, meter.SettleRequest{Subject: child, ReservationID: r.ID, Amount: limes.MustParseAmount("1")}, nil); !errors.Is(err, meter.ErrReservationNotFound) {
		t.Fatalf("settle after expiry: err = %v, want ErrReservationNotFound", err)
	}
	if res, err := f.usage.ReleaseExpired(ctx, meter.ReleaseExpiredRequest{}); err != nil || res.Released != 0 {
		t.Fatalf("ReleaseExpired on a bare context = %d, %v; want 0 — the scope is not engaging", res.Released, err)
	}
	if _, err := f.usage.ReleaseExpired(auth.WithCrossTenantRead(ctx), meter.ReleaseExpiredRequest{}); !errors.Is(err, limes.ErrScopeMismatch) {
		t.Fatalf("ReleaseExpired under a read-only scope: err = %v, want ErrScopeMismatch", err)
	}
	// Housekeeping, as the purger scopes it.
	res, err := f.usage.ReleaseExpired(pgstore.WithAllTenants(ctx), meter.ReleaseExpiredRequest{})
	if err != nil || res.Released != 1 {
		t.Fatalf("ReleaseExpired = %d, %v; want 1", res.Released, err)
	}
	for _, id := range []uuid.UUID{f.child, f.root} {
		if u, _ := f.usage.GetUsage(ledgerCtx, id); u.ReservedAmount != 0 {
			t.Errorf("%s still holds %v after the sweep", id, u.ReservedAmount)
		}
	}
}

// The admin API lists a tenant's open holds from the store the server runs,
// on the app role: the tenant's own, and none of them for another tenant.
func TestTenantReservationsAreListedUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	r, err := f.usage.Reserve(auth.WithActingTenant(ctx, f.tenant), meter.ReserveRequest{
		Subject: f.subject(t, ctx, f.child), Amount: limes.MustParseAmount("15"), Op: "llm", Actor: "agent",
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	srv := admin.NewTenantBudgetServer(f.usage, nil)

	resp, err := srv.ListReservations(ctx, &pb.TenantBudgetServiceListReservationsRequest{TenantId: f.tenant.String()})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := resp.GetReservations(); len(got) != 1 || got[0].GetId() != r.ID.String() ||
		got[0].GetCapabilityId() != f.child.String() || got[0].GetAmount().GetUnits() != 15 || got[0].GetActor() != "agent" {
		t.Errorf("listed %v, want the one hold %s of 15 by agent", got, r.ID)
	}
	other, err := srv.ListReservations(ctx, &pb.TenantBudgetServiceListReservationsRequest{TenantId: uuid.New().String()})
	if err != nil || len(other.GetReservations()) != 0 {
		t.Errorf("another tenant: %v, %v; want no hold", other.GetReservations(), err)
	}
}

// Revoking one capability releases its holds at once, on the app role, and
// only its: a live sibling's hold stays, as its cost may still come.
func TestRevokingOneCapabilityReleasesItsHolds(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	held := limes.MustParseAmount("5")
	for _, id := range []uuid.UUID{f.child, f.sibling} {
		if _, err := f.usage.Reserve(ledgerCtx, meter.ReserveRequest{
			Subject: f.subject(t, ctx, id), Amount: held, Op: "llm", Actor: "agent",
		}); err != nil {
			t.Fatalf("reserve on %s: %v", id, err)
		}
	}
	if _, err := f.records.Revoke(ledgerCtx, limes.RevokeRequest{CapabilityID: f.child}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// As the admin plane's Revoke calls it, with no tenant in scope yet.
	bulk := &auth.BulkRevocation{Holds: f.usage}
	if n, err := bulk.ReleaseRevoked(ctx, f.tenant, f.child); err != nil || n != 1 {
		t.Fatalf("release: %d, %v; want the child's one hold", n, err)
	}
	if u, _ := f.usage.GetUsage(ledgerCtx, f.child); u.ReservedAmount != 0 {
		t.Errorf("the revoked child still holds %v", u.ReservedAmount)
	}
	if u, _ := f.usage.GetUsage(ledgerCtx, f.sibling); u.ReservedAmount != held {
		t.Errorf("the live sibling holds %v, want %v", u.ReservedAmount, held)
	}
	if n, err := bulk.ReleaseRevoked(ctx, f.tenant, f.child); err != nil || n != 0 {
		t.Errorf("repeat: %d, %v; want nothing left", n, err)
	}
}

//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The graces the scan runs with here: a PENDING upload is left an hour past
// its URL's expiry, a promoted object's anchor settles a minute past it.
const (
	scanPendingGrace = time.Hour
	scanSettleGrace  = time.Minute
	// scanBatch is large enough for every anchor the test database holds.
	scanBatch = 1000
)

// A promoted object's anchor is taken up once its URL has expired by the
// settle grace, while a PENDING upload whose URL expired at the same instant
// is left to its client for the longer pending grace (ADR-0051): the two are
// not one interval.
func TestScanExpiredAnchorsGracesByState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	pending := f.registerUpload(t, ctx, "pending.bin", f.private)
	promoted := f.registerUpload(t, ctx, lateKey, f.private)
	f.complete(t, ctx, promoted, f.private, f.putThroughURL(t, ctx, lateKey, f.private))

	// Both URLs expired past the settle grace, within the pending one.
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - $2::interval
		WHERE tenant_id = $1 AND presign_expires_at IS NOT NULL`, f.tenant, (2 * scanSettleGrace).String())
	got := f.scanAnchors(t, ctx)
	if !got[promoted] || got[pending] {
		t.Fatalf("scanned promoted %v, pending %v; want the promoted object only", got[promoted], got[pending])
	}

	// Past the pending grace too, the upload is taken up as well.
	mustExec(t, ctx, f.pool, `UPDATE objects SET presign_expires_at = now() - $2::interval
		WHERE id = $1`, pending, (2 * scanPendingGrace).String())
	if got := f.scanAnchors(t, ctx); !got[pending] {
		t.Fatal("the PENDING upload past its grace was not scanned")
	}
}

// scanAnchors is the set of the tenant's objects the reconciler's scan takes
// up with the test's graces.
func (f publicMigrationFixture) scanAnchors(t *testing.T, ctx context.Context) map[uuid.UUID]bool {
	t.Helper()
	anchors, err := statemachine.New(f.pool).ScanExpiredAnchors(ctx,
		statemachine.AnchorGraces{Pending: scanPendingGrace, Settle: scanSettleGrace}, scanBatch, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	out := map[uuid.UUID]bool{}
	for _, a := range anchors {
		out[a.ObjectID] = true
	}
	return out
}

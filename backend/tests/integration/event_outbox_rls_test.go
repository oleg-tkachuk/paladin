//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// A platform admin acting on another tenant's resources — creating its
// bucket, trashing it — records the event for that tenant from a session
// pinned to its own. The runtime (RLS) pool must accept that insert: a
// refusal aborts the admin's change itself.
func TestEventOutbox_AnAdminRecordsAnEventForAnotherTenant(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	admin := mustCreateTenant(t, f.h.PoolMigrate, "outbox-rls-admin")
	target := mustCreateTenant(t, f.h.PoolMigrate, "outbox-rls-target")

	ctx := ctxAdmin(t, admin)
	tx, err := f.h.PoolApp.Begin(ctx)
	if err != nil {
		t.Fatalf("begin on the RLS pool: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := worker.NewEventRecorder(nil).RecordTx(ctx, tx, target.String(), makeEvent("paladin.bucket.created", target)); err != nil {
		t.Fatalf("RecordTx for another tenant: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := f.recordedEvents(t, target); got != 1 {
		t.Errorf("recorded events for the target = %d, want 1", got)
	}
}

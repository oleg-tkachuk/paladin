package tenanth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// PurgeTenant must not fan out paladin.tenant.purged.
//
// The outbox pattern cannot carry this event: event_deliveries.tenant_id and
// event_subscriptions.tenant_id both reference tenants(id) ON DELETE CASCADE,
// so the delivery rows and the subscriptions that would receive them are
// destroyed by the same statement. Attempting it anyway put a foreign-key
// violation inside the purge transaction, which Postgres treats as fatal to
// the whole transaction — COMMIT came back as ROLLBACK, and the tenant stayed
// in the trash. Only tenants with a subscription were affected, so it read as
// intermittent for a long time.
//
// This test fails loudly if a dispatch is ever put back: the producer errors
// on any call, and a purge that touches it inherits the failure.
type refusingProducer struct{ calls int }

func (p *refusingProducer) Dispatch(context.Context, string, worker.Event) (int, error) {
	p.calls++
	return 0, errors.New("Dispatch must not be reached from PurgeTenant")
}

func (p *refusingProducer) DispatchTx(context.Context, pgx.Tx, string, worker.Event) (int, error) {
	p.calls++
	// The real failure was a foreign-key violation on the insert. Standing in
	// for it with any error reproduces what matters: an error returned from
	// inside the transaction aborts the purge.
	return 0, errors.New("DispatchTx must not be reached from PurgeTenant")
}

func TestPurgeTenantDoesNotFanOutItsOwnEvent(t *testing.T) {
	tid := uuid.New()
	repo := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
		return Tenant{TenantID: id, DeletedAt: time.Now()}, nil
	}}
	producer := &refusingProducer{}

	h := NewHandler(repo, allow())
	h.SetEventProducer(producer)

	if err := h.PurgeTenant(adminCtx(tid), tid); err != nil {
		t.Fatalf("purge failed: %v — a tenant with a subscription must still purge", err)
	}
	if producer.calls != 0 {
		t.Errorf("the purge dispatched %d event(s); the delivery rows and the "+
			"subscriptions that would receive them are cascaded away by this very "+
			"delete, so the only thing a dispatch can do here is abort the purge",
			producer.calls)
	}
	if repo.lastHardDelete.tenantID != tid {
		t.Errorf("hard delete forwarded %+v, want tenant %v", repo.lastHardDelete, tid)
	}
}

// Purging an active tenant names the call that has to come first. The message
// used to send callers to DeleteTenant(force=true), a flag that no longer
// exists.
func TestPurgeActiveTenantPointsAtDeleteTenant(t *testing.T) {
	tid := uuid.New()
	repo := &fakeRepo{getFn: func(_ context.Context, id uuid.UUID) (Tenant, error) {
		return Tenant{TenantID: id}, nil
	}}
	err := NewHandler(repo, allow()).PurgeTenant(adminCtx(tid), tid)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want %v (err: %v)", got, connect.CodeFailedPrecondition, err)
	}
	if !errors.Is(err, errPurgeActiveTenant) {
		t.Errorf("err = %v, want errPurgeActiveTenant", err)
	}
	if strings.Contains(err.Error(), "force") {
		t.Errorf("message still points at a flag that is gone: %v", err)
	}
	if repo.lastHardDelete.tenantID != uuid.Nil {
		t.Errorf("an active tenant was hard-deleted: %+v", repo.lastHardDelete)
	}
}

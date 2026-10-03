package eventingest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// foundLookup resolves the event to one object.
type foundLookup struct{ fakeLookup }

func (f *foundLookup) LookupObjectByKey(context.Context, pgtype.UUID, string, string) (sqlc.LookupObjectByKeyRow, error) {
	return sqlc.LookupObjectByKeyRow{Object: sqlc.Object{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}}, nil
}

type refusingPromoter struct {
	err   error
	calls int
}

func (p *refusingPromoter) PromoteToAvailableInTx(context.Context, uuid.UUID, string, int64, string, string, statemachine.Source, func(context.Context, pgx.Tx) error) (bool, error) {
	p.calls++
	return false, p.err
}

// An event reporting bytes that are not the registered ones used to fail
// Handle, so the consumer redelivered it forever: retrying cannot change the
// bytes. It is acknowledged and left for the reconciler, which settles the
// object from a HEAD.
func TestHandleAcknowledgesAContentMismatch(t *testing.T) {
	p := &refusingPromoter{err: &statemachine.ContentMismatchError{Field: "size", Want: "10", Got: "11"}}
	h := &PromoteHandler{Lookup: &foundLookup{}, Transitioner: p, Logger: zap.NewNop()}
	ev := uploadedEvent(uuid.New(), "docs", "a.txt")
	ev.SubjectFields.SizeBytes = 11

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v, want the event acknowledged", err)
	}
	if p.calls != 1 {
		t.Fatalf("promote called %d times, want 1", p.calls)
	}
}

// Any other promote failure is still returned, so the event is retried.
func TestHandleReturnsOtherPromoteErrors(t *testing.T) {
	p := &refusingPromoter{err: errors.New("db down")}
	h := &PromoteHandler{Lookup: &foundLookup{}, Transitioner: p, Logger: zap.NewNop()}
	if err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", "a.txt")); err == nil {
		t.Fatal("a database error was swallowed")
	}
}

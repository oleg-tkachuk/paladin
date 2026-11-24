package eventingest

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// placingPromoter records where the promote was told the event's bytes are.
type placingPromoter struct {
	placed *statemachine.Placement
	err    error
}

func (p *placingPromoter) PromoteToAvailableInTx(_ context.Context, _ uuid.UUID, placed *statemachine.Placement, _ string, _ int64, _, _ string, _ statemachine.Source, _ func(context.Context, pgx.Tx) error) (bool, error) {
	p.placed = placed
	return false, p.err
}

// boundLookup resolves the event to one object in a collection bound to
// backend/bucket.
func boundLookup(backend, bucket string) *foundLookup {
	return &foundLookup{fakeLookup{binding: sqlc.GetCollectionRow{BackendName: backend, BucketName: bucket}}}
}

// An event naming its bucket promotes with that bucket as the bytes'
// placement, so a storage migration rebinding the collection between the
// bucket check and the promote refuses it; one naming none checks nothing.
func TestHandlePromotesWhereTheEventSaysTheBytesAre(t *testing.T) {
	for name, tc := range map[string]struct {
		bucket string
		want   *statemachine.Placement
	}{
		"the event names its bucket": {"shared", &statemachine.Placement{BackendID: "be-1", Bucket: "shared"}},
		"the event names none":       {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			p := &placingPromoter{}
			h := &PromoteHandler{Lookup: boundLookup("be-1", "shared"), Transitioner: p, Logger: zap.NewNop()}
			ev := uploadedEvent(uuid.New(), "docs", "a.txt")
			ev.SubjectFields.Bucket = tc.bucket
			if err := h.Handle(context.Background(), ev); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if (p.placed == nil) != (tc.want == nil) || (p.placed != nil && *p.placed != *tc.want) {
				t.Errorf("promoted with placement %+v, want %+v", p.placed, tc.want)
			}
		})
	}
}

// A promote refused because the collection moved since the bucket check is
// acknowledged: the event's bytes are in a bucket the object is no longer
// read from, so a redelivery would be refused again. A pending upload is the
// reconciler's to move.
func TestHandleAcknowledgesAPromoteTheRebindRefused(t *testing.T) {
	p := &placingPromoter{err: &statemachine.BindingMovedError{}}
	h := &PromoteHandler{Lookup: boundLookup("be-1", "shared"), Transitioner: p, Logger: zap.NewNop()}
	ev := uploadedEvent(uuid.New(), "docs", "a.txt")
	ev.SubjectFields.Bucket = "shared"
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v, want the event acknowledged", err)
	}
}

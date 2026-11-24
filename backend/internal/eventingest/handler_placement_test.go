package eventingest

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// placingPromoter records where the promote was told the event's bytes are.
type placingPromoter struct {
	placed *statemachine.Placement
	calls  int
	err    error
}

func (p *placingPromoter) PromoteToAvailableInTx(_ context.Context, _ uuid.UUID, placed *statemachine.Placement, _ string, _ int64, _, _ string, _ statemachine.Source, _ func(context.Context, pgx.Tx) error) (bool, error) {
	p.placed = placed
	p.calls++
	return false, p.err
}

// An uploaded event is the row's only when it comes from the bucket the
// row's upload URL was signed for (ADR-0051): the promote places the object
// there. An event from another bucket — a storage migration's own copy, the
// URL of a deleted row under the same key — and one for an object whose URLs
// have all settled are acknowledged and change nothing.
func TestHandlePromotesOnlyAWriteThroughTheRowsURL(t *testing.T) {
	anchor := &statemachine.Placement{BackendID: foundBackend, Bucket: foundBucket}
	for name, tc := range map[string]struct {
		bucket  string
		settled bool
		want    *statemachine.Placement
		calls   int
	}{
		"from the anchor":            {foundBucket, false, anchor, 1},
		"naming no bucket":           {"", false, nil, 1},
		"from another bucket":        {"dedicated", false, nil, 0},
		"for a settled object":       {foundBucket, true, nil, 0},
		"naming none, after settled": {"", true, nil, 0},
	} {
		t.Run(name, func(t *testing.T) {
			p := &placingPromoter{}
			h := &PromoteHandler{Lookup: &foundLookup{settled: tc.settled}, Transitioner: p, Logger: zap.NewNop()}
			ev := uploadedEvent(uuid.New(), "docs", "a.txt")
			ev.SubjectFields.Bucket = tc.bucket
			if err := h.Handle(context.Background(), ev); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if p.calls != tc.calls {
				t.Fatalf("promoted %d times, want %d", p.calls, tc.calls)
			}
			if (p.placed == nil) != (tc.want == nil) || (p.placed != nil && *p.placed != *tc.want) {
				t.Errorf("promoted with placement %+v, want %+v", p.placed, tc.want)
			}
		})
	}
}

// A promote refused because the row settled or was anchored elsewhere since
// it was read is acknowledged: a redelivery would be refused again.
func TestHandleAcknowledgesAPromoteNotAnchored(t *testing.T) {
	p := &placingPromoter{err: &statemachine.NotAnchoredError{}}
	h := &PromoteHandler{Lookup: &foundLookup{}, Transitioner: p, Logger: zap.NewNop()}
	ev := uploadedEvent(uuid.New(), "docs", "a.txt")
	ev.SubjectFields.Bucket = foundBucket
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v, want the event acknowledged", err)
	}
}

// A deleted event concerns the object only from the bucket its bytes are in:
// a delete anywhere else leaves them where they are, unprobed.
func TestHandleTrashesOnlyForADeleteWhereTheBytesAre(t *testing.T) {
	for name, tc := range map[string]struct {
		bucket string
		probed bool
	}{
		"from the placement": {foundBucket, true},
		"naming no bucket":   {"", true},
		"from another":       {"source-copy", false},
	} {
		t.Run(name, func(t *testing.T) {
			probe := &fakeProbe{found: true}
			h := &PromoteHandler{Lookup: &foundLookup{}, Probe: probe, Deleter: &fakeDeleter{}, Logger: zap.NewNop()}
			ev := uploadedEvent(uuid.New(), "docs", "a.txt")
			ev.Type = EventTypeDeleted
			ev.SubjectFields.Bucket = tc.bucket
			if err := h.Handle(context.Background(), ev); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if probe.called != tc.probed {
				t.Fatalf("probed = %v, want %v", probe.called, tc.probed)
			}
		})
	}
}

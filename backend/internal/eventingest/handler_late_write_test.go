package eventingest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// lateRecorder records the late source write the handler asked for, and
// answers err.
type lateRecorder struct {
	called bool
	w      statemachine.LateSourceWrite
	err    error
}

func (l *lateRecorder) PromoteLateSourceWriteInTx(_ context.Context, _ uuid.UUID, w statemachine.LateSourceWrite, _ func(context.Context, pgx.Tx) error) (bool, error) {
	l.called = true
	l.w = w
	return l.err == nil, l.err
}

// noMove is a mover the routing tests never reach: the promoter is faked.
type noMove struct{}

var errNoMove = errors.New("unexpected move")

func (noMove) MoveObjectBytes(context.Context, uuid.UUID, statemachine.Placement, statemachine.Placement) (statemachine.MovedBytes, error) {
	return statemachine.MovedBytes{}, errNoMove
}

// lateEvent is an uploaded event from bucket for a collection bound to
// be-1/dedicated.
func lateEvent(bucket string) CloudEvent {
	ev := uploadedEvent(uuid.New(), "docs", "a.txt")
	ev.SubjectFields.Bucket = bucket
	ev.SubjectFields.Sequencer = "0001"
	return ev
}

// An uploaded event from a bucket the collection is not bound to is a late
// source write when the handler moves those: it asks for the move into the
// collection's bucket rather than promoting in place. Without a mover it is
// skipped, as before.
func TestHandleRoutesAnEventFromAnotherBucket(t *testing.T) {
	bound := statemachine.Placement{BackendID: "be-1", Bucket: "dedicated"}
	for name, tc := range map[string]struct {
		mover    SourceMover
		wantLate bool
	}{
		"moves late writes": {noMove{}, true},
		"no mover wired":    {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			p, late := &placingPromoter{}, &lateRecorder{}
			h := &PromoteHandler{Lookup: boundLookup(bound.BackendID, bound.Bucket), Transitioner: p, Logger: zap.NewNop(),
				Late: late, Mover: tc.mover}
			if err := h.Handle(context.Background(), lateEvent("shared")); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if late.called != tc.wantLate {
				t.Fatalf("late write asked = %v, want %v", late.called, tc.wantLate)
			}
			if p.placed != nil {
				t.Errorf("promoted in place at %+v; an event from another bucket never is", p.placed)
			}
			if tc.wantLate && (late.w.FromBucket != "shared" || late.w.To != bound || late.w.Sequencer != "0001") {
				t.Errorf("late write = %+v, want from shared into %+v with the event's sequencer", late.w, bound)
			}
		})
	}
}

// The late write is decided on when the store says the write was made. A
// time the adapter took from its own clock says only when the event arrived,
// and reaches the decision as unknown.
func TestHandlePassesOnlyAReportedWriteTime(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		reported bool
		want     time.Time
	}{
		"reported by the store": {true, at},
		"the ingest's clock":    {false, time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			late := &lateRecorder{}
			h := &PromoteHandler{Lookup: boundLookup("be-1", "dedicated"), Transitioner: &placingPromoter{}, Logger: zap.NewNop(),
				Late: late, Mover: noMove{}}
			ev := lateEvent("shared")
			ev.Time, ev.TimeReported = at, tc.reported
			if err := h.Handle(context.Background(), ev); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if !late.w.WrittenAt.Equal(tc.want) {
				t.Fatalf("late write's time = %v, want %v", late.w.WrittenAt, tc.want)
			}
		})
	}
}

// A promote the rebind refused after the bucket check is a late source write
// too: the move targets the bucket the refusal names.
func TestHandleMovesAWriteTheRebindOvertook(t *testing.T) {
	bound := statemachine.Placement{BackendID: "be-1", Bucket: "dedicated"}
	p := &placingPromoter{err: &statemachine.BindingMovedError{Placed: statemachine.Placement{BackendID: "be-1", Bucket: "shared"}, Bound: bound}}
	late := &lateRecorder{}
	h := &PromoteHandler{Lookup: boundLookup("be-1", "shared"), Transitioner: p, Logger: zap.NewNop(), Late: late, Mover: noMove{}}
	if err := h.Handle(context.Background(), lateEvent("shared")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !late.called || late.w.To != bound {
		t.Fatalf("late write = %+v (asked %v), want a move into %+v", late.w, late.called, bound)
	}
}

// What the late write answers decides the event: a skip, an unrelated
// bucket, or bytes already gone are acknowledged — a redelivery could not
// change them — while any other failure is retried.
func TestHandleSettlesALateWritesOutcome(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		wantRetry bool
	}{
		"moved":              {nil, false},
		"not migrated from":  {statemachine.ErrNotMigratedFrom, false},
		"skipped":            {&statemachine.LateWriteSkippedError{Reason: statemachine.LateSkipWrittenSinceRebind}, false},
		"source copy gone":   {statemachine.ErrLateWriteGone, false},
		"rebound again":      {&statemachine.BindingMovedError{}, true},
		"the database fails": {errors.New("connection reset"), true},
	} {
		t.Run(name, func(t *testing.T) {
			h := &PromoteHandler{Lookup: boundLookup("be-1", "dedicated"), Transitioner: &placingPromoter{}, Logger: zap.NewNop(),
				Late: &lateRecorder{err: tc.err}, Mover: noMove{}}
			err := h.Handle(context.Background(), lateEvent("shared"))
			if (err != nil) != tc.wantRetry {
				t.Fatalf("Handle = %v, want retried %v", err, tc.wantRetry)
			}
		})
	}
}

// A delete event from another bucket is never a late write: the source's
// copies are deleted by the migration's own cleanup.
func TestHandleSkipsADeleteEventFromAnotherBucket(t *testing.T) {
	late := &lateRecorder{}
	h := &PromoteHandler{Lookup: boundLookup("be-1", "dedicated"), Transitioner: &placingPromoter{}, Logger: zap.NewNop(),
		Late: late, Mover: noMove{}}
	ev := lateEvent("shared")
	ev.Type = EventTypeDeleted
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if late.called {
		t.Fatal("a delete event from another bucket was taken for a late write")
	}
}

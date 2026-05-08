package eventingest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeDedup is a minimal in-memory DedupStore for the worker test.
// First call for a given event_id returns it (claimed); subsequent
// calls return pgx.ErrNoRows (the same error sqlc returns when ON
// CONFLICT DO NOTHING fired).
type fakeDedup struct {
	seen   map[string]bool
	failOn string
}

func (f *fakeDedup) ClaimIngestedEvent(_ context.Context, eventID, _, _ string, _ *string) (string, error) {
	if eventID == f.failOn {
		return "", errors.New("simulated db error")
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[eventID] {
		return "", pgx.ErrNoRows
	}
	f.seen[eventID] = true
	return eventID, nil
}

type recordingHandler struct {
	calls []CloudEvent
	err   error
}

func (r *recordingHandler) Handle(_ context.Context, ev CloudEvent) error {
	r.calls = append(r.calls, ev)
	return r.err
}

func TestWorker_DedupSkipsDuplicate(t *testing.T) {
	dedup := &fakeDedup{}
	handler := &recordingHandler{}
	w := &Worker{Driver: nil /* unused for deliver */, Dedup: dedup, Handler: handler}

	ev := CloudEvent{ID: "evt-1", Type: EventTypeUploaded, Source: "test"}

	if err := w.deliver(context.Background(), ev); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := w.deliver(context.Background(), ev); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(handler.calls) != 1 {
		t.Errorf("handler called %d times, want 1 (dedup skipped second)", len(handler.calls))
	}
}

func TestWorker_RejectsEmptyID(t *testing.T) {
	w := &Worker{Dedup: &fakeDedup{}, Handler: &recordingHandler{}}
	err := w.deliver(context.Background(), CloudEvent{ID: ""})
	if err == nil {
		t.Error("empty id must fail (closed-by-default)")
	}
}

func TestWorker_HandlerErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	w := &Worker{
		Dedup:   &fakeDedup{},
		Handler: &recordingHandler{err: wantErr},
	}
	err := w.deliver(context.Background(), CloudEvent{ID: "evt-1", Type: EventTypeUploaded})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestWorker_DedupClaimDBErrorPropagates(t *testing.T) {
	w := &Worker{
		Dedup:   &fakeDedup{failOn: "evt-1"},
		Handler: &recordingHandler{},
	}
	err := w.deliver(context.Background(), CloudEvent{ID: "evt-1", Type: EventTypeUploaded})
	if err == nil {
		t.Error("expected dedup-claim DB error to propagate")
	}
}

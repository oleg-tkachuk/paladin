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
	seen       map[string]bool
	failOn     string
	releaseErr error
	// releasedLive records whether each release ran on a context that was
	// still live, which the worker must arrange even when delivery's ended.
	releasedLive []bool
}

func (f *fakeDedup) ReleaseIngestedEvent(ctx context.Context, eventID, _ string) error {
	f.releasedLive = append(f.releasedLive, ctx.Err() == nil)
	if f.releaseErr != nil {
		return f.releaseErr
	}
	delete(f.seen, eventID)
	return nil
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

	if err := w.Deliver(context.Background(), ev); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := w.Deliver(context.Background(), ev); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(handler.calls) != 1 {
		t.Errorf("handler called %d times, want 1 (dedup skipped second)", len(handler.calls))
	}
}

func TestWorker_RejectsEmptyID(t *testing.T) {
	w := &Worker{Dedup: &fakeDedup{}, Handler: &recordingHandler{}}
	err := w.Deliver(context.Background(), CloudEvent{ID: ""})
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
	err := w.Deliver(context.Background(), CloudEvent{ID: "evt-1", Type: EventTypeUploaded})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestWorker_DedupClaimDBErrorPropagates(t *testing.T) {
	w := &Worker{
		Dedup:   &fakeDedup{failOn: "evt-1"},
		Handler: &recordingHandler{},
	}
	err := w.Deliver(context.Background(), CloudEvent{ID: "evt-1", Type: EventTypeUploaded})
	if err == nil {
		t.Error("expected dedup-claim DB error to propagate")
	}
}

// failOnce fails its first call and succeeds after, like a promote that hit a
// transient database error.
type failOnce struct {
	calls  int
	cancel context.CancelFunc
}

func (f *failOnce) Handle(context.Context, CloudEvent) error {
	f.calls++
	if f.calls == 1 {
		if f.cancel != nil {
			f.cancel()
		}
		return errors.New("transient promote failure")
	}
	return nil
}

func TestWorker_RedeliveryAfterAFailedHandlerIsHandled(t *testing.T) {
	dedup := &fakeDedup{}
	handler := &failOnce{}
	w := &Worker{Dedup: dedup, Handler: handler}
	ev := CloudEvent{ID: "evt-retry", Type: EventTypeUploaded, Source: "test"}

	if err := w.Deliver(context.Background(), ev); err == nil {
		t.Fatal("first delivery: want the handler's error so the broker redelivers")
	}
	if err := w.Deliver(context.Background(), ev); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if handler.calls != 2 {
		t.Errorf("handler ran %d times, want 2: the redelivery was skipped as a duplicate", handler.calls)
	}
}

func TestWorker_ReleasesTheClaimEvenWhenDeliveryWasCancelled(t *testing.T) {
	dedup := &fakeDedup{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := &failOnce{cancel: cancel}
	w := &Worker{Dedup: dedup, Handler: handler}

	_ = w.Deliver(ctx, CloudEvent{ID: "evt-cancel", Type: EventTypeUploaded, Source: "test"})
	if len(dedup.releasedLive) != 1 || !dedup.releasedLive[0] {
		t.Errorf("release ran on a live context: %v, want [true]", dedup.releasedLive)
	}
}

func TestWorker_AFailedReleaseKeepsTheHandlerError(t *testing.T) {
	handlerErr := errors.New("transient promote failure")
	dedup := &fakeDedup{releaseErr: errors.New("db down")}
	w := &Worker{Dedup: dedup, Handler: &recordingHandler{err: handlerErr}}

	err := w.Deliver(context.Background(), CloudEvent{ID: "evt-db", Type: EventTypeUploaded, Source: "test"})
	if !errors.Is(err, handlerErr) {
		t.Errorf("err = %v, want the handler's error", err)
	}
}

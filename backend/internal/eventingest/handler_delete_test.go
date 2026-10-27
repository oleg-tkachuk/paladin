package eventingest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// rowLookup finds one object row for any key.
type rowLookup struct {
	fakeLookup
	row sqlc.Object
}

func (r *rowLookup) LookupObjectByKey(context.Context, pgtype.UUID, string, string) (sqlc.LookupObjectByKeyRow, error) {
	return sqlc.LookupObjectByKeyRow{Object: r.row}, nil
}

type fakeProbe struct {
	found  bool
	err    error
	called bool
	gotID  uuid.UUID
}

func (p *fakeProbe) HeadByObjectID(_ context.Context, id uuid.UUID) (string, int64, string, string, bool, error) {
	p.called, p.gotID = true, id
	return "", 0, "", "", p.found, p.err
}

type fakeDeleter struct {
	err       error
	called    bool
	gotID     uuid.UUID
	gotRV     int64
	afterRuns bool
}

func (d *fakeDeleter) SoftDeleteInTx(ctx context.Context, id uuid.UUID, rv int64, onDeleted func(context.Context, pgx.Tx) error) error {
	d.called, d.gotID, d.gotRV = true, id, rv
	if d.err != nil {
		return d.err
	}
	d.afterRuns = true
	return onDeleted(ctx, nil)
}

type recordedEvents struct {
	events []worker.Event
}

func (r *recordedEvents) DispatchTx(_ context.Context, _ pgx.Tx, _ string, evt worker.Event) (int, error) {
	r.events = append(r.events, evt)
	return 1, nil
}

const deletedObjectVersion = 7

func deleteFixture(state sqlc.ObjectState) (uuid.UUID, *rowLookup, CloudEvent) {
	id := uuid.New()
	tenant := uuid.New()
	lookup := &rowLookup{row: sqlc.Object{
		ID:              pgtype.UUID{Bytes: id, Valid: true},
		State:           state,
		ResourceVersion: deletedObjectVersion,
	}}
	ev := CloudEvent{ID: "e", Type: EventTypeDeleted, Source: "s3://assets", SubjectFields: SubjectFields{
		TenantID: tenant.String(), Collection: "docs", Key: "a.txt",
	}}
	return id, lookup, ev
}

// Bytes that are gone from the store put an available object in the trash,
// guarded by the version just read, with the soft-delete event on the same tx.
func TestHandle_DeleteEventTrashesAnObjectWhoseBytesAreGone(t *testing.T) {
	id, lookup, ev := deleteFixture(sqlc.ObjectStateAVAILABLE)
	probe, deleter, events := &fakeProbe{}, &fakeDeleter{}, &recordedEvents{}
	h := &PromoteHandler{Lookup: lookup, Probe: probe, Deleter: deleter, Events: events, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if probe.gotID != id {
		t.Errorf("HEAD for %v, want %v", probe.gotID, id)
	}
	if !deleter.called || deleter.gotID != id || deleter.gotRV != deletedObjectVersion {
		t.Fatalf("soft delete called=%v id=%v version=%d, want %v at %d", deleter.called, deleter.gotID, deleter.gotRV, id, deletedObjectVersion)
	}
	if n := len(events.events); n != 1 {
		t.Fatalf("%d events, want 1", n)
	}
	got := events.events[0]
	if got.Type != string(EventTypeDeleted) {
		t.Errorf("event type = %q", got.Type)
	}
	want := map[string]any{
		"tenant_id": ev.SubjectFields.TenantID, "collection": "docs", "key": "a.txt",
		"object_id": id.String(), "mode": "soft", "source": eventSourceStorage, "event_source": "s3://assets",
	}
	for k, v := range want {
		if got.Payload[k] != v {
			t.Errorf("payload[%q] = %v, want %v", k, got.Payload[k], v)
		}
	}
}

// The physical key may name a newer object, or one whose bytes moved: while
// the bytes are where the row places them, the event changes nothing.
func TestHandle_DeleteEventLeavesAnObjectWhoseBytesAreThere(t *testing.T) {
	_, lookup, ev := deleteFixture(sqlc.ObjectStateAVAILABLE)
	deleter, events := &fakeDeleter{}, &recordedEvents{}
	h := &PromoteHandler{Lookup: lookup, Probe: &fakeProbe{found: true}, Deleter: deleter, Events: events, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if deleter.called || len(events.events) > 0 {
		t.Errorf("soft delete called=%v, events=%d; the bytes are still there", deleter.called, len(events.events))
	}
}

// A pending object's bytes are Paladin's own to discard; the store is not even
// asked.
func TestHandle_DeleteEventSkipsAnObjectThatIsNotAvailable(t *testing.T) {
	for _, state := range []sqlc.ObjectState{sqlc.ObjectStatePENDING} {
		t.Run(string(state), func(t *testing.T) {
			_, lookup, ev := deleteFixture(state)
			probe, deleter := &fakeProbe{}, &fakeDeleter{}
			h := &PromoteHandler{Lookup: lookup, Probe: probe, Deleter: deleter, Logger: zap.NewNop()}
			if err := h.Handle(context.Background(), ev); err != nil {
				t.Fatalf("Handle = %v", err)
			}
			if probe.called || deleter.called {
				t.Errorf("HEAD=%v soft delete=%v for a %s object", probe.called, deleter.called, state)
			}
		})
	}
}

// A store that cannot be asked, or a row that changed under the delete, is
// retried rather than settled.
func TestHandle_DeleteEventFailuresAreRetried(t *testing.T) {
	for name, tc := range map[string]struct {
		probe   *fakeProbe
		deleter *fakeDeleter
	}{
		"head fails":      {&fakeProbe{err: errors.New("timeout")}, &fakeDeleter{}},
		"version changed": {&fakeProbe{}, &fakeDeleter{err: statemachine.ErrConflict}},
	} {
		t.Run(name, func(t *testing.T) {
			_, lookup, ev := deleteFixture(sqlc.ObjectStateAVAILABLE)
			h := &PromoteHandler{Lookup: lookup, Probe: tc.probe, Deleter: tc.deleter, Logger: zap.NewNop()}
			if err := h.Handle(context.Background(), ev); err == nil {
				t.Fatal("Handle = nil, want an error the worker retries")
			}
			if tc.deleter.afterRuns {
				t.Error("the delete went ahead")
			}
		})
	}
}

// A handler wired without a probe only notes the event, as before.
func TestHandle_DeleteEventWithoutAProbeIsNoted(t *testing.T) {
	_, lookup, ev := deleteFixture(sqlc.ObjectStateAVAILABLE)
	deleter := &fakeDeleter{}
	h := &PromoteHandler{Lookup: lookup, Deleter: deleter, Logger: zap.NewNop()}
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if deleter.called {
		t.Error("deleted without asking the store")
	}
}

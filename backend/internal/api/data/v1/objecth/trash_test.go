package objecth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// trashSM is a state machine whose soft delete and restore answer err, and
// run their event callback first when err is nil.
type trashSM struct {
	fakeStateMachine
	err      error
	deleted  []uuid.UUID
	restored []uuid.UUID
}

func (f *trashSM) SoftDeleteInTx(ctx context.Context, id uuid.UUID, _ int64, onDeleted func(context.Context, pgx.Tx) error) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return onDeleted(ctx, nil)
}

func (f *trashSM) RestoreInTx(ctx context.Context, id uuid.UUID, onRestored func(context.Context, pgx.Tx) error) error {
	if f.err != nil {
		return f.err
	}
	f.restored = append(f.restored, id)
	return onRestored(ctx, nil)
}

// eventLog records the events dispatched on a transaction.
type eventLog struct{ events []worker.Event }

func (e *eventLog) RecordTx(_ context.Context, _ pgx.Tx, _ string, evt worker.Event) error {
	e.events = append(e.events, evt)
	return nil
}

func trashCtx(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
}

// A soft delete moves an available object to the trash with its event; a
// conflicting version is Aborted, any other failure of the transition
// Internal, and a public object has no trash at all.
func TestSoftDelete(t *testing.T) {
	tid, oid := uuid.New(), uuid.New()
	available := Object{ObjectID: oid, TenantID: tid, Collection: "docs", Key: "a.txt", State: statemachine.StateAvailable}
	for name, tc := range map[string]struct {
		obj    Object
		smErr  error
		want   connect.Code
		events int
	}{
		"trashed":         {available, nil, 0, 1},
		"version changed": {available, statemachine.ErrConflict, connect.CodeAborted, 0},
		"store failing":   {available, errors.New("db down"), connect.CodeInternal, 0},
		"public object": {Object{ObjectID: oid, TenantID: tid, Collection: "docs", Key: "a.txt",
			State: statemachine.StateAvailable, PublicURL: "https://cdn/a.txt"}, nil, connect.CodeFailedPrecondition, 0},
	} {
		t.Run(name, func(t *testing.T) {
			sm, events := &trashSM{err: tc.smErr}, &eventLog{}
			h := &Handler{repo: &purgeRepo{obj: tc.obj}, policy: allowAll{}, sm: sm, events: events}
			err := h.DeleteObject(trashCtx(tid), "docs", oid.String(), "1", false, false)
			if got := connect.CodeOf(err); err != nil && got != tc.want || err == nil && tc.want != 0 {
				t.Fatalf("err = %v, want code %v", err, tc.want)
			}
			if len(events.events) != tc.events {
				t.Fatalf("events = %v, want %d", events.events, tc.events)
			}
			if tc.events == 1 {
				evt := events.events[0]
				if evt.Type != eventtype.ObjectDeleted || evt.Payload[payloadMode] != deleteModeSoft ||
					evt.Payload[payloadObjectID] != oid.String() {
					t.Errorf("event = %+v, want a soft paladin.object.deleted for the object", evt)
				}
			}
		})
	}
}

// restoreRepo serves one trashed object, nothing live at its key, and the
// lookup and collision reads the test chooses to fail.
type restoreRepo struct {
	restoreParentStub
	collisionErr error
	collision    bool
	findCalls    int
	refindErr    error
}

func (r *restoreRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	r.findCalls++
	if r.findCalls > 1 && r.refindErr != nil {
		return Object{}, r.refindErr
	}
	return placedIn(r.obj, "backend-7", "bucket-7"), nil
}

func (r *restoreRepo) LiveCollision(context.Context, uuid.UUID, string, string) (bool, error) {
	return r.collision, r.collisionErr
}

// A restore takes a trashed object out of the trash with its event, unless
// the object is not trashed, has moved on, would land on a live object, or
// the store fails along the way.
func TestRestoreObject(t *testing.T) {
	tid, oid := uuid.New(), uuid.NewV7()
	trashed := Object{ObjectID: oid, TenantID: tid, Collection: "docs", Key: "a.txt", State: statemachine.StateDeleted, ResourceVersion: 4}
	for name, tc := range map[string]struct {
		repo   *restoreRepo
		smErr  error
		rv     string
		want   connect.Code
		events int
	}{
		"restored":          {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}}, nil, "4", 0, 1},
		"not in the trash":  {&restoreRepo{restoreParentStub: restoreParentStub{obj: Object{ObjectID: oid, State: statemachine.StateAvailable, ResourceVersion: 4}}}, nil, "4", connect.CodeFailedPrecondition, 0},
		"moved on":          {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}}, nil, "3", connect.CodeAborted, 0},
		"key taken":         {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}, collision: true}, nil, "4", connect.CodeFailedPrecondition, 0},
		"collision unknown": {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}, collisionErr: errors.New("db down")}, nil, "4", connect.CodeInternal, 0},
		"restore failing":   {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}}, errors.New("db down"), "4", connect.CodeInternal, 0},
		"re-read failing":   {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}, refindErr: errors.New("db down")}, nil, "4", connect.CodeInternal, 1},
		"no version given":  {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}}, nil, "", connect.CodeInvalidArgument, 0},
		"version not a num": {&restoreRepo{restoreParentStub: restoreParentStub{obj: trashed}}, nil, "x", connect.CodeInvalidArgument, 0},
	} {
		t.Run(name, func(t *testing.T) {
			sm, events := &trashSM{err: tc.smErr}, &eventLog{}
			h := &Handler{repo: tc.repo, policy: allowAll{}, sm: sm, events: events, storage: headStorage{}}
			got, err := h.RestoreObject(trashCtx(tid), "docs", oid.String(), tc.rv)
			if code := connect.CodeOf(err); err != nil && code != tc.want || err == nil && tc.want != 0 {
				t.Fatalf("err = %v, want code %v", err, tc.want)
			}
			if len(events.events) != tc.events {
				t.Fatalf("events = %v, want %d", events.events, tc.events)
			}
			if tc.want == 0 && (got == nil || got.ObjectID != oid || events.events[0].Type != eventtype.ObjectRestored) {
				t.Errorf("restored %+v with %v, want the object and paladin.object.restored", got, events.events)
			}
		})
	}
}

// A malformed object id is refused before anything is read.
func TestParseObjectName(t *testing.T) {
	id := uuid.New()
	for name, tc := range map[string]struct {
		collection, objectID string
		want                 connect.Code
	}{
		"named":            {"docs", id.String(), 0},
		"no collection":    {"", id.String(), connect.CodeInvalidArgument},
		"no object":        {"docs", "", connect.CodeInvalidArgument},
		"object not an id": {"docs", "nope", connect.CodeInvalidArgument},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseObjectName(tc.collection, tc.objectID)
			if code := connect.CodeOf(err); err != nil && code != tc.want || err == nil && tc.want != 0 {
				t.Fatalf("err = %v, want code %v", err, tc.want)
			}
			if err == nil && got != id {
				t.Errorf("id = %v, want %v", got, id)
			}
		})
	}
}

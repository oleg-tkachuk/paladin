package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// A batch reissued after its worker died meets the first run's work. These
// tests pin that each executor counts that work as succeeded — under
// already_applied, and logged — instead of reporting it as a failure.

const (
	msgAlreadyApplied = "batch item already applied; counted as succeeded"
	msgBatchFinished  = "batch finished"
)

func observedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// appliedLogs returns the object ids logged as already applied, with the
// reason each was logged under.
func appliedLogs(t *testing.T, logs *observer.ObservedLogs) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range logs.FilterMessage(msgAlreadyApplied).All() {
		if e.Level != zapcore.DebugLevel {
			t.Errorf("%q logged at %s, want debug", msgAlreadyApplied, e.Level)
		}
		f := e.ContextMap()
		out[f["object_id"].(string)] = f["reason"].(string)
	}
	return out
}

// outcome returns the counts the batch logged when it finished.
func outcome(t *testing.T, logs *observer.ObservedLogs) map[string]int64 {
	t.Helper()
	entries := logs.FilterMessage(msgBatchFinished).All()
	if len(entries) != 1 {
		t.Fatalf("logged %q %d times, want once", msgBatchFinished, len(entries))
	}
	if entries[0].Level != zapcore.InfoLevel {
		t.Errorf("%q logged at %s, want info", msgBatchFinished, entries[0].Level)
	}
	f := entries[0].ContextMap()
	out := map[string]int64{}
	for _, k := range []string{"total", "succeeded", "already_applied", "failed"} {
		out[k] = f[k].(int64)
	}
	return out
}

func wantOutcome(t *testing.T, logs *observer.ObservedLogs, total, succeeded, applied, failed int64) {
	t.Helper()
	got := outcome(t, logs)
	want := map[string]int64{"total": total, "succeeded": succeeded, "already_applied": applied, "failed": failed}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("logged %s = %d, want %d", k, got[k], v)
		}
	}
}

// ─── delete ────────────────────────────────────────────────────────────────

func TestBatchSoftDeleteCountsTrashedObjectsAsDone(t *testing.T) {
	tenant := uuid.New()
	trashed, live := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []objecth.Object{
		{ObjectID: trashed, Collection: "k", State: statemachine.StateDeleted, ResourceVersion: 4},
		{ObjectID: live, Collection: "k", State: statemachine.StateAvailable, ResourceVersion: 2},
	}}
	tr := newFakeTransitions()
	log, logs := observedLogger()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr, Logger: log}

	body, err := e.Execute(context.Background(),
		mkOp(t, tenant, batchh.BatchDeleteArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{trashed, live}}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp := decodeDeleteResp(t, body)
	if resp.Succeeded != 2 || resp.AlreadyApplied != 1 || resp.Failed != 0 {
		t.Fatalf("resp = %+v, want 2 succeeded, 1 already applied, 0 failed", resp)
	}
	if _, called := tr.softArgs[trashed]; called {
		t.Error("SoftDelete was called on an object already in the trash")
	}
	if _, called := tr.softArgs[live]; !called {
		t.Error("SoftDelete was not called on the live object")
	}
	if got := appliedLogs(t, logs); got[trashed.String()] != reasonAlreadyDeleted || len(got) != 1 {
		t.Errorf("already-applied logs = %v, want only %s", got, trashed)
	}
	wantOutcome(t, logs, 2, 2, 1, 0)
}

// A permanent delete of a trashed object still has bytes and a row to remove;
// the trash is not where it was asked to end.
func TestBatchPermanentDeleteStillErasesTrashedObjects(t *testing.T) {
	tenant := uuid.New()
	trashed := uuid.New()
	repo := &lookupRepo{objs: []objecth.Object{
		{ObjectID: trashed, Collection: "k", State: statemachine.StateDeleted, ResourceVersion: 5},
	}}
	perm := newFakePermanentDeleter()
	log, logs := observedLogger()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions(), Permanent: perm, Erasures: noErasures{}, Logger: log}

	body, err := e.Execute(context.Background(), mkOp(t, tenant,
		batchh.BatchDeleteArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{trashed}, Permanent: true}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp := decodeDeleteResp(t, body)
	if len(perm.calls) != 1 || resp.Succeeded != 1 || resp.AlreadyApplied != 0 {
		t.Fatalf("calls = %v, resp = %+v; want one hard delete, nothing already applied", perm.calls, resp)
	}
	if got := appliedLogs(t, logs); len(got) != 0 {
		t.Errorf("already-applied logs = %v, want none", got)
	}
}

// ─── restore ───────────────────────────────────────────────────────────────

func TestBatchRestoreCountsAvailableObjectsAsDone(t *testing.T) {
	tenant := uuid.New()
	restored, trashed := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []objecth.Object{
		{ObjectID: restored, Collection: "k", State: statemachine.StateAvailable},
		{ObjectID: trashed, Collection: "k", State: statemachine.StateDeleted},
	}}
	tr := newFakeTransitions()
	log, logs := observedLogger()
	e := &BatchRestoreExecutor{Objects: repo, Transitions: tr, Logger: log}

	body, err := e.Execute(context.Background(), mkOp(t, tenant,
		batchh.BatchRestoreObjectsArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{restored, trashed}}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var resp BatchRestoreResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Succeeded != 2 || resp.AlreadyApplied != 1 || resp.Failed != 0 {
		t.Fatalf("resp = %+v, want 2 succeeded, 1 already applied, 0 failed", resp)
	}
	if tr.restored[restored] {
		t.Error("Restore was called on an object already available")
	}
	if !tr.restored[trashed] {
		t.Error("Restore was not called on the trashed object")
	}
	if got := appliedLogs(t, logs); got[restored.String()] != reasonAlreadyRestored || len(got) != 1 {
		t.Errorf("already-applied logs = %v, want only %s", got, restored)
	}
	wantOutcome(t, logs, 2, 2, 1, 0)
}

// ─── copy ──────────────────────────────────────────────────────────────────

// occupiedCopyRepo refuses every destination row as taken and answers the
// lookup that follows with dst / dstErr.
type occupiedCopyRepo struct {
	limitsCopyRepo
	dst    objecth.Object
	dstErr error
}

func (*occupiedCopyRepo) CreateObject(context.Context, objecth.CreateObjectArgs) (objecth.Object, error) {
	return objecth.Object{}, &pgconn.PgError{Code: "23505"}
}

func (r *occupiedCopyRepo) FindByPath(context.Context, uuid.UUID, string, string) (objecth.Object, error) {
	return r.dst, r.dstErr
}

func TestBatchCopyOccupiedDestination(t *testing.T) {
	src := objecth.Object{
		ObjectID: uuid.New(), Collection: "src", State: statemachine.StateAvailable,
		Key: "a.png", ContentType: "image/png", SizeBytes: 10, ETag: "etag-a", Checksum: "sum-a",
	}
	copyOf := func(mut func(*objecth.Object)) objecth.Object {
		d := src
		d.ObjectID, d.Collection = uuid.New(), "dst"
		mut(&d)
		return d
	}
	errDB := errors.New("connection reset")

	for _, tc := range []struct {
		name       string
		dst        objecth.Object
		dstErr     error
		wantDone   bool
		wantReason string
	}{
		{name: "an earlier run's copy", dst: copyOf(func(*objecth.Object) {}), wantDone: true},
		{name: "different etag", dst: copyOf(func(d *objecth.Object) { d.ETag = "etag-b" }), wantReason: "different object"},
		{name: "different size", dst: copyOf(func(d *objecth.Object) { d.SizeBytes = 11 }), wantReason: "different object"},
		{name: "different checksum", dst: copyOf(func(d *objecth.Object) { d.Checksum = "sum-b" }), wantReason: "different object"},
		{name: "a write in progress", dst: copyOf(func(d *objecth.Object) { d.State = statemachine.StatePending }), wantReason: "write in progress"},
		{name: "a trashed object", dstErr: pgx.ErrNoRows, wantReason: "in the trash"},
		{name: "a failed lookup", dstErr: errDB, wantReason: errDB.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			repo := &occupiedCopyRepo{limitsCopyRepo: limitsCopyRepo{sources: []objecth.Object{src}}, dst: tc.dst, dstErr: tc.dstErr}
			tr := &copyFakeTransitioner{}
			log, logs := observedLogger()
			e := &BatchCopyExecutor{
				Objects: repo, Storage: &copyFakeStorage{}, Transitions: tr,
				PendingTTL: time.Minute, Limits: copyLimits, Pending: noAdoption{}, Operations: noAdoption{}, Logger: log,
			}
			meta, err := json.Marshal(batchh.BatchCopyArgs{
				TenantID: tenant, SrcCollection: "src", DstCollection: "dst", ObjectIDs: []uuid.UUID{src.ObjectID},
			})
			if err != nil {
				t.Fatal(err)
			}
			body, err := e.Execute(context.Background(), mkOpRaw(tenant, meta))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var resp BatchCopyResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatal(err)
			}
			if tr.promoteHit || tr.markFailedHit {
				t.Error("an occupied destination was promoted or marked failed")
			}
			if tc.wantDone {
				if resp.Succeeded != 1 || resp.AlreadyApplied != 1 || resp.Failed != 0 {
					t.Fatalf("resp = %+v, want counted as already copied", resp)
				}
				if got := appliedLogs(t, logs); got[src.ObjectID.String()] != reasonAlreadyCopied {
					t.Errorf("already-applied logs = %v, want %s", got, src.ObjectID)
				}
				wantOutcome(t, logs, 1, 1, 1, 0)
				return
			}
			if resp.Failed != 1 || resp.AlreadyApplied != 0 {
				t.Fatalf("resp = %+v, want one failure", resp)
			}
			if r := resp.Failures[0].Reason; !strings.Contains(r, tc.wantReason) || strings.Contains(r, "23505") {
				t.Errorf("reason = %q, want it to name %q and not the SQLSTATE", r, tc.wantReason)
			}
			if got := appliedLogs(t, logs); len(got) != 0 {
				t.Errorf("already-applied logs = %v, want none", got)
			}
			wantOutcome(t, logs, 1, 0, 0, 1)
		})
	}
}

// A create that fails for any reason but a taken key stays the create error.
func TestBatchCopyCreateErrorIsNotAnOccupiedKey(t *testing.T) {
	tenant := uuid.New()
	src := objecth.Object{ObjectID: uuid.New(), Collection: "src", State: statemachine.StateAvailable, Key: "a.png", ContentType: "image/png", SizeBytes: 10}
	repo := &failingCreateRepo{limitsCopyRepo: limitsCopyRepo{sources: []objecth.Object{src}}}
	e := &BatchCopyExecutor{
		Objects: repo, Storage: &copyFakeStorage{}, Transitions: &copyFakeTransitioner{},
		PendingTTL: time.Minute, Limits: copyLimits, Pending: noAdoption{}, Operations: noAdoption{},
	}
	meta, err := json.Marshal(batchh.BatchCopyArgs{TenantID: tenant, SrcCollection: "src", DstCollection: "dst", ObjectIDs: []uuid.UUID{src.ObjectID}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := e.Execute(context.Background(), mkOpRaw(tenant, meta))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var resp BatchCopyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Failed != 1 || !strings.Contains(resp.Failures[0].Reason, "create dst row") {
		t.Fatalf("resp = %+v, want the create error", resp)
	}
}

type failingCreateRepo struct{ limitsCopyRepo }

func (*failingCreateRepo) CreateObject(context.Context, objecth.CreateObjectArgs) (objecth.Object, error) {
	return objecth.Object{}, &pgconn.PgError{Code: "23503"}
}

func mkOpRaw(tenantID uuid.UUID, meta []byte) operationh.Operation {
	return operationh.Operation{TenantID: tenantID, Metadata: meta}
}

// tagsRepo serves the batch's objects and accepts every tag write.
type tagsRepo struct{ lookupRepo }

func (*tagsRepo) UpdateMetadata(_ context.Context, a objecth.UpdateMetadataArgs) (objecth.Object, error) {
	return objecth.Object{ObjectID: a.ObjectID}, nil
}

// Tags need no already-applied check — writing them again is the same
// result — but the batch still reports how it ended.
func TestBatchUpdateTagsLogsItsOutcome(t *testing.T) {
	tenant := uuid.New()
	known, ghost := uuid.New(), uuid.New()
	log, logs := observedLogger()
	e := &BatchUpdateTagsExecutor{
		Objects: &tagsRepo{lookupRepo{objs: []objecth.Object{{ObjectID: known, Collection: "k"}}}},
		Logger:  log,
	}
	if _, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchUpdateTagsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{known, ghost}, Tags: map[string]string{"env": "prod"},
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	wantOutcome(t, logs, 2, 1, 0, 1)
}

// A permanent delete reissued after its worker died finds the first run's
// erasures missing; the tombstones tell them from ids that never existed.
func TestBatchPermanentDeleteCountsErasedObjectsAsDone(t *testing.T) {
	tenant := uuid.New()
	erased, ghost, live := uuid.New(), uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []objecth.Object{{ObjectID: live, Collection: "k", ResourceVersion: 1}}}
	er := &fakeErasures{erased: map[uuid.UUID]bool{erased: true}}
	perm := newFakePermanentDeleter()
	log, logs := observedLogger()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions(), Permanent: perm, Erasures: er, Logger: log}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{erased, ghost, live}, Permanent: true,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp := decodeDeleteResp(t, body)
	if resp.Succeeded != 2 || resp.AlreadyApplied != 1 || resp.Failed != 1 {
		t.Fatalf("resp = %+v, want 2 succeeded (1 already applied) and the ghost failed", resp)
	}
	if resp.Failures[0].ObjectID != ghost.String() || resp.Failures[0].Reason != "not found" {
		t.Errorf("failure = %+v, want the ghost not found", resp.Failures[0])
	}
	if len(er.askedFor) != 2 {
		t.Errorf("asked about %v, want only the two missing ids", er.askedFor)
	}
	if got := appliedLogs(t, logs); got[erased.String()] != reasonAlreadyErased || len(got) != 1 {
		t.Errorf("already-applied logs = %v, want only %s", got, erased)
	}
	wantOutcome(t, logs, 3, 2, 1, 1)
}

// A soft delete never erases, so it does not ask; nor does a permanent one
// with nothing missing.
func TestBatchDeleteAsksForErasuresOnlyWhenItCould(t *testing.T) {
	tenant := uuid.New()
	id := uuid.New()
	for name, args := range map[string]batchh.BatchDeleteArgs{
		"soft, id missing":           {TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()}},
		"permanent, nothing missing": {TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}, Permanent: true},
	} {
		t.Run(name, func(t *testing.T) {
			er := &fakeErasures{}
			e := &BatchDeleteExecutor{
				Objects:     &lookupRepo{objs: []objecth.Object{{ObjectID: id, Collection: "k", ResourceVersion: 1}}},
				Transitions: newFakeTransitions(), Permanent: newFakePermanentDeleter(), Erasures: er,
			}
			if _, err := e.Execute(context.Background(), mkOp(t, tenant, args)); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if er.asked {
				t.Error("asked for erasures")
			}
		})
	}
}

func TestBatchPermanentDeleteFailsOnAnErasureLookupError(t *testing.T) {
	tenant := uuid.New()
	e := &BatchDeleteExecutor{
		Objects: &lookupRepo{}, Transitions: newFakeTransitions(),
		Permanent: newFakePermanentDeleter(), Erasures: &fakeErasures{err: errors.New("db down")},
	}
	_, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()}, Permanent: true,
	}))
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v, want the lookup error", err)
	}
}

func TestBatchPermanentDeleteNeedsAnErasureLookup(t *testing.T) {
	tenant := uuid.New()
	e := &BatchDeleteExecutor{Objects: &lookupRepo{}, Transitions: newFakeTransitions(), Permanent: newFakePermanentDeleter()}
	if _, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()}, Permanent: true,
	})); err == nil {
		t.Fatal("a permanent batch with no erasure lookup was accepted")
	}
}

// fakeAdoption answers liveness from a set and adoption with a fixed result,
// recording what it was asked.
type fakeAdoption struct {
	live      map[uuid.UUID]bool
	liveErr   error
	adopt     bool
	adoptErr  error
	asked     []uuid.UUID
	adoptions []adoptCall
}

type adoptCall struct {
	objectID, creator, adopter uuid.UUID
	deadline                   time.Time
}

func (f *fakeAdoption) IsLive(_ context.Context, id uuid.UUID) (bool, error) {
	f.asked = append(f.asked, id)
	return f.live[id], f.liveErr
}

func (f *fakeAdoption) AdoptPending(_ context.Context, _, objectID, creator, adopter uuid.UUID, deadline time.Time) (bool, error) {
	f.adoptions = append(f.adoptions, adoptCall{objectID, creator, adopter, deadline})
	return f.adopt, f.adoptErr
}

// countingStorage counts the copies a batch makes.
type countingStorage struct {
	copyFakeStorage
	copies int
}

func (s *countingStorage) CopyObject(context.Context, objecth.Location, objecth.Location) error {
	s.copies++
	return nil
}

// A copy cut off before its promote leaves its destination PENDING. A reissue
// adopts that row when — and only when — the operation that created it can no
// longer write it and it was made for an object like src.
func TestBatchCopyAdoptsAPendingDestinationFromADeadRun(t *testing.T) {
	const pendingTTL = time.Minute
	src := objecth.Object{
		ObjectID: uuid.New(), Collection: "src", State: statemachine.StateAvailable,
		Key: "a.png", ContentType: "image/png", SizeBytes: 10, ETag: "etag-a",
	}
	opID, deadRun, liveRun := uuid.New(), uuid.New(), uuid.New()
	pending := func(mut func(*objecth.Object)) objecth.Object {
		d := objecth.Object{
			ObjectID: uuid.New(), Collection: "dst", State: statemachine.StatePending,
			Key: src.Key, ContentType: src.ContentType, SizeBytes: src.SizeBytes, CreatedByOperation: deadRun,
		}
		mut(&d)
		return d
	}
	errDB := errors.New("connection reset")

	for _, tc := range []struct {
		name        string
		dst         objecth.Object
		adoption    fakeAdoption
		wantAdopted bool
		wantAsked   bool
		wantReason  string
	}{
		{name: "dead creator", dst: pending(func(*objecth.Object) {}), adoption: fakeAdoption{adopt: true},
			wantAdopted: true, wantAsked: true},
		{name: "an upload, not a copy", dst: pending(func(d *objecth.Object) { d.CreatedByOperation = uuid.Nil() }),
			wantReason: "write in progress"},
		{name: "this operation's own row", dst: pending(func(d *objecth.Object) { d.CreatedByOperation = opID }),
			wantReason: "write in progress"},
		{name: "another size", dst: pending(func(d *objecth.Object) { d.SizeBytes = 11 }),
			wantReason: "pending copy of a different object"},
		{name: "another type", dst: pending(func(d *objecth.Object) { d.ContentType = "text/plain" }),
			wantReason: "pending copy of a different object"},
		{name: "live creator", dst: pending(func(d *objecth.Object) { d.CreatedByOperation = liveRun }),
			adoption: fakeAdoption{live: map[uuid.UUID]bool{liveRun: true}, adopt: true}, wantAsked: true,
			wantReason: "write in progress"},
		{name: "liveness unknown", dst: pending(func(*objecth.Object) {}), adoption: fakeAdoption{liveErr: errDB},
			wantAsked: true, wantReason: errDB.Error()},
		{name: "another reissue adopted it first", dst: pending(func(*objecth.Object) {}), adoption: fakeAdoption{adopt: false},
			wantAsked: true, wantReason: "write in progress"},
		{name: "adoption failed", dst: pending(func(*objecth.Object) {}), adoption: fakeAdoption{adoptErr: errDB},
			wantAsked: true, wantReason: errDB.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			repo := &occupiedCopyRepo{limitsCopyRepo: limitsCopyRepo{sources: []objecth.Object{src}}, dst: tc.dst}
			storage := &countingStorage{}
			tr := &copyFakeTransitioner{}
			adoption := tc.adoption
			log, logs := observedLogger()
			e := &BatchCopyExecutor{
				Objects: repo, Storage: storage, Transitions: tr,
				PendingTTL: pendingTTL, Limits: copyLimits, Pending: &adoption, Operations: &adoption, Logger: log,
			}
			meta, err := json.Marshal(batchh.BatchCopyArgs{
				TenantID: tenant, SrcCollection: "src", DstCollection: "dst", ObjectIDs: []uuid.UUID{src.ObjectID},
			})
			if err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			op := mkOpRaw(tenant, meta)
			op.OperationID = opID
			body, err := e.Execute(context.Background(), op)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var resp BatchCopyResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatal(err)
			}
			if asked := len(adoption.asked) > 0; asked != tc.wantAsked {
				t.Errorf("asked for liveness = %v, want %v", asked, tc.wantAsked)
			}
			adopted := logs.FilterMessage("batch copy adopted a pending destination left by a stopped operation").All()

			if tc.wantAdopted {
				if resp.Succeeded != 1 || resp.AlreadyApplied != 0 || resp.Failed != 0 {
					t.Fatalf("resp = %+v, want one copy made now", resp)
				}
				if storage.copies != 1 || !tr.promoteHit {
					t.Errorf("copies = %d, promoted = %v; want the adopted row filled", storage.copies, tr.promoteHit)
				}
				a := adoption.adoptions[0]
				if a.objectID != tc.dst.ObjectID || a.creator != deadRun || a.adopter != opID {
					t.Errorf("adoption = %+v, want %s from %s to %s", a, tc.dst.ObjectID, deadRun, opID)
				}
				if a.deadline.Before(before.Add(pendingTTL)) {
					t.Errorf("deadline %v is not pushed a PendingTTL out", a.deadline)
				}
				if len(adopted) != 1 || adopted[0].ContextMap()["creator_operation_id"] != deadRun.String() {
					t.Errorf("adoption logs = %v, want one naming the dead run", adopted)
				}
				return
			}
			if resp.Failed != 1 || !strings.Contains(resp.Failures[0].Reason, tc.wantReason) {
				t.Fatalf("resp = %+v, want a failure naming %q", resp, tc.wantReason)
			}
			if storage.copies != 0 || tr.promoteHit {
				t.Errorf("copies = %d, promoted = %v; want the row left alone", storage.copies, tr.promoteHit)
			}
			if len(adopted) != 0 {
				t.Errorf("logged an adoption that did not happen: %v", adopted)
			}
		})
	}
}

// creatorRepo records the creator a batch copy stamps on its rows.
type creatorRepo struct {
	limitsCopyRepo
	creators []uuid.UUID
}

func (r *creatorRepo) CreateObject(_ context.Context, a objecth.CreateObjectArgs) (objecth.Object, error) {
	r.creators = append(r.creators, a.CreatedByOperation)
	return objecth.Object{ObjectID: uuid.New(), Key: a.Key}, nil
}

func TestBatchCopyStampsItsOperationOnTheRowsItCreates(t *testing.T) {
	tenant, opID := uuid.New(), uuid.New()
	src := objecth.Object{ObjectID: uuid.New(), Collection: "src", State: statemachine.StateAvailable, Key: "a.png", ContentType: "image/png", SizeBytes: 10}
	repo := &creatorRepo{limitsCopyRepo: limitsCopyRepo{sources: []objecth.Object{src}}}
	e := &BatchCopyExecutor{
		Objects: repo, Storage: &copyFakeStorage{}, Transitions: &copyFakeTransitioner{},
		PendingTTL: time.Minute, Limits: copyLimits, Pending: noAdoption{}, Operations: noAdoption{},
	}
	meta, err := json.Marshal(batchh.BatchCopyArgs{TenantID: tenant, SrcCollection: "src", DstCollection: "dst", ObjectIDs: []uuid.UUID{src.ObjectID}})
	if err != nil {
		t.Fatal(err)
	}
	op := mkOpRaw(tenant, meta)
	op.OperationID = opID
	if _, err := e.Execute(context.Background(), op); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.creators) != 1 || repo.creators[0] != opID {
		t.Errorf("creators = %v, want [%s]", repo.creators, opID)
	}
}

// Without the adoption seams a reissue would wait out every dead run's
// PENDING row, so a copy executor wired without them refuses to run.
func TestBatchCopyRefusesWithoutAdoptionDependencies(t *testing.T) {
	for name, e := range map[string]*BatchCopyExecutor{
		"no Pending":    {Objects: &copyFakeRepo{}, Storage: &copyFakeStorage{}, Transitions: &copyFakeTransitioner{}, PendingTTL: time.Minute, Limits: copyLimits, Operations: noAdoption{}},
		"no Operations": {Objects: &copyFakeRepo{}, Storage: &copyFakeStorage{}, Transitions: &copyFakeTransitioner{}, PendingTTL: time.Minute, Limits: copyLimits, Pending: noAdoption{}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.Execute(context.Background(), operationh.Operation{}); err == nil || !strings.Contains(err.Error(), "dependencies missing") {
				t.Fatalf("err = %v, want a wiring error", err)
			}
		})
	}
}

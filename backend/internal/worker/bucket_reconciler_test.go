package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// BucketReconciler already takes interfaces for everything it touches — the
// repo, the provisioner, the outbox producer — and had no test file at all.
// The BACKLOG entry that measured this package concluded its remaining
// branches "need a transaction and a failing dependency, so they belong in
// internal/integration". For this file that was wrong: the seam is already
// here, and none of what follows needs a database.
//
// What it does need is dependencies that fail in ONE place, because every
// branch below is about what happens to the row when one step of a
// multi-step reconcile does not land.

var errBackend = errors.New("backend unavailable")

type fakeBucketRepo struct {
	provisions []admindomain.BucketProvisionRow
	deletions  []admindomain.BucketProvisionRow

	readyCalls    []string
	provFailCalls []markFailure
	delFailCalls  []markFailure

	readyErr    error
	provFailErr error
	delFailErr  error
	getResult   admindomain.Bucket
	getErr      error
	deleteErr   error
	deleteTxN   int
	runInTxErr  error
	// closureErr is what the transaction body returned. The real RunInTx
	// rolls back on a non-nil error; capturing it here is how a test can see
	// that an inner failure actually reached the transaction boundary
	// instead of being swallowed inside the closure.
	closureErr error
	ranClosure bool
}

type markFailure struct {
	bucket   string
	terminal bool
	msg      string
}

func (f *fakeBucketRepo) ListPendingProvisions(context.Context, int32, int32) ([]admindomain.BucketProvisionRow, error) {
	return f.provisions, nil
}

func (f *fakeBucketRepo) ListPendingDeletions(context.Context, int32, int32) ([]admindomain.BucketProvisionRow, error) {
	return f.deletions, nil
}

func (f *fakeBucketRepo) MarkProvisionReady(_ context.Context, _, bucketName string) error {
	f.readyCalls = append(f.readyCalls, bucketName)
	return f.readyErr
}

func (f *fakeBucketRepo) MarkProvisionFailed(_ context.Context, _, bucketName string, terminal bool, msg string) error {
	f.provFailCalls = append(f.provFailCalls, markFailure{bucketName, terminal, msg})
	return f.provFailErr
}

func (f *fakeBucketRepo) MarkDeletionFailed(_ context.Context, _, bucketName string, terminal bool, msg string) error {
	f.delFailCalls = append(f.delFailCalls, markFailure{bucketName, terminal, msg})
	return f.delFailErr
}

// RunInTx hands the closure a nil pgx.Tx: nothing below it touches the tx,
// it only passes it along to GetTx / DeleteTx / DispatchTx, which are fakes.
func (f *fakeBucketRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if f.runInTxErr != nil {
		return f.runInTxErr
	}
	f.ranClosure = true
	f.closureErr = fn(ctx, nil)
	return f.closureErr
}

func (f *fakeBucketRepo) GetTx(context.Context, pgx.Tx, string, string) (admindomain.Bucket, error) {
	return f.getResult, f.getErr
}

func (f *fakeBucketRepo) DeleteTx(context.Context, pgx.Tx, string, string, int64) error {
	f.deleteTxN++
	return f.deleteErr
}

type fakeProvisioner struct {
	createErr error
	deleteErr error
	tagErr    error
	tagged    int
	created   int
	removed   int
}

func (f *fakeProvisioner) CreateBucket(context.Context, string, string, string) error {
	f.created++
	return f.createErr
}

func (f *fakeProvisioner) DeleteBucket(context.Context, string, string) error {
	f.removed++
	return f.deleteErr
}

func (f *fakeProvisioner) TagBucketOwner(context.Context, string, string, uuid.UUID) error {
	f.tagged++
	return f.tagErr
}

type fakeBucketEvents struct {
	dispatched []Event
	err        error
}

func (f *fakeBucketEvents) DispatchTx(_ context.Context, _ pgx.Tx, _ string, evt Event) (int, error) {
	f.dispatched = append(f.dispatched, evt)
	return 1, f.err
}

var (
	_ BucketProvisionRepo = (*fakeBucketRepo)(nil)
	_ BucketProvisioner   = (*fakeProvisioner)(nil)
	_ BucketEventProducer = (*fakeBucketEvents)(nil)
)

func newReconciler(repo *fakeBucketRepo, prov *fakeProvisioner) *BucketReconciler {
	return NewBucketReconciler(repo, prov, BucketReconcilerConfig{}, nil)
}

func provisionRow(owner uuid.UUID) admindomain.BucketProvisionRow {
	return admindomain.BucketProvisionRow{
		BackendID: "primary", BucketName: "b1", Region: "eu-central-1",
		OwnerTenantID: owner,
	}
}

// Cost-attribution tagging is documented as best-effort: "a backend that
// doesn't support PutBucketTagging must not block provisioning; the bucket
// is already usable". If that ever stops being best-effort, every bucket on
// such a backend stays pending forever while the backend already has it.
func TestBucketReconciler_TaggingFailureStillMarksReady(t *testing.T) {
	repo := &fakeBucketRepo{}
	prov := &fakeProvisioner{tagErr: errBackend}
	r := newReconciler(repo, prov)

	r.reconcileOne(context.Background(), provisionRow(uuid.New()))

	if prov.tagged != 1 {
		t.Errorf("tagging attempted %d times, want 1", prov.tagged)
	}
	if len(repo.readyCalls) != 1 {
		t.Errorf("bucket was not marked ready after a tagging failure — the "+
			"backend has the bucket and the row stays pending forever "+
			"(ready calls: %v, failures: %v)", repo.readyCalls, repo.provFailCalls)
	}
	if len(repo.provFailCalls) != 0 {
		t.Errorf("a best-effort tagging failure was recorded as a provision "+
			"failure: %v", repo.provFailCalls)
	}
}

// A shared bucket has no owner, and tagging it would attribute cost to the
// nil tenant.
func TestBucketReconciler_SharedBucketIsNotTagged(t *testing.T) {
	repo := &fakeBucketRepo{}
	prov := &fakeProvisioner{}
	r := newReconciler(repo, prov)

	r.reconcileOne(context.Background(), provisionRow(uuid.Nil))

	if prov.tagged != 0 {
		t.Errorf("a bucket with no owner was tagged %d times", prov.tagged)
	}
	if len(repo.readyCalls) != 1 {
		t.Errorf("ready calls %v", repo.readyCalls)
	}
}

// The backend created the bucket but the DB-side mark failed. The row must
// stay pending so the next tick converges — recording a failure here would
// burn the retry budget for a bucket that already exists.
func TestBucketReconciler_MarkReadyFailureRecordsNoFailure(t *testing.T) {
	repo := &fakeBucketRepo{readyErr: errBackend}
	r := newReconciler(repo, &fakeProvisioner{})

	r.reconcileOne(context.Background(), provisionRow(uuid.Nil))

	if len(repo.provFailCalls) != 0 {
		t.Errorf("a failed mark-ready was recorded as a provisioning failure "+
			"(%v) — the retry budget drains on a bucket the backend already has",
			repo.provFailCalls)
	}
}

// Terminal errors stop the retries; transient ones must not. Misclassifying
// in one direction retries forever against a name that can never be valid;
// in the other, one network blip dead-letters a bucket permanently.
func TestBucketReconciler_ClassifiesProvisionErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		terminal bool
	}{
		{"AccessDenied is terminal", errors.New("AccessDenied: no"), true},
		{"InvalidBucketName is terminal", errors.New("InvalidBucketName"), true},
		{"TooManyBuckets is terminal", errors.New("TooManyBuckets"), true},
		{"a timeout is transient", context.DeadlineExceeded, false},
		{"a cancel is transient", context.Canceled, false},
		{"an unknown error is transient", errBackend, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeBucketRepo{}
			r := newReconciler(repo, &fakeProvisioner{createErr: tc.err})

			r.reconcileOne(context.Background(), provisionRow(uuid.Nil))

			if len(repo.provFailCalls) != 1 {
				t.Fatalf("failure calls %v", repo.provFailCalls)
			}
			if got := repo.provFailCalls[0].terminal; got != tc.terminal {
				t.Errorf("terminal = %v, want %v", got, tc.terminal)
			}
		})
	}
}

// With no provisioner wired the row is deliberately marked TRANSIENT, so a
// later deploy that has one still converges. Marking it terminal would
// dead-letter every pending bucket in a dev process.
func TestBucketReconciler_NoProvisionerKeepsTheRowRetryable(t *testing.T) {
	repo := &fakeBucketRepo{}
	r := NewBucketReconciler(repo, nil, BucketReconcilerConfig{}, nil)

	r.reconcileOne(context.Background(), provisionRow(uuid.Nil))

	if len(repo.provFailCalls) != 1 {
		t.Fatalf("failure calls %v", repo.provFailCalls)
	}
	if repo.provFailCalls[0].terminal {
		t.Error("a missing provisioner dead-lettered the row; a deploy that " +
			"wires one would never pick it up again")
	}
}

// The delete path's ordering promise: the backend confirms first, and only
// then does the row go. A backend failure that still removed the row would
// leave a bucket nobody knows about, paid for and unreachable.
func TestBucketReconciler_BackendDeleteFailureKeepsTheRow(t *testing.T) {
	repo := &fakeBucketRepo{}
	events := &fakeBucketEvents{}
	r := newReconciler(repo, &fakeProvisioner{deleteErr: errors.New("BucketNotEmpty")})
	r.SetEventProducer(events)

	r.reconcileDeleteOne(context.Background(), provisionRow(uuid.New()))

	if repo.deleteTxN != 0 {
		t.Error("the row was removed although the backend still has the bucket")
	}
	if len(events.dispatched) != 0 {
		t.Error("paladin.bucket.deleted was announced for a bucket that still exists")
	}
	if len(repo.delFailCalls) != 1 || !repo.delFailCalls[0].terminal {
		t.Errorf("BucketNotEmpty must be terminal — retrying cannot help until "+
			"the contents go (%v)", repo.delFailCalls)
	}
}

// A concurrent replica already finished: the row is gone by the time this
// one reads it. That is success, not an error — and it must not announce a
// deletion the other replica already announced.
func TestBucketReconciler_AlreadyRemovedEmitsNothing(t *testing.T) {
	repo := &fakeBucketRepo{getErr: admindomain.ErrNotFound}
	events := &fakeBucketEvents{}
	r := newReconciler(repo, &fakeProvisioner{})
	r.SetEventProducer(events)

	r.reconcileDeleteOne(context.Background(), provisionRow(uuid.New()))

	if repo.deleteTxN != 0 {
		t.Error("a row that was already gone was deleted again")
	}
	if len(events.dispatched) != 0 {
		t.Error("the deletion was announced twice — once per replica")
	}
	if len(repo.delFailCalls) != 0 {
		t.Errorf("a concurrent completion was recorded as a failure: %v",
			repo.delFailCalls)
	}
}

// The happy path, and the shape of what subscribers receive.
func TestBucketReconciler_DeleteAnnouncesTheTerminalEvent(t *testing.T) {
	owner := uuid.New()
	repo := &fakeBucketRepo{getResult: admindomain.Bucket{
		BackendID: "primary", BucketName: "b1", OwnerTenantID: owner,
	}}
	events := &fakeBucketEvents{}
	r := newReconciler(repo, &fakeProvisioner{})
	r.SetEventProducer(events)

	r.reconcileDeleteOne(context.Background(), provisionRow(owner))

	if repo.deleteTxN != 1 {
		t.Fatalf("row deletions = %d, want 1", repo.deleteTxN)
	}
	if len(events.dispatched) != 1 {
		t.Fatalf("events = %d, want 1 — the bucket vanished with no "+
			"announcement, and a subscriber's last word was `.deleting`",
			len(events.dispatched))
	}
	evt := events.dispatched[0]
	if evt.Type != "paladin.bucket.deleted" {
		t.Errorf("event type %q", evt.Type)
	}
	if evt.TenantID != owner.String() {
		t.Errorf("event tenant %q, want %q", evt.TenantID, owner)
	}
	if evt.Payload["mode"] != "outbox" {
		t.Errorf("payload mode %v, want outbox — this marks the async "+
			"completion apart from the handler's immediate delete",
			evt.Payload["mode"])
	}
}

// The row removal and the announcement share one transaction, so an event
// write that fails has to reach the transaction boundary — that is what
// makes the removal roll back. Swallowed inside the closure it would commit
// the delete and the bucket would vanish with nobody told.
func TestBucketReconciler_EventFailureReachesTheTxBoundary(t *testing.T) {
	repo := &fakeBucketRepo{}
	events := &fakeBucketEvents{err: errBackend}
	r := newReconciler(repo, &fakeProvisioner{})
	r.SetEventProducer(events)

	r.reconcileDeleteOne(context.Background(), provisionRow(uuid.New()))

	if !repo.ranClosure {
		t.Fatal("the transaction body never ran")
	}
	if len(events.dispatched) != 1 {
		t.Fatalf("the event was never attempted: %v", events.dispatched)
	}
	if !errors.Is(repo.closureErr, errBackend) {
		t.Errorf("the transaction body returned %v — a failed announcement "+
			"that does not reach the boundary commits the row removal, and "+
			"the bucket disappears with nobody told", repo.closureErr)
	}
}

// Terminal and transient failures differ only in what they log — the row is
// marked either way, and the level is what tells an operator whether to go
// look. So the log IS the observable here, and asserting on it is the only
// way to hold the branch: inverting the mark-failed error check leaves every
// test above passing while the two outcomes stop being distinguishable.
func TestBucketReconciler_LogsTerminalAndTransientDifferently(t *testing.T) {
	// Both paths, because they are twins: the provision side and the delete
	// side each classify, each mark, each log at two levels. Covering one of
	// them leaves the other's identical branch unheld — which is exactly what
	// happened on the first pass, and a mutation of the delete path said so.
	for _, tc := range []struct {
		name  string
		del   bool
		err   error
		level zapcore.Level
		msg   string
	}{
		{"provision terminal", false, errors.New("AccessDenied"), zapcore.ErrorLevel,
			"bucket provisioning failed permanently"},
		{"provision transient", false, errBackend, zapcore.WarnLevel,
			"bucket provisioning failed (transient)"},
		{"delete terminal", true, errors.New("BucketNotEmpty"), zapcore.ErrorLevel,
			"bucket deletion failed permanently"},
		{"delete transient", true, errBackend, zapcore.WarnLevel,
			"bucket deletion failed (transient)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			repo := &fakeBucketRepo{}
			prov := &fakeProvisioner{}
			if tc.del {
				prov.deleteErr = tc.err
			} else {
				prov.createErr = tc.err
			}
			r := NewBucketReconciler(repo, prov, BucketReconcilerConfig{}, zap.New(core))

			if tc.del {
				r.reconcileDeleteOne(context.Background(), provisionRow(uuid.Nil))
			} else {
				r.reconcileOne(context.Background(), provisionRow(uuid.Nil))
			}

			found := logs.FilterMessage(tc.msg).All()
			if len(found) != 1 {
				t.Fatalf("no %q line; got %v", tc.msg, messages(logs.All()))
			}
			if found[0].Level != tc.level {
				t.Errorf("logged at %v, want %v — the level is what tells an "+
					"operator whether this bucket will ever succeed",
					found[0].Level, tc.level)
			}
		})
	}
}

// When the mark-failed write itself fails there is nothing left to say about
// the backend error: the row was not updated, so the terminal/transient
// verdict was never recorded and announcing it would describe a state that
// does not exist.
func TestBucketReconciler_MarkFailedWriteFailureStopsThere(t *testing.T) {
	for _, tc := range []struct {
		name           string
		del            bool
		writeFailedMsg string
		verdictMsg     string
	}{
		{"provision", false, "mark-failed write failed",
			"bucket provisioning failed permanently"},
		{"delete", true, "mark-deletion-failed write failed",
			"bucket deletion failed permanently"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			repo := &fakeBucketRepo{}
			prov := &fakeProvisioner{}
			if tc.del {
				repo.delFailErr = errBackend
				prov.deleteErr = errors.New("BucketNotEmpty")
			} else {
				repo.provFailErr = errBackend
				prov.createErr = errors.New("AccessDenied")
			}
			r := NewBucketReconciler(repo, prov, BucketReconcilerConfig{}, zap.New(core))

			if tc.del {
				r.reconcileDeleteOne(context.Background(), provisionRow(uuid.Nil))
			} else {
				r.reconcileOne(context.Background(), provisionRow(uuid.Nil))
			}

			if n := len(logs.FilterMessage(tc.writeFailedMsg).All()); n != 1 {
				t.Fatalf("the failed write was not reported; got %v", messages(logs.All()))
			}
			if n := len(logs.FilterMessage(tc.verdictMsg).All()); n != 0 {
				t.Error("a verdict was announced although the row that carries " +
					"it was never written")
			}
		})
	}
}

func messages(entries []observer.LoggedEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Message
	}
	return out
}

package operations

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The (backend, bucket) lookupRepo resolves an open collection to.
const (
	testBackend = "backend-batch"
	testBucket  = "bucket-batch"
)

// The lines admitBatch logs.
const (
	lineBatchRefused     = "the bucket gate refuses the batch's bucket; the operation fails with its refusal"
	lineBatchLookupError = "bucket lookup before the batch's first item failed"
)

// The bucket states a batch can find its collection's bucket in when it runs,
// after the submit RPC admitted it.
var (
	openBucket         = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}
	drainingBucket     = bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}
	provisioningBucket = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}
	disabledBucket     = bucketgate.State{ProvisionState: bucketgate.ProvisionReady}
)

// gatedBatch is one batch executor, run against a repository whose bucket is
// in a given state; touched reports whether it wrote any item.
type gatedBatch struct {
	name   string
	access bucketgate.Access
	run    func(t *testing.T, repo *tagRepo) (touched bool, err error)
}

func gatedBatches(id uuid.UUID) []gatedBatch {
	tenant := uuid.New()
	available := objecth.Object{ObjectID: id, Collection: "k", State: statemachine.StateAvailable, ResourceVersion: 1}
	trashed := objecth.Object{ObjectID: id, Collection: "k", State: statemachine.StateDeleted, ResourceVersion: 1}
	return []gatedBatch{
		{"soft delete", bucketgate.MetadataWrite, func(t *testing.T, repo *tagRepo) (bool, error) {
			t.Helper()
			repo.objs = []objecth.Object{available}
			tr := newFakeTransitions()
			_, err := (&BatchDeleteExecutor{Objects: repo, Transitions: tr}).Execute(context.Background(),
				mkOp(t, tenant, batchh.BatchDeleteArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}}))
			return len(tr.softArgs) > 0, err
		}},
		{"permanent delete", bucketgate.BytesWrite, func(t *testing.T, repo *tagRepo) (bool, error) {
			t.Helper()
			repo.objs = []objecth.Object{available}
			pd := newFakePermanentDeleter()
			_, err := (&BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions(), Permanent: pd, Erasures: noErasures{}}).
				Execute(context.Background(), mkOp(t, tenant, batchh.BatchDeleteArgs{
					TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}, Permanent: true,
				}))
			return len(pd.calls) > 0, err
		}},
		{"restore", bucketgate.MetadataWrite, func(t *testing.T, repo *tagRepo) (bool, error) {
			t.Helper()
			repo.objs = []objecth.Object{trashed}
			tr := newFakeTransitions()
			_, err := (&BatchRestoreExecutor{Objects: repo, Transitions: tr}).Execute(context.Background(),
				mkOp(t, tenant, batchh.BatchRestoreObjectsArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}}))
			return len(tr.restored) > 0, err
		}},
		{"update tags", bucketgate.MetadataWrite, func(t *testing.T, repo *tagRepo) (bool, error) {
			t.Helper()
			repo.objs = []objecth.Object{available}
			_, err := (&BatchUpdateTagsExecutor{Objects: repo}).Execute(context.Background(),
				mkOp(t, tenant, batchh.BatchUpdateTagsArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}, Tags: map[string]string{"a": "b"}}))
			return len(repo.updArgs) > 0, err
		}},
	}
}

// Each executor asks the gate again when it runs, with its own access, before
// its first item. A bucket whose state changed since the submit and that the
// gate now refuses fails the operation with the gate's refusal and writes
// nothing; a metadata batch still runs on a draining or provisioning bucket,
// where a permanent delete, a write of bytes, is refused.
func TestBatchExecutorsAskTheBucketGateWhenTheyRun(t *testing.T) {
	for _, st := range []struct {
		name  string
		state bucketgate.State
		// refuses names the error each access is refused with; a missing
		// access is let through.
		refuses map[bucketgate.Access]error
	}{
		{"open", openBucket, nil},
		{"draining", drainingBucket, map[bucketgate.Access]error{bucketgate.BytesWrite: bucketgate.ErrBackendReadOnly}},
		{"provisioning", provisioningBucket, map[bucketgate.Access]error{bucketgate.BytesWrite: bucketgate.ErrBucketProvisioning}},
		{"disabled", disabledBucket, map[bucketgate.Access]error{
			bucketgate.BytesWrite: bucketgate.ErrBackendDisabled, bucketgate.MetadataWrite: bucketgate.ErrBackendDisabled,
		}},
	} {
		for _, b := range gatedBatches(uuid.New()) {
			t.Run(b.name+"/"+st.name, func(t *testing.T) {
				state := st.state
				repo := &tagRepo{lookupRepo: lookupRepo{bucketState: &state}}
				touched, err := b.run(t, repo)
				if len(repo.gotAccess) != 1 || repo.gotAccess[0] != b.access {
					t.Fatalf("gate asked about %v, want once about %v", repo.gotAccess, b.access)
				}
				want := st.refuses[b.access]
				if want == nil {
					if err != nil {
						t.Fatalf("Execute = %v, want the batch to run", err)
					}
					if !touched {
						t.Fatal("the batch wrote nothing")
					}
					return
				}
				if !errors.Is(err, want) || !bucketgate.Refused(err) {
					t.Fatalf("Execute = %v, want the refusal %v", err, want)
				}
				if touched {
					t.Fatal("a refused batch wrote an item")
				}
			})
		}
	}
}

// A refusal is logged at Warn with the operation, the collection, the access
// and the gate's reason; a lookup the store could not answer is a failure,
// logged at Error, and not a refusal.
func TestAdmitBatchLogsByWhatItIs(t *testing.T) {
	op := operationh.Operation{OperationID: uuid.New(), TenantID: uuid.New(), Type: "BatchUpdateTags"}
	disabled := disabledBucket
	for _, tc := range []struct {
		name    string
		repo    *lookupRepo
		line    string
		level   zapcore.Level
		refused bool
	}{
		{"refused", &lookupRepo{bucketState: &disabled}, lineBatchRefused, zapcore.WarnLevel, true},
		{"store unreachable", &lookupRepo{lookupErr: errors.New("connection reset")}, lineBatchLookupError, zapcore.ErrorLevel, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			err := admitBatch(context.Background(), tc.repo, zap.New(core), op, "k", bucketgate.MetadataWrite)
			if err == nil || bucketgate.Refused(err) != tc.refused {
				t.Fatalf("admitBatch = %v, want an error refused=%v", err, tc.refused)
			}
			lines := logs.FilterMessage(tc.line).All()
			if len(lines) != 1 || lines[0].Level != tc.level {
				t.Fatalf("want one %v %q line, got %v", tc.level, tc.line, logs.All())
			}
			fields := lines[0].ContextMap()
			if fields["operation_id"] != op.OperationID.String() || fields["collection"] != "k" ||
				fields["access"] != bucketgate.MetadataWrite.String() || fields["tenant_id"] != op.TenantID.String() {
				t.Errorf("fields = %v", fields)
			}
		})
	}
}

// cancellingExecutor stops the worker while it runs, as a shutdown does, and
// returns the cancellation.
type cancellingExecutor struct{ stop context.CancelFunc }

func (e cancellingExecutor) Execute(ctx context.Context, _ operationh.Operation) ([]byte, error) {
	e.stop()
	return nil, context.Canceled
}

// An executor cut short by the worker's shutdown is logged at Warn, not as a
// failure at Error, and the operation is still recorded.
func TestRunnerLogsAShutdownAtWarn(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	repo := newMemRepo()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	r := &Runner{Repo: repo, Executors: map[string]Executor{"X": cancellingExecutor{stop: stop}}, Logger: zap.New(core)}
	id := uuid.New()
	r.runOne(ctx, operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})

	if got := repo.final[id]; got.State != operationh.StateFailed {
		t.Fatalf("operation state = %s, want FAILED", got.State)
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Errorf("%d Error lines for a shutdown; got %v", n, logs.All())
	}
	if logs.FilterMessage("executor stopped by the worker's shutdown").Len() != 1 {
		t.Errorf("no shutdown line; got %v", logs.All())
	}
}

// The runner records a gate refusal with the refusal's reason as the error
// code, logged at Warn; any other executor failure is EXEC_FAILED at Error.
func TestRunnerRecordsAGateRefusalByItsReason(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  string
		level zapcore.Level
	}{
		{"refused", errors.Join(errors.New("collection k"), bucketgate.ErrBackendDisabled),
			bucketgate.ErrBackendDisabled.Reason.String(), zapcore.WarnLevel},
		{"failed", errors.New("boom"), "EXEC_FAILED", zapcore.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			repo := newMemRepo()
			r := &Runner{Repo: repo, Executors: map[string]Executor{"X": &stubExecutor{err: tc.err}}, Logger: zap.New(core)}
			id := uuid.New()
			repo.enqueue(operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
			r.drain(context.Background())

			got := repo.final[id]
			if got.State != operationh.StateFailed || got.ErrorCode != tc.code {
				t.Fatalf("operation = %s/%q, want FAILED/%q", got.State, got.ErrorCode, tc.code)
			}
			var seen bool
			for _, e := range logs.All() {
				if e.Level == tc.level && e.Level >= zapcore.WarnLevel {
					seen = true
				}
			}
			if !seen {
				t.Errorf("no %v line; got %v", tc.level, logs.All())
			}
		})
	}
}

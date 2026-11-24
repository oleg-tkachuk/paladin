package worker

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

func TestSplitBucketName(t *testing.T) {
	cases := map[string]struct {
		backend, bucket string
		errish          bool
	}{
		"storageBackends/primary/buckets/archive": {backend: "primary", bucket: "archive"},
		"":          {errish: true},
		"buckets/x": {errish: true},
	}
	for in, want := range cases {
		gotBackend, gotBucket, err := splitBucketName(in)
		if want.errish {
			if err == nil {
				t.Errorf("%q: expected error", in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", in, err)
		}
		if gotBackend != want.backend || gotBucket != want.bucket {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", in, gotBackend, gotBucket, want.backend, want.bucket)
		}
	}
}

func mustTick(t *testing.T, w *ReplicationWorker) {
	t.Helper()
	if err := w.tick(context.Background()); err != nil {
		t.Fatalf("tick = %v", err)
	}
}

// A queued object is copied to the same tenant, collection and key in the
// destination, at the row's size, and its replica recorded.
func TestReplicationCopiesTheObjectKeyAndSize(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("docs/report.pdf")
	rep := newScriptedReplicator(store, nil)
	mustTick(t, replicatingWorker(store, rep, nil))

	if len(rep.copied) != 1 {
		t.Fatalf("copies = %d, want 1", len(rep.copied))
	}
	c := rep.copied[0]
	want := ReplicationTarget{
		BackendID: gateDstBackend, BucketName: gateDstBucket,
		TenantID: store.tenant, Collection: passCollection, Key: obj.Row.Key,
	}
	if c.dst != want {
		t.Errorf("dst = %+v, want %+v", c.dst, want)
	}
	if c.src.BackendID != passBackend || c.src.BucketName != passBucket || c.src.Key != obj.Row.Key {
		t.Errorf("src = %+v", c.src)
	}
	if c.size != obj.Row.SizeBytes {
		t.Errorf("size = %d, want %d", c.size, obj.Row.SizeBytes)
	}
	r := store.row(obj.Row.Key)
	if r.due || r.claimed {
		t.Errorf("row due=%v claimed=%v after the copy, want settled", r.due, r.claimed)
	}
	wantCopy := ReplicaCopy{BucketID: fakeDstID, ObjectID: obj.Row.ObjectID, WrittenAt: obj.WrittenAt, SizeBytes: obj.Row.SizeBytes}
	if r.claim.Replica != wantCopy {
		t.Errorf("recorded replica %+v, want %+v", r.claim.Replica, wantCopy)
	}
}

// A bucket's objects written before replication was enabled are queued once,
// the first time the worker meets the bucket, and copied once.
func TestReplicationQueuesABucketsExistingObjectsOnce(t *testing.T) {
	store := newFakeReplicaStore()
	store.existing("a")
	store.existing("b")
	rep := newScriptedReplicator(store, nil)
	w := replicatingWorker(store, rep, nil)
	mustTick(t, w)
	mustTick(t, w)
	got := rep.copiedKeys()
	slices.Sort(got)
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("copied %v, want each existing object once", got)
	}
}

// A seed that fails defers the bucket: nothing claimed, the failure reported.
func TestReplicationDefersABucketItCannotSeed(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	store.seedErr = errors.New("pool closed")
	rep := newScriptedReplicator(store, nil)
	if err := replicatingWorker(store, rep, nil).tick(context.Background()); !errors.Is(err, store.seedErr) {
		t.Errorf("tick = %v, want %v", err, store.seedErr)
	}
	if store.claims != 0 || len(rep.copied) != 0 {
		t.Errorf("claims = %d, copies = %d under a failed seed", store.claims, len(rep.copied))
	}
}

// The held-object case the watermark could not handle: one object fails
// every attempt, and every other object is copied exactly once however many
// passes follow.
func TestReplicationCopiesEachObjectOnceWhileAnotherFails(t *testing.T) {
	store := newFakeReplicaStore()
	for _, k := range []string{"a", "b-held", "c", "d"} {
		store.write(k)
	}
	const passes = 4
	rep := newScriptedReplicator(store, map[string][]error{"b-held": repeat(errPermanentCopy, passes)})
	w := replicatingWorker(store, rep, nil)
	for range passes {
		if err := w.tick(context.Background()); !errors.Is(err, errPermanentCopy) {
			t.Fatalf("tick = %v, want the held object's failure", err)
		}
		store.retryAll()
	}
	got := rep.copiedKeys()
	slices.Sort(got)
	if !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Errorf("copied %v across %d passes, want each other object once", got, passes)
	}
	if n := rep.attempts["b-held"]; n != passes {
		t.Errorf("held object tried %d times, want once per pass (%d)", n, passes)
	}
}

// A failed row is put off by its own backoff, with its reason recorded.
func TestReplicationPutsAFailedObjectOffByItsBackoff(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	rep := newScriptedReplicator(store, map[string][]error{"a": repeat(errPermanentCopy, 2)})
	w := replicatingWorker(store, rep, nil)
	_ = w.tick(context.Background())
	r := store.row("a")
	if r.due || r.delay != replicationRetryBase || r.lastError != errPermanentCopy.Error() || r.claim.Attempts != 1 {
		t.Fatalf("after one failure: due=%v delay=%v reason=%q attempts=%d", r.due, r.delay, r.lastError, r.claim.Attempts)
	}
	store.retryAll()
	_ = w.tick(context.Background())
	if r := store.row("a"); r.delay != 2*replicationRetryBase || r.claim.Attempts != 2 {
		t.Errorf("after two failures: delay=%v attempts=%d, want the backoff doubled", r.delay, r.claim.Attempts)
	}
}

// A new byte write under an AVAILABLE object is copied again; a write that
// leaves the bytes as they were — a tag, a restore — is not.
func TestReplicationCopiesAgainOnlyAfterAByteWrite(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("a")
	rep := newScriptedReplicator(store, nil)
	core, logs := observer.New(zapcore.DebugLevel)
	w := replicatingWorker(store, rep, zap.New(core))
	mustTick(t, w)

	store.touch(obj.Row.ObjectID)
	mustTick(t, w)
	if n := len(rep.copied); n != 1 {
		t.Fatalf("copies after a metadata change = %d, want 1", n)
	}
	if logs.FilterMessage("replica already holds the object's current bytes").Len() != 1 {
		t.Error("the unchanged object was not logged as current")
	}
	if r := store.row("a"); r.due {
		t.Error("a current replica left its row due")
	}

	store.rewrite(obj.Row.ObjectID)
	mustTick(t, w)
	if n := len(rep.copied); n != 2 {
		t.Errorf("copies after a byte write = %d, want 2", n)
	}
}

// A write that lands while its location is being copied leaves the row due:
// the copy may hold the old bytes, so a later pass copies again.
func TestReplicationKeepsAWriteDuringTheCopyDue(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("a")
	rep := newScriptedReplicator(store, nil)
	once := false
	store.onReplicate = func() {
		if !once {
			once = true
			store.rewrite(obj.Row.ObjectID)
		}
	}
	w := replicatingWorker(store, rep, nil)
	mustTick(t, w)
	if n := len(rep.copied); n != 2 {
		t.Errorf("copies = %d, want the write during the first copy copied again", n)
	}
	if got := store.dueKeys(); len(got) != 0 {
		t.Errorf("due after the pass: %v", got)
	}
}

// A bucket with more due rows than one claim takes is copied whole in one
// tick.
func TestReplicationClaimsEveryBatchInOneTick(t *testing.T) {
	store := newFakeReplicaStore()
	total := 2*replicationClaimBatch + 1
	for i := range total {
		store.write(string(rune('a'+i%26)) + uuid.NewV7().String())
	}
	rep := newScriptedReplicator(store, nil)
	mustTick(t, replicatingWorker(store, rep, nil))
	if n := len(rep.copied); n != total {
		t.Errorf("copies = %d, want %d", n, total)
	}
}

// A transient failure ends the bucket's tick: the failed row is put off, the
// rest of its batch handed back due and untouched.
func TestReplicationStopsTheBucketOnATransientFailure(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	store.write("b")
	store.write("c")
	rep := newScriptedReplicator(store, map[string][]error{"a": {errTransientCopy}})
	if err := replicatingWorker(store, rep, nil).tick(context.Background()); !errors.Is(err, ErrCopyTransient) {
		t.Fatalf("tick = %v, want the transient failure", err)
	}
	if got := rep.copiedKeys(); len(got) != 0 {
		t.Errorf("copied %v after the backend failed", got)
	}
	if r := store.row("a"); r.due || r.claim.Attempts != 1 {
		t.Errorf("failed row due=%v attempts=%d, want put off", r.due, r.claim.Attempts)
	}
	for _, k := range []string{"b", "c"} {
		if r := store.row(k); !r.due || r.claimed || r.claim.Attempts != 0 {
			t.Errorf("%s due=%v claimed=%v attempts=%d, want handed back untouched", k, r.due, r.claimed, r.claim.Attempts)
		}
	}
}

// A replica that keeps mismatching is the object's own problem: the row is
// put off and the bucket carries on.
func TestReplicationCarriesOnPastAMismatch(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	store.write("b")
	rep := newScriptedReplicator(store, map[string][]error{"a": {errMismatchCopy}})
	if err := replicatingWorker(store, rep, nil).tick(context.Background()); !errors.Is(err, ErrCopyMismatch) {
		t.Fatalf("tick = %v, want the mismatch", err)
	}
	if got := rep.copiedKeys(); !slices.Equal(got, []string{"b"}) {
		t.Errorf("copied %v, want the object after the mismatch", got)
	}
	if r := store.row("a"); r.due || r.claim.Attempts != 1 {
		t.Errorf("mismatched row due=%v attempts=%d, want put off", r.due, r.claim.Attempts)
	}
}

// A not-found from the copy settles the row only once confirmed: the row
// unchanged and a fresh HEAD finding nothing.
func TestReplicationSettlesAConfirmedMissingSource(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("gone")
	rep := newScriptedReplicator(store, map[string][]error{"gone": {errSourceGone}})
	rep.missing = true
	mustTick(t, replicatingWorker(store, rep, nil))
	if r := store.row("gone"); r.due || r.claim.Attempts != 0 {
		t.Errorf("row due=%v attempts=%d, want settled", r.due, r.claim.Attempts)
	}
}

func TestReplicationRetriesAnUnconfirmedMissingSource(t *testing.T) {
	cases := map[string]func(*scriptedReplicator, *fakeReplSource, ReplicaObject){
		"head finds it": func(rep *scriptedReplicator, _ *fakeReplSource, _ ReplicaObject) { rep.missing = false },
		"head fails": func(rep *scriptedReplicator, _ *fakeReplSource, _ ReplicaObject) {
			rep.missing, rep.missingErr = true, errors.New("timeout")
		},
		"row changed": func(rep *scriptedReplicator, src *fakeReplSource, obj ReplicaObject) {
			rep.missing = true
			src.objStates = map[uuid.UUID]ReplicationObjectState{obj.Row.ObjectID: {
				Found: true, State: string(statemachine.StateAvailable), ResourceVersion: obj.Row.ResourceVersion + 1,
			}}
		},
		"row trashed": func(rep *scriptedReplicator, src *fakeReplSource, obj ReplicaObject) {
			rep.missing = true
			src.objStates = map[uuid.UUID]ReplicationObjectState{obj.Row.ObjectID: {Found: true, State: "DELETED"}}
		},
		"row gone": func(rep *scriptedReplicator, src *fakeReplSource, obj ReplicaObject) {
			rep.missing = true
			src.objStates = map[uuid.UUID]ReplicationObjectState{obj.Row.ObjectID: {}}
		},
		"row unreadable": func(rep *scriptedReplicator, src *fakeReplSource, _ ReplicaObject) {
			rep.missing = true
			src.objErr = errors.New("pool closed")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeReplicaStore()
			obj := store.write("gone")
			rep := newScriptedReplicator(store, map[string][]error{"gone": {errSourceGone}})
			w := replicatingWorker(store, rep, nil)
			setup(rep, w.Buckets.(*fakeReplSource), obj)
			if err := w.tick(context.Background()); !errors.Is(err, ErrObjectMissing) {
				t.Errorf("tick = %v, want the unconfirmed not-found", err)
			}
			if r := store.row("gone"); r.due || r.claim.Attempts != 1 {
				t.Errorf("row due=%v attempts=%d, want put off for a retry", r.due, r.claim.Attempts)
			}
		})
	}
}

// An object in the trash keeps its replica; its row settles without a copy.
func TestReplicationLeavesATrashedObjectsReplica(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("a")
	rep := newScriptedReplicator(store, nil)
	w := replicatingWorker(store, rep, nil)
	mustTick(t, w)
	store.setState(obj.Row.ObjectID, "DELETED")
	store.touch(obj.Row.ObjectID) // a queue entry from before the trash move
	mustTick(t, w)
	if n := len(rep.copied); n != 1 {
		t.Errorf("copies = %d, want none for a trashed object", n)
	}
	if r := store.row("a"); r == nil || r.due || r.claim.Replica.ObjectID != obj.Row.ObjectID {
		t.Errorf("row %+v, want settled with its replica kept", r)
	}
	store.setState(obj.Row.ObjectID, string(statemachine.StateAvailable))
	mustTick(t, w)
	if n := len(rep.copied); n != 1 {
		t.Errorf("copies after a restore = %d, want the kept replica reused", n)
	}
}

// A purged object's replica is deleted from the destination at its key, and
// its row dropped.
func TestReplicationDeletesAPurgedObjectsReplica(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("a")
	rep := newScriptedReplicator(store, nil)
	w := replicatingWorker(store, rep, nil)
	mustTick(t, w)
	store.purge(obj.Row.ObjectID)
	mustTick(t, w)
	want := ReplicationTarget{
		BackendID: gateDstBackend, BucketName: gateDstBucket,
		TenantID: store.tenant, Collection: passCollection, Key: "a",
	}
	if len(rep.deleted) != 1 || rep.deleted[0] != want {
		t.Errorf("deletes = %+v, want one at %+v", rep.deleted, want)
	}
	if len(rep.copied) != 1 || store.row("a") != nil {
		t.Errorf("copies = %d, row = %+v; want the row dropped", len(rep.copied), store.row("a"))
	}
}

// A purged object the worker never copied into the current destination —
// filtered out, or copied into one the bucket no longer names — is forgotten
// with no delete.
func TestReplicationDeletesOnlyAReplicaItWroteThere(t *testing.T) {
	cases := map[string]func(*fakeReplicaStore, *ReplicationWorker){
		"filtered out": func(_ *fakeReplicaStore, w *ReplicationWorker) {
			filtered(w, `key.startsWith("keep/")`)
		},
		"another destination": func(s *fakeReplicaStore, _ *ReplicationWorker) {
			s.mu.Lock()
			s.rows["a"].claim.Replica = ReplicaCopy{BucketID: fakeBucketID(gateDstBackend, "old-dr")}
			s.rows["a"].due = false
			s.seeded[fakeSrcID.Bytes] = true
			s.mu.Unlock()
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeReplicaStore()
			obj := store.write("a")
			rep := newScriptedReplicator(store, nil)
			w := replicatingWorker(store, rep, nil)
			setup(store, w)
			mustTick(t, w)
			store.purge(obj.Row.ObjectID)
			mustTick(t, w)
			if len(rep.deleted) != 0 || store.row("a") != nil {
				t.Errorf("deletes = %+v, row = %+v; want the row forgotten without a delete", rep.deleted, store.row("a"))
			}
		})
	}
}

// A delete that fails is retried by the row's backoff; a transient one ends
// the bucket's tick, a permanent one does not.
func TestReplicationRetriesAFailedDelete(t *testing.T) {
	cases := map[string]struct {
		err     error
		stopped bool
	}{
		"transient": {errTransientCopy, true},
		"permanent": {errPermanentCopy, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeReplicaStore()
			gone := store.write("a")
			rep := newScriptedReplicator(store, map[string][]error{deleteScriptKey("a"): {tc.err}})
			w := replicatingWorker(store, rep, nil)
			mustTick(t, w)
			store.purge(gone.Row.ObjectID)
			store.write("b")
			if err := w.tick(context.Background()); !errors.Is(err, tc.err) {
				t.Fatalf("tick = %v, want %v", err, tc.err)
			}
			if r := store.row("a"); r == nil || r.due || r.claim.Attempts != 1 {
				t.Fatalf("row %+v, want kept and put off", r)
			}
			if copiedB := slices.Contains(rep.copiedKeys(), "b"); copiedB == tc.stopped {
				t.Errorf("copied b = %v, want %v", copiedB, !tc.stopped)
			}
			store.retryAll()
			mustTick(t, w)
			if len(rep.deleted) != 1 || store.row("a") != nil {
				t.Errorf("after the retry: deletes = %d, row = %+v; want the replica deleted", len(rep.deleted), store.row("a"))
			}
		})
	}
}

// A new object written at a purged key takes its row over: the purge's
// delete is not run, and the new object is copied.
func TestReplicationCopiesANewObjectAtAPurgedKey(t *testing.T) {
	store := newFakeReplicaStore()
	old := store.write("a")
	rep := newScriptedReplicator(store, nil)
	w := replicatingWorker(store, rep, nil)
	mustTick(t, w)
	store.purge(old.Row.ObjectID)
	fresh := store.write("a")
	mustTick(t, w)
	if len(rep.deleted) != 0 || len(rep.copied) != 2 {
		t.Fatalf("deletes = %d, copies = %d; want the new object copied over the old replica", len(rep.deleted), len(rep.copied))
	}
	if r := store.row("a"); r == nil || r.claim.Replica.ObjectID != fresh.Row.ObjectID {
		t.Errorf("row %+v, want the new object's replica", r)
	}
}

// A dry run deletes nothing and leaves the purged row due.
func TestReplicationDryRunDeletesNothing(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("a")
	store.mu.Lock()
	store.rows["a"].claim.Replica = ReplicaCopy{BucketID: fakeDstID, ObjectID: obj.Row.ObjectID}
	store.mu.Unlock()
	store.purge(obj.Row.ObjectID)
	mustTick(t, replicatingWorker(store, nil, nil))
	if r := store.row("a"); r == nil || !r.due || r.claimed {
		t.Errorf("row %+v, want it handed back due", r)
	}
}

// A row whose object left the bucket — its collection rebound — is
// forgotten without a copy.
func TestReplicationForgetsAnObjectThatLeftTheBucket(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("a")
	store.mu.Lock()
	moved := store.objects[obj.Row.ObjectID]
	moved.BucketID = fakeBucketID(passBackend, "elsewhere")
	store.objects[obj.Row.ObjectID] = moved
	store.mu.Unlock()
	rep := newScriptedReplicator(store, nil)
	mustTick(t, replicatingWorker(store, rep, nil))
	if len(rep.copied) != 0 || store.row("a") != nil {
		t.Errorf("copies = %d, row = %+v; want the row forgotten", len(rep.copied), store.row("a"))
	}
}

// A copy is logged at Debug by object id alone; the key appears on failure
// lines only.
func TestReplicationLogsASuccessAtDebugWithoutTheKey(t *testing.T) {
	store := newFakeReplicaStore()
	obj := store.write("private/report.pdf")
	core, logs := observer.New(zapcore.DebugLevel)
	mustTick(t, replicatingWorker(store, newScriptedReplicator(store, nil), zap.New(core)))
	lines := logs.FilterMessage("replicated object").All()
	if len(lines) != 1 {
		t.Fatalf("success lines = %d, want 1", len(lines))
	}
	if lines[0].Level != zapcore.DebugLevel {
		t.Errorf("success logged at %v, want debug", lines[0].Level)
	}
	fields := lines[0].ContextMap()
	if _, ok := fields[logKeyKey]; ok {
		t.Errorf("success line carries the object key: %v", fields)
	}
	if fields[logKeyObjectID] != obj.Row.ObjectID.String() {
		t.Errorf("success line object_id = %v, want %s", fields[logKeyObjectID], obj.Row.ObjectID)
	}
}

// A failure line carries the key an operator needs.
func TestReplicationLogsAFailureWithTheKey(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("private/report.pdf")
	core, logs := observer.New(zapcore.WarnLevel)
	rep := newScriptedReplicator(store, map[string][]error{"private/report.pdf": {errPermanentCopy}})
	_ = replicatingWorker(store, rep, zap.New(core)).tick(context.Background())
	lines := logs.FilterMessage("failed to copy object; retrying it later").All()
	if len(lines) != 1 || lines[0].ContextMap()[logKeyKey] != "private/report.pdf" {
		t.Errorf("failure lines %v, want one naming the key", lines)
	}
}

// A dry run copies nothing and leaves every row due, after one claim.
func TestReplicationDryRunLeavesRowsDue(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	store.write("b")
	mustTick(t, replicatingWorker(store, nil, nil))
	if got := store.dueKeys(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("due after a dry run: %v, want both", got)
	}
	if store.claims != 1 {
		t.Errorf("claims = %d, want one batch for a dry run", store.claims)
	}
}

// A claim or object read that fails is reported, and an object read hands
// the claimed rows back.
func TestReplicationReportsAStoreFailure(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		store := newFakeReplicaStore()
		store.write("a")
		store.claimErr = errors.New("pool closed")
		if err := replicatingWorker(store, newScriptedReplicator(store, nil), nil).tick(context.Background()); !errors.Is(err, store.claimErr) {
			t.Errorf("tick = %v, want %v", err, store.claimErr)
		}
	})
	t.Run("objects", func(t *testing.T) {
		store := newFakeReplicaStore()
		store.write("a")
		store.objErr = errors.New("pool closed")
		rep := newScriptedReplicator(store, nil)
		if err := replicatingWorker(store, rep, nil).tick(context.Background()); !errors.Is(err, store.objErr) {
			t.Errorf("tick = %v, want %v", err, store.objErr)
		}
		if r := store.row("a"); r.claimed || !r.due {
			t.Errorf("row claimed=%v due=%v, want handed back due", r.claimed, r.due)
		}
	})
	t.Run("settle", func(t *testing.T) {
		store := newFakeReplicaStore()
		store.write("a")
		store.settleErr = errors.New("pool closed")
		if err := replicatingWorker(store, newScriptedReplicator(store, nil), nil).tick(context.Background()); !errors.Is(err, store.settleErr) {
			t.Errorf("tick = %v, want %v", err, store.settleErr)
		}
	})
}

// A worker stopped mid-copy reports no failure.
func TestReplicationStopsQuietlyWhenCancelled(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	store.write("b")
	ctx, cancel := context.WithCancel(context.Background())
	store.onReplicate = cancel
	rep := newScriptedReplicator(store, map[string][]error{"a": {context.Canceled}})
	if err := replicatingWorker(store, rep, nil).tick(ctx); err != nil {
		t.Errorf("tick = %v, want nil when the worker is stopping", err)
	}
	if got := rep.copiedKeys(); len(got) != 0 {
		t.Errorf("copied %v after the stop", got)
	}
}

// filtered sets the fixture bucket's replication filter and wires an
// evaluator.
func filtered(w *ReplicationWorker, expr string) *ReplicationWorker {
	src := w.Buckets.(*fakeReplSource)
	src.buckets[0].Replication.Filter = expr
	w.CELEvaluator = cel.NewEvaluator()
	return w
}

// The filter selects what is copied; an object it does not match is settled.
func TestReplicationCopiesOnlyWhatTheFilterMatches(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("keep/a")
	store.write("skip/b")
	rep := newScriptedReplicator(store, nil)
	mustTick(t, filtered(replicatingWorker(store, rep, nil), `key.startsWith("keep/")`))
	if got := rep.copiedKeys(); !slices.Equal(got, []string{"keep/a"}) {
		t.Errorf("copied %v, want only the object the filter matches", got)
	}
	if got := store.dueKeys(); len(got) != 0 {
		t.Errorf("due %v, want the filtered object settled", got)
	}
}

// An object the filter fails to evaluate on is neither copied nor settled.
func TestReplicationRetriesAFilterError(t *testing.T) {
	store := newFakeReplicaStore()
	store.write("a")
	rep := newScriptedReplicator(store, nil)
	w := filtered(replicatingWorker(store, rep, nil), `int(key) > 0`) // "a" is no int
	if err := w.tick(context.Background()); err == nil {
		t.Fatal("tick = nil, want the evaluation failure")
	}
	if len(rep.copied) != 0 {
		t.Errorf("copied %v on a filter that could not decide", rep.copiedKeys())
	}
	if r := store.row("a"); r.claim.Attempts != 1 {
		t.Errorf("attempts = %d, want the row put off", r.claim.Attempts)
	}
}

// A filter that does not compile, or one with no evaluator to run it, stops
// the bucket: copying everything would ignore it.
func TestReplicationRefusesAFilterItCannotRun(t *testing.T) {
	cases := map[string]func(*ReplicationWorker) *ReplicationWorker{
		"does not compile": func(w *ReplicationWorker) *ReplicationWorker { return filtered(w, "1 + 1") },
		"no evaluator": func(w *ReplicationWorker) *ReplicationWorker {
			w = filtered(w, `key.startsWith("a")`)
			w.CELEvaluator = nil
			return w
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeReplicaStore()
			store.write("a")
			rep := newScriptedReplicator(store, nil)
			core, logs := observer.New(zapcore.ErrorLevel)
			w := setup(replicatingWorker(store, rep, zap.New(core)))
			if err := w.tick(context.Background()); err == nil {
				t.Fatal("tick = nil, want the filter's failure")
			}
			if len(rep.copied) != 0 || store.claims != 0 {
				t.Errorf("copies = %d, claims = %d under a filter that cannot run", len(rep.copied), store.claims)
			}
			if logs.Len() != 1 {
				t.Errorf("error lines = %d, want 1", logs.Len())
			}
		})
	}
}

func TestReplicationRetryDelay(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, replicationRetryBase},
		{2, 2 * replicationRetryBase},
		{3, 4 * replicationRetryBase},
		{9, 256 * replicationRetryBase},
		{10, replicationRetryMax},
		{50, replicationRetryMax},
	}
	for _, tc := range cases {
		if got := replicationRetryDelay(tc.attempt); got != tc.want {
			t.Errorf("replicationRetryDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

package worker

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// The gate states a side of a replication meets.
var (
	replDisabled = bucketgate.State{ProvisionState: bucketgate.ProvisionReady}
	replDraining = bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}
	replPending  = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}
)

// The worker asks the gate Read on the source and BytesWrite on the
// destination. Each case disables or drains one side: a refused side defers
// the bucket — nothing claimed, every row still due, no failure — and the
// refusal is logged once though the tick meets the bucket twice. A draining
// source still serves reads, so it replicates.
func TestReplicationAsksTheGateOnBothSides(t *testing.T) {
	cases := []struct {
		name     string
		backend  string
		bucket   string
		state    bucketgate.State
		deferred bool
	}{
		{"source disabled", passBackend, passBucket, replDisabled, true},
		{"source draining", passBackend, passBucket, replDraining, false},
		{"destination disabled", gateDstBackend, gateDstBucket, replDisabled, true},
		{"destination draining", gateDstBackend, gateDstBucket, replDraining, true},
		{"destination provisioning", gateDstBackend, gateDstBucket, replPending, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeReplicaStore()
			store.write("a")
			rep := newScriptedReplicator(store, nil)
			core, logs := observer.New(zapcore.WarnLevel)
			w := replicatingWorker(store, rep, zap.New(core))
			src := w.Buckets.(*fakeReplSource)
			src.gateStates = map[string]bucketgate.State{tc.backend + "/" + tc.bucket: tc.state}
			// The same bucket twice in one tick: the refusal is logged once.
			src.buckets = append(src.buckets, src.buckets[0])

			if err := w.tick(context.Background()); err != nil {
				t.Fatalf("tick = %v, want nil: a deferral is not a failure", err)
			}
			if got := len(rep.copied) == 0; got != tc.deferred {
				t.Fatalf("copied %v, deferred = %v, want %v", rep.copiedKeys(), got, tc.deferred)
			}
			if !tc.deferred {
				return
			}
			if store.claims != 0 {
				t.Errorf("a deferred bucket claimed %d times", store.claims)
			}
			if r := store.row("a"); !r.due || r.claim.Attempts != 0 {
				t.Errorf("row due=%v attempts=%d, want it waiting untouched", r.due, r.claim.Attempts)
			}
			refusals := logs.FilterMessage("the bucket gate refuses this bucket; its work is deferred until the gate admits it").All()
			if len(refusals) != 1 {
				t.Fatalf("refusal lines = %d, want 1 per tick", len(refusals))
			}
			if got := refusals[0].ContextMap()["bucket_name"]; got != tc.bucket {
				t.Errorf("refusal names bucket %v, want %s", got, tc.bucket)
			}
		})
	}
}

// A refused bucket is taken by the first tick whose gate admits it, however
// long it waited: what it queued stays queued.
func TestReplicationTakesADeferredBucketOnceAdmitted(t *testing.T) {
	store := newFakeReplicaStore()
	store.existing("seeded")
	rep := newScriptedReplicator(store, nil)
	w := replicatingWorker(store, rep, nil)
	src := w.Buckets.(*fakeReplSource)
	src.gateStates = map[string]bucketgate.State{gateDstBackend + "/" + gateDstBucket: replDisabled}
	mustTick(t, w)
	store.write("written")
	mustTick(t, w)
	src.gateStates = nil
	mustTick(t, w)
	if got := rep.copiedKeys(); len(got) != 2 {
		t.Errorf("copied %v after the gate admitted the bucket, want both objects", got)
	}
}

// A destination with no bucket row, or whose state cannot be read, is a
// failure, and nothing is copied into it.
func TestReplicationRefusesADestinationItCannotRead(t *testing.T) {
	storeDown := errors.New("pool closed")
	cases := map[string]func(*fakeReplSource) error{
		"not registered": func(s *fakeReplSource) error {
			s.unknown = map[string]bool{gateDstBackend + "/" + gateDstBucket: true}
			return ErrReplicationDestinationUnknown
		},
		"state unreadable": func(s *fakeReplSource) error {
			s.gateErr = storeDown
			return storeDown
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			store := newFakeReplicaStore()
			store.write("a")
			rep := newScriptedReplicator(store, nil)
			w := replicatingWorker(store, rep, nil)
			want := setup(w.Buckets.(*fakeReplSource))
			if err := w.tick(context.Background()); !errors.Is(err, want) {
				t.Errorf("tick = %v, want %v", err, want)
			}
			if len(rep.copied) != 0 {
				t.Errorf("copied %v into a destination with no state", rep.copiedKeys())
			}
		})
	}
}

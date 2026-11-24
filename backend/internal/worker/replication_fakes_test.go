package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The fixture's source and destination.
const (
	passBackend    = "primary"
	passBucket     = "paladin-archive"
	gateDstBackend = "secondary"
	gateDstBucket  = "paladin-archive-dr"
	passCollection = "docs"
)

var (
	errTransientCopy = fmt.Errorf("slow down: %w", ErrCopyTransient)
	errMismatchCopy  = fmt.Errorf("%w: %w: digest differs", ErrCopyTransient, ErrCopyMismatch)
	errPermanentCopy = errors.New("access denied")
	errSourceGone    = fmt.Errorf("no such key: %w", ErrObjectMissing)
)

// fakeBucketID is a stable id per bucket name, for the gate's deferrals.
func fakeBucketID(backend, bucket string) pgtype.UUID {
	id := pgtype.UUID{Valid: true}
	copy(id.Bytes[:], backend+"/"+bucket)
	return id
}

type fakeReplSource struct {
	buckets []admindomain.Bucket
	listErr error
	// objStates answers ObjectReplicationState by object id; an id not in
	// it reads as an unchanged AVAILABLE row at version 0.
	objStates map[uuid.UUID]ReplicationObjectState
	objErr    error
	// gateStates is each bucket's gate state, by "backend/bucket"; a bucket
	// not in it is enabled and ready. unknown lists destinations with no row.
	gateStates map[string]bucketgate.State
	unknown    map[string]bool
	gateErr    error
}

func (f *fakeReplSource) gateState(backend, bucket string) bucketgate.State {
	if st, ok := f.gateStates[backend+"/"+bucket]; ok {
		return st
	}
	return bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}
}

func (f *fakeReplSource) BucketGateState(_ context.Context, backend, bucket string) (BucketGateState, error) {
	if f.gateErr != nil {
		return BucketGateState{}, f.gateErr
	}
	if f.unknown[backend+"/"+bucket] {
		return BucketGateState{}, ErrReplicationDestinationUnknown
	}
	return BucketGateState{ID: fakeBucketID(backend, bucket), State: f.gateState(backend, bucket)}, nil
}

func (f *fakeReplSource) ObjectReplicationState(_ context.Context, _, id uuid.UUID) (ReplicationObjectState, error) {
	if st, ok := f.objStates[id]; ok {
		return st, f.objErr
	}
	return ReplicationObjectState{Found: true, State: string(statemachine.StateAvailable)}, f.objErr
}

func (f *fakeReplSource) ListBucketsWithReplication(_ context.Context) ([]ReplicationBucket, error) {
	out := make([]ReplicationBucket, 0, len(f.buckets))
	for _, b := range f.buckets {
		out = append(out, ReplicationBucket{
			Bucket: b, ID: fakeBucketID(b.BackendID, b.BucketName), State: f.gateState(b.BackendID, b.BucketName),
		})
	}
	return out, f.listErr
}

// fakeReplica is one row of the fake store.
type fakeReplica struct {
	claim   ReplicaClaim
	due     bool
	claimed bool
	// delay and lastError are the last retry's.
	delay     time.Duration
	lastError string
}

// fakeReplicaStore models object_replicas as the SQL does: a write marks a
// location due and bumps its generation, and every give-back of a claim acts
// only at the claim's generation.
type fakeReplicaStore struct {
	mu      sync.Mutex
	tenant  uuid.UUID
	rows    map[string]*fakeReplica
	objects map[uuid.UUID]ReplicaObject
	seeded  map[[16]byte]bool

	seedErr, claimErr, objErr, settleErr error
	claims                               int
	// onReplicate runs inside a copy, to model a write that lands during it.
	onReplicate func()
}

func newFakeReplicaStore() *fakeReplicaStore {
	return &fakeReplicaStore{
		tenant: uuid.NewV7(), rows: map[string]*fakeReplica{},
		objects: map[uuid.UUID]ReplicaObject{}, seeded: map[[16]byte]bool{},
	}
}

var (
	fakeSrcID = fakeBucketID(passBackend, passBucket)
	fakeDstID = fakeBucketID(gateDstBackend, gateDstBucket)
	// fakeWriteClock stamps each fake write a distinct byte write.
	fakeWriteEpoch = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
)

// existing stores an AVAILABLE object in the source bucket without queueing
// it: one written before replication was enabled, which only a seed queues.
func (s *fakeReplicaStore) existing(key string) ReplicaObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj := ReplicaObject{
		Row: LifecycleObjectRow{
			ObjectID: uuid.NewV7(), Key: key, State: string(statemachine.StateAvailable), SizeBytes: int64(len(key)),
		},
		BucketID:  fakeSrcID,
		WrittenAt: fakeWriteEpoch.Add(time.Duration(len(s.objects)) * time.Second),
	}
	s.objects[obj.Row.ObjectID] = obj
	return obj
}

// write stores an AVAILABLE object and queues its location, as the objects
// trigger does on a promote.
func (s *fakeReplicaStore) write(key string) ReplicaObject {
	obj := s.existing(key)
	s.queue(obj)
	return obj
}

// rewrite gives an object a new byte write and queues it.
func (s *fakeReplicaStore) rewrite(id uuid.UUID) {
	s.mu.Lock()
	obj := s.objects[id]
	obj.WrittenAt = obj.WrittenAt.Add(time.Hour)
	s.objects[id] = obj
	s.mu.Unlock()
	s.queue(obj)
}

// touch queues an object without a new byte write: a tag or metadata change.
func (s *fakeReplicaStore) touch(id uuid.UUID) {
	s.mu.Lock()
	obj := s.objects[id]
	s.mu.Unlock()
	s.queue(obj)
}

// setState moves an object to state; leaving AVAILABLE queues nothing.
func (s *fakeReplicaStore) setState(id uuid.UUID, state string) {
	s.mu.Lock()
	obj := s.objects[id]
	obj.Row.State = state
	s.objects[id] = obj
	s.mu.Unlock()
	if state == string(statemachine.StateAvailable) {
		s.queue(obj)
	}
}

// purge deletes an object and marks the rows it owns, as the delete trigger
// does.
func (s *fakeReplicaStore) purge(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, id)
	for _, r := range s.rows {
		if r.claim.ObjectID == id {
			r.claim.Generation++
			r.claim.Attempts = 0
			r.due = true
		}
	}
}

func (s *fakeReplicaStore) queue(obj ReplicaObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueLocked(obj)
}

func (s *fakeReplicaStore) queueLocked(obj ReplicaObject) {
	r, ok := s.rows[obj.Row.Key]
	if !ok {
		r = &fakeReplica{claim: ReplicaClaim{
			SourceBucketID: fakeSrcID, TenantID: s.tenant, Collection: passCollection, Key: obj.Row.Key,
		}}
		s.rows[obj.Row.Key] = r
	}
	r.claim.ObjectID = obj.Row.ObjectID
	r.claim.Generation++
	r.claim.Attempts = 0
	r.due = true
}

func (s *fakeReplicaStore) SeedReplication(_ context.Context, src pgtype.UUID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seedErr != nil {
		return 0, s.seedErr
	}
	if s.seeded[src.Bytes] {
		return 0, nil
	}
	s.seeded[src.Bytes] = true
	var n int64
	for _, obj := range s.objects {
		if obj.BucketID == src && obj.Row.State == string(statemachine.StateAvailable) {
			s.queueLocked(obj)
			n++
		}
	}
	return n, nil
}

func (s *fakeReplicaStore) ClaimReplicas(_ context.Context, src pgtype.UUID, limit int, _ time.Duration) ([]ReplicaClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	keys := make([]string, 0, len(s.rows))
	for k, r := range s.rows {
		if r.due && !r.claimed && r.claim.SourceBucketID == src {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]ReplicaClaim, 0, len(keys))
	for _, k := range keys {
		s.rows[k].claimed = true
		out = append(out, s.rows[k].claim)
	}
	return out, nil
}

func (s *fakeReplicaStore) ReplicaObjects(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]ReplicaObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objErr != nil {
		return nil, s.objErr
	}
	out := map[uuid.UUID]ReplicaObject{}
	for _, id := range ids {
		if obj, ok := s.objects[id]; ok {
			out[id] = obj
		}
	}
	return out, nil
}

// giveBack ends a claim; at the claim's generation it applies settle.
func (s *fakeReplicaStore) giveBack(c ReplicaClaim, settle func(r *fakeReplica)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settleErr != nil {
		return s.settleErr
	}
	r, ok := s.rows[c.Key]
	if !ok {
		return nil
	}
	r.claimed = false
	if r.claim.Generation == c.Generation {
		settle(r)
	}
	return nil
}

func (s *fakeReplicaStore) RecordReplicaCopy(_ context.Context, c ReplicaClaim, copied ReplicaCopy) error {
	s.mu.Lock()
	if r, ok := s.rows[c.Key]; ok && s.settleErr == nil {
		r.claim.Replica = copied
	}
	s.mu.Unlock()
	return s.giveBack(c, func(r *fakeReplica) { r.due, r.claim.Attempts = false, 0 })
}

func (s *fakeReplicaStore) SettleReplica(_ context.Context, c ReplicaClaim) error {
	return s.giveBack(c, func(r *fakeReplica) { r.due, r.claim.Attempts = false, 0 })
}

func (s *fakeReplicaStore) RetryReplica(_ context.Context, c ReplicaClaim, delay time.Duration, reason string) error {
	return s.giveBack(c, func(r *fakeReplica) {
		r.due = false // put off past this test's ticks
		r.claim.Attempts++
		r.delay, r.lastError = delay, reason
	})
}

func (s *fakeReplicaStore) ReleaseReplica(_ context.Context, c ReplicaClaim) error {
	return s.giveBack(c, func(*fakeReplica) {})
}

func (s *fakeReplicaStore) ForgetReplica(_ context.Context, c ReplicaClaim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settleErr != nil {
		return s.settleErr
	}
	r, ok := s.rows[c.Key]
	if !ok {
		return nil
	}
	r.claimed = false
	if r.claim.Generation == c.Generation {
		delete(s.rows, c.Key)
	}
	return nil
}

// row is the fake's row at key, or nil.
func (s *fakeReplicaStore) row(key string) *fakeReplica {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rows[key]; ok {
		cp := *r
		return &cp
	}
	return nil
}

// dueKeys lists the keys still due.
func (s *fakeReplicaStore) dueKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k, r := range s.rows {
		if r.due {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// retryAll makes every put-off row due again, as time passing would.
func (s *fakeReplicaStore) retryAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.lastError != "" {
			r.due = true
		}
	}
}

// scriptedReplicator answers each copy by the object's key: the errors listed
// for a key are returned one per attempt, then the copy succeeds.
type scriptedReplicator struct {
	mu       sync.Mutex
	store    *fakeReplicaStore
	script   map[string][]error
	attempts map[string]int
	copied   []replicatedCall
	// missing and missingErr answer SourceMissing.
	missing    bool
	missingErr error
}

type replicatedCall struct {
	src, dst ReplicationTarget
	size     int64
}

func newScriptedReplicator(store *fakeReplicaStore, script map[string][]error) *scriptedReplicator {
	return &scriptedReplicator{store: store, script: script, attempts: map[string]int{}}
}

func (s *scriptedReplicator) SourceMissing(context.Context, ReplicationTarget) (bool, error) {
	return s.missing, s.missingErr
}

func (s *scriptedReplicator) Replicate(_ context.Context, src, dst ReplicationTarget, size int64) error {
	if s.store != nil && s.store.onReplicate != nil {
		s.store.onReplicate()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.attempts[src.Key]
	s.attempts[src.Key] = n + 1
	if errs := s.script[src.Key]; n < len(errs) && errs[n] != nil {
		return errs[n]
	}
	s.copied = append(s.copied, replicatedCall{src: src, dst: dst, size: size})
	return nil
}

func (s *scriptedReplicator) copiedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.copied))
	for _, c := range s.copied {
		out = append(out, c.src.Key)
	}
	return out
}

// repeat is n copies of err, one per attempt.
func repeat(err error, n int) []error {
	out := make([]error, n)
	for i := range out {
		out[i] = err
	}
	return out
}

// replicatingWorker is a worker over one replicating bucket, its store and
// rep. rep may be nil for a dry run.
func replicatingWorker(store *fakeReplicaStore, rep StorageReplicator, logger *zap.Logger) *ReplicationWorker {
	src := &fakeReplSource{buckets: []admindomain.Bucket{{
		BackendID: passBackend, BucketName: passBucket,
		Replication: admindomain.BucketReplication{
			Enabled:           true,
			DestinationBucket: "storageBackends/" + gateDstBackend + "/buckets/" + gateDstBucket,
		},
	}}}
	w := &ReplicationWorker{Buckets: src, Replicas: store, Logger: logger}
	if rep != nil {
		w.Replicator = rep
	}
	return w
}

// Replication worker — copies a replicated bucket's objects to its
// `replication.destination_bucket`, on the same backend or another, and keeps
// state per object (ADR-0050).
//
// The state is a queue in the database, one row per replica location (the
// source bucket and the tenant, collection and key). A write that the replica
// may not reflect marks its row due in the write's own transaction: a promote,
// a change to a field the bucket's filter reads, a restore from the trash, the
// object's purge. No timestamp window decides what the worker sees, so a
// transaction of any length is never passed over, and a pass reads only the
// rows that are due.
//
// Each tick, for every bucket with replication enabled:
//
//  0. Queue the bucket's existing objects the first time the worker meets it,
//     and again after its replication settings change.
//  1. Ask the bucket gate (ADR-0015): Read on the source, BytesWrite on the
//     destination, whose state is read by backend and bucket name. A refused
//     bucket's rows wait, untouched, and the refusal is logged once per tick.
//  2. Claim the bucket's due rows in batches. Each row names the object last
//     written at its location; the object is read as it is now.
//  3. Decide each row. An object purged from the source has its replica
//     deleted from the destination (a byte write there, which the gate
//     admitted as BytesWrite) and its row dropped; one that left the bucket
//     drops its row; one in the trash keeps its replica; one the bucket's filter (CEL,
//     over the same fields a ListObjects filter sees) does not match is
//     settled; one whose replica already holds its current byte write is
//     settled. Anything else is copied through StorageReplicator to the same
//     collection and key in the destination bucket, and the byte write it
//     copied is recorded.
//  4. A row settles only at the generation it was claimed at: a write during
//     the copy leaves it due for a later pass. A failure puts the row off by
//     its own backoff and leaves every other row alone; one still failing
//     transiently ends the bucket's tick, since its backend is likely down
//     for every object after it.
package worker

import (
	"context"
	"errors"
	"time"
	"uuid"

	celpkg "cel.dev/cel-go/cel"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// Batching, claims and retry of the replication worker.
const (
	// replicationClaimBatch is how many rows one claim takes: small, so a
	// pass that stops early hands few back.
	replicationClaimBatch = 32
	// replicationClaimLease is how long a claim keeps other workers off a
	// row: past it, a worker that died mid-batch loses the rows. Longer than
	// one batch of large copies takes.
	replicationClaimLease = time.Hour
	// replicationRetryBase is how long a row waits after its first failed
	// attempt; each later failure doubles it, up to replicationRetryMax.
	replicationRetryBase = time.Minute
	replicationRetryMax  = 6 * time.Hour
	// replicationDefaultInterval stands in for an unset Interval.
	replicationDefaultInterval = 5 * time.Minute
)

// StorageReplicator copies one object from source → destination bucket.
// Implementations live in internal/storage; the worker only sees this seam.
//
// sizeBytes is the object's size as its row records it, which lets the
// implementation pick a server-side copy or a stream. A failure a later
// attempt may cure wraps ErrCopyTransient; a source the backend does not hold
// wraps ErrObjectMissing.
//
// SourceMissing answers whether the source's key is absent from a bucket
// confirmed reachable — true only then; an unreachable bucket is an error.
//
// DeleteReplica removes the replica at dst; a key already absent is no
// failure. A failure a later attempt may cure wraps ErrCopyTransient.
type StorageReplicator interface {
	Replicate(ctx context.Context, src, dst ReplicationTarget, sizeBytes int64) error
	SourceMissing(ctx context.Context, src ReplicationTarget) (bool, error)
	DeleteReplica(ctx context.Context, dst ReplicationTarget) error
}

// ErrCopyTransient marks a copy failure worth retrying: throttling, a server
// error, a dropped connection.
var ErrCopyTransient = errors.New("transient copy failure")

// ErrCopyMismatch marks a replica that did not match its source, which the
// implementation removed. The source may have changed under the copy, so the
// row is retried; the mismatch is the object's own problem, not its
// backend's, so it does not end the bucket's tick.
var ErrCopyMismatch = errors.New("replica does not match its source")

// ReplicationTarget identifies one side of a copy. Collection + Key are the
// logical pair; BackendID + BucketName are the physical routing.
type ReplicationTarget struct {
	BackendID  string
	BucketName string
	TenantID   uuid.UUID
	Collection string
	Key        string
}

// ReplicationSource lists buckets with replication enabled, and re-reads one
// object's row.
//
// BucketGateState reads the destination's gate state by backend and bucket
// name, ErrReplicationDestinationUnknown when no such bucket is registered.
type ReplicationSource interface {
	ListBucketsWithReplication(ctx context.Context) ([]ReplicationBucket, error)
	ObjectReplicationState(ctx context.Context, tenantID, objectID uuid.UUID) (ReplicationObjectState, error)
	BucketGateState(ctx context.Context, backendID, bucketName string) (BucketGateState, error)
}

// ReplicationBucket is a bucket with replication enabled, with its id and the
// state the bucket gate decides its read on.
type ReplicationBucket struct {
	admindomain.Bucket
	ID    pgtype.UUID
	State bucketgate.State
}

// BucketGateState is one bucket's id and gate state.
type BucketGateState struct {
	ID    pgtype.UUID
	State bucketgate.State
}

// ErrReplicationDestinationUnknown is a replication destination that names no
// registered bucket: there is no state for the gate to admit a write on.
var ErrReplicationDestinationUnknown = errors.New("the replication destination is not a registered bucket")

// ReplicationObjectState is an object's row as re-read before the worker
// passes over it. Found is false when the row is gone.
type ReplicationObjectState struct {
	Found           bool
	State           string
	ResourceVersion int64
}

// ReplicaStore is the per-object replication state: a queue of replica
// locations a write marked due, and what each destination holds.
//
// SeedReplication queues every AVAILABLE object of a bucket the first time
// it is called for it, and again after the bucket's replication settings
// change; otherwise it queues nothing. It returns how many it queued.
//
// ClaimReplicas takes up to limit due rows of a bucket for this worker for
// lease. Every claimed row must be given back: recorded, settled, retried,
// released or forgotten — each at the claim's generation, so a write since
// the claim leaves the row due.
type ReplicaStore interface {
	SeedReplication(ctx context.Context, sourceBucketID pgtype.UUID) (int64, error)
	ClaimReplicas(ctx context.Context, sourceBucketID pgtype.UUID, limit int, lease time.Duration) ([]ReplicaClaim, error)
	ReplicaObjects(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]ReplicaObject, error)
	RecordReplicaCopy(ctx context.Context, c ReplicaClaim, copied ReplicaCopy) error
	SettleReplica(ctx context.Context, c ReplicaClaim) error
	RetryReplica(ctx context.Context, c ReplicaClaim, delay time.Duration, reason string) error
	ReleaseReplica(ctx context.Context, c ReplicaClaim) error
	ForgetReplica(ctx context.Context, c ReplicaClaim) error
}

// ReplicaClaim is one claimed replica location: the object last written
// there, the generation the claim read, and what the destination holds.
type ReplicaClaim struct {
	SourceBucketID pgtype.UUID
	TenantID       uuid.UUID
	Collection     string
	Key            string
	ObjectID       uuid.UUID
	Generation     int64
	// Attempts is how many attempts failed since the last write or success.
	Attempts int
	// Replica is what replication last wrote at this location; its
	// BucketID is invalid when it wrote nothing.
	Replica ReplicaCopy
}

// ReplicaCopy is a replica as written: the destination bucket, the object
// copied, the byte write it copied and its size.
type ReplicaCopy struct {
	BucketID  pgtype.UUID
	ObjectID  uuid.UUID
	WrittenAt time.Time
	SizeBytes int64
}

// holds reports whether the replica in dst is obj's current byte write.
func (c ReplicaCopy) holds(dst pgtype.UUID, obj ReplicaObject) bool {
	return c.BucketID.Valid && c.BucketID == dst && c.ObjectID == obj.Row.ObjectID && c.WrittenAt.Equal(obj.WrittenAt)
}

// ReplicaObject is a claimed row's object as it is now: the fields the filter
// reads, the bucket its collection is bound to, and its current byte write.
type ReplicaObject struct {
	Row       LifecycleObjectRow
	BucketID  pgtype.UUID
	WrittenAt time.Time
}

// ReplicationWorker copies each replicated bucket's due objects. A nil
// StorageReplicator yields a worker that claims and logs intent without
// copying, and gives every row back as it found it.
type ReplicationWorker struct {
	Buckets    ReplicationSource
	Replicas   ReplicaStore
	Replicator StorageReplicator
	Interval   time.Duration
	Logger     *zap.Logger
	// CELEvaluator compiles each bucket's replication.filter, once per bucket
	// per tick through its cache. A bucket with a filter and no evaluator is
	// not replicated: copying everything would ignore the filter.
	CELEvaluator *cel.Evaluator
}

func (r *ReplicationWorker) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = replicationDefaultInterval
	}
	r.log().Info("replication worker starting",
		zap.Duration("interval", r.Interval),
		zap.Bool("dry_run", r.Replicator == nil))
	return RunTicker(ctx, "replication", r.Interval, r.tick)
}

// tick replicates every enabled bucket, carrying on past one that fails, and
// returns the first failure.
func (r *ReplicationWorker) tick(ctx context.Context) error {
	buckets, err := r.Buckets.ListBucketsWithReplication(ctx)
	if err != nil {
		r.log().Warn("failed to list buckets", zap.Error(err))
		return err
	}
	gates := replicationGates{
		src: newBucketDeferrals(r.log(), bucketgate.Read),
		dst: newBucketDeferrals(r.log(), bucketgate.BytesWrite),
	}
	var first error
	for _, b := range buckets {
		if ctx.Err() != nil {
			return first
		}
		keepFirst(&first, r.processBucket(ctx, b, gates))
	}
	return first
}

// replicationGates are one tick's gate answers: Read on each source bucket,
// BytesWrite on each destination. Each refused bucket is logged once a tick.
type replicationGates struct {
	src, dst *bucketDeferrals
}

// bucketPass is what one tick's pass over a bucket did.
type bucketPass struct {
	copied, current, filtered, missing, deleted, forgotten, retried int
	// failed is the first failure, which the tick reports.
	failed error
	// stopped is set once a failure ended the pass early.
	stopped bool
}

// replicaRoute is where one bucket's objects replicate from and to, and the
// bucket's compiled filter (nil: every object).
type replicaRoute struct {
	bucket                admindomain.Bucket
	srcID, dstID          pgtype.UUID
	dstBackend, dstBucket string
	filter                celpkg.Program
}

// processBucket replicates one bucket's due rows and returns the first
// failure it logged. A row that fails does not stop the others; one still
// failing transiently stops the bucket for the tick. A dry run is not a
// failure.
func (r *ReplicationWorker) processBucket(ctx context.Context, rb ReplicationBucket, gates replicationGates) error {
	b := rb.Bucket
	if !b.Replication.Enabled || b.Replication.DestinationBucket == "" {
		return nil
	}
	bucketLog := r.log().With(zap.String(logKeyBackend, b.BackendID), zap.String(logKeyBucket, b.BucketName))
	dstBackend, dstBucket, err := splitBucketName(b.Replication.DestinationBucket)
	if err != nil {
		bucketLog.Warn("invalid destination", zap.String("dst", b.Replication.DestinationBucket), zap.Error(err))
		return err
	}
	bucketLog = bucketLog.With(zap.String(logKeyDstBackend, dstBackend), zap.String(logKeyDstBucket, dstBucket))
	// Before the gate: what is queued waits for the gate, and nothing written
	// meanwhile is missed, since the triggers queue it.
	queued, err := r.Replicas.SeedReplication(ctx, rb.ID)
	if err != nil {
		bucketLog.Warn("failed to queue the bucket's objects for replication; deferring the bucket to the next tick", zap.Error(err))
		return err
	}
	if queued > 0 {
		bucketLog.Info("queued the bucket's objects for replication", zap.Int64("objects", queued))
	}
	dst, admitted, err := r.admitBoth(ctx, bucketLog, rb, dstBackend, dstBucket, gates)
	if !admitted {
		return err
	}
	filter, err := r.compileFilter(b.Replication.Filter)
	if err != nil {
		bucketLog.Error("replication filter does not compile; the bucket is not replicated until it is fixed",
			zap.String("filter", b.Replication.Filter), zap.Error(err))
		return err
	}
	route := replicaRoute{
		bucket: b, srcID: rb.ID, dstID: dst.ID, dstBackend: dstBackend, dstBucket: dstBucket, filter: filter,
	}
	var pass bucketPass
	for !pass.stopped && ctx.Err() == nil {
		if !r.replicateBatch(ctx, bucketLog, &pass, route) {
			break
		}
	}
	summary := []zap.Field{
		zap.Int("copied", pass.copied), zap.Int("current", pass.current), zap.Int("filtered", pass.filtered),
		zap.Int("source_missing", pass.missing), zap.Int("deleted", pass.deleted), zap.Int("forgotten", pass.forgotten), zap.Int("retried", pass.retried),
		zap.Bool("dry_run", r.Replicator == nil),
	}
	switch {
	case pass.failed != nil || pass.stopped:
		bucketLog.Info("replication pass ended with failures", summary...)
	case pass == bucketPass{}:
		bucketLog.Debug("replication pass found nothing due")
	default:
		bucketLog.Info("replication pass complete", summary...)
	}
	return pass.failed
}

// replicateBatch claims one batch of due rows and decides each. It reports
// whether another batch may follow: false once nothing was due, a claim
// failed, or the worker is dry-running — a dry run hands its rows back due,
// so a second claim would take them again.
func (r *ReplicationWorker) replicateBatch(ctx context.Context, log *zap.Logger, pass *bucketPass, route replicaRoute) bool {
	claims, err := r.Replicas.ClaimReplicas(ctx, route.srcID, replicationClaimBatch, replicationClaimLease)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("failed to claim due replicas", zap.Error(err))
			keepFirst(&pass.failed, err)
		}
		return false
	}
	if len(claims) == 0 {
		return false
	}
	ids := make([]uuid.UUID, 0, len(claims))
	for _, c := range claims {
		ids = append(ids, c.ObjectID)
	}
	objects, err := r.Replicas.ReplicaObjects(ctx, ids)
	if err != nil {
		log.Warn("failed to read the claimed objects; handing them back", zap.Error(err))
		keepFirst(&pass.failed, err)
		r.release(ctx, log, pass, claims)
		return false
	}
	for i, c := range claims {
		if ctx.Err() != nil || pass.stopped {
			r.release(ctx, log, pass, claims[i:])
			return false
		}
		obj, found := objects[c.ObjectID]
		r.replicateClaim(ctx, log, pass, c, obj, found, route)
	}
	return r.Replicator != nil
}

// release hands claims back due, untouched. A cancelled context cannot write
// them back; their lease runs out instead.
func (r *ReplicationWorker) release(ctx context.Context, log *zap.Logger, pass *bucketPass, claims []ReplicaClaim) {
	if ctx.Err() != nil {
		return
	}
	for _, c := range claims {
		if err := r.Replicas.ReleaseReplica(ctx, c); err != nil {
			log.Warn("failed to hand a claimed replica back; it waits out its lease",
				zap.String(logKeyObjectID, c.ObjectID.String()), zap.Error(err))
			keepFirst(&pass.failed, err)
		}
	}
}

// replicateClaim decides one claimed row: forget it, settle it, or copy its
// object.
func (r *ReplicationWorker) replicateClaim(
	ctx context.Context, bucketLog *zap.Logger, pass *bucketPass, c ReplicaClaim, obj ReplicaObject, found bool, route replicaRoute,
) {
	// The success lines name the object by id alone; a failure line adds its
	// key, which an operator needs to find the object.
	okLog := bucketLog.With(
		zap.String(logKeyTenant, c.TenantID.String()),
		zap.String(logKeyCollection, c.Collection),
		zap.String(logKeyObjectID, c.ObjectID.String()),
	)
	log := okLog.With(zap.String(logKeyKey, c.Key))
	switch {
	case !found:
		r.deletePurged(ctx, okLog, log, pass, c, route)
	case obj.BucketID != route.srcID:
		okLog.Info("object's collection is no longer bound to the replicated bucket; forgetting its replica")
		r.forget(ctx, log, pass, c)
	case obj.Row.State != string(statemachine.StateAvailable):
		// In the trash the source still holds the bytes, and the replica
		// stays; a restore marks the row due again.
		okLog.Debug("object is not available; leaving its replica as it is", zap.String("state", obj.Row.State))
		r.settle(ctx, log, pass, c)
	default:
		r.copyObject(ctx, okLog, log, pass, c, obj, route)
	}
}

// copyObject copies an AVAILABLE object unless the filter passes it over or
// its replica is current, and records the outcome on the row.
func (r *ReplicationWorker) copyObject(
	ctx context.Context, okLog, log *zap.Logger, pass *bucketPass, c ReplicaClaim, obj ReplicaObject, route replicaRoute,
) {
	if route.filter != nil {
		match, err := cel.Match(route.filter, lifecycleRowToCELVars(obj.Row))
		if err != nil {
			// Neither copied nor known to be outside the filter: retried
			// until the filter or the object changes.
			log.Warn("failed to evaluate the replication filter", zap.Error(err))
			r.retry(ctx, log, pass, c, err)
			return
		}
		if !match {
			pass.filtered++
			okLog.Debug("object is outside the replication filter")
			r.settle(ctx, log, pass, c)
			return
		}
	}
	if c.Replica.holds(route.dstID, obj) {
		pass.current++
		okLog.Debug("replica already holds the object's current bytes")
		r.settle(ctx, log, pass, c)
		return
	}
	if r.Replicator == nil {
		okLog.Info("dry-run match")
		r.release(ctx, log, pass, []ReplicaClaim{c})
		return
	}
	src := ReplicationTarget{
		BackendID: route.bucket.BackendID, BucketName: route.bucket.BucketName,
		TenantID: c.TenantID, Collection: c.Collection, Key: c.Key,
	}
	dst := ReplicationTarget{
		BackendID: route.dstBackend, BucketName: route.dstBucket,
		TenantID: c.TenantID, Collection: c.Collection, Key: c.Key,
	}
	err := r.Replicator.Replicate(ctx, src, dst, obj.Row.SizeBytes)
	switch {
	case err == nil:
		pass.copied++
		okLog.Debug("replicated object", zap.Int64("size_bytes", obj.Row.SizeBytes))
		if err := r.Replicas.RecordReplicaCopy(ctx, c, ReplicaCopy{
			BucketID: route.dstID, ObjectID: obj.Row.ObjectID, WrittenAt: obj.WrittenAt, SizeBytes: obj.Row.SizeBytes,
		}); err != nil {
			log.Warn("failed to record a replica; the object is copied again", zap.Error(err))
			keepFirst(&pass.failed, err)
		}
	case ctx.Err() != nil:
		// Stopping: the claim's lease hands the row back.
	case errors.Is(err, ErrObjectMissing):
		gone, why := r.confirmSourceGone(ctx, src, obj.Row)
		if !gone {
			log.Warn("source object reported missing but not confirmed; retrying it later",
				zap.String("reason", why), zap.Error(err))
			r.retry(ctx, log, pass, c, err)
			return
		}
		// The row is unchanged and still AVAILABLE, and the source's bucket
		// holds no such key: nothing to copy until the object is written
		// again, which marks the row due.
		pass.missing++
		log.Warn("source object missing; passing over it", zap.Error(err))
		r.settle(ctx, log, pass, c)
	case errors.Is(err, ErrCopyMismatch):
		log.Warn("replica did not match its source; retrying it later", zap.Error(err))
		r.retry(ctx, log, pass, c, err)
	case errors.Is(err, ErrCopyTransient):
		log.Warn("failed to copy object; deferring the bucket to the next tick", zap.Error(err))
		r.retry(ctx, log, pass, c, err)
		pass.stopped = true
	default:
		log.Warn("failed to copy object; retrying it later", zap.Error(err))
		r.retry(ctx, log, pass, c, err)
	}
}

// deletePurged deletes the replica of an object purged from the source — its
// row gone, by a permanent delete, the trash's expiry or its tenant's purge —
// and drops the row. Only a replica this worker wrote into the current
// destination is deleted; a location it never copied, or copied into a
// destination the bucket no longer names, is only forgotten.
func (r *ReplicationWorker) deletePurged(
	ctx context.Context, okLog, log *zap.Logger, pass *bucketPass, c ReplicaClaim, route replicaRoute,
) {
	if !c.Replica.BucketID.Valid || c.Replica.BucketID != route.dstID {
		okLog.Debug("purged object has no replica in the destination; forgetting it")
		r.forget(ctx, log, pass, c)
		return
	}
	if r.Replicator == nil {
		okLog.Info("dry-run delete")
		r.release(ctx, log, pass, []ReplicaClaim{c})
		return
	}
	err := r.Replicator.DeleteReplica(ctx, ReplicationTarget{
		BackendID: route.dstBackend, BucketName: route.dstBucket,
		TenantID: c.TenantID, Collection: c.Collection, Key: c.Key,
	})
	switch {
	case err == nil:
		pass.deleted++
		okLog.Debug("deleted the replica of a purged object")
		r.forget(ctx, log, pass, c)
	case ctx.Err() != nil:
		// Stopping: the claim's lease hands the row back.
	case errors.Is(err, ErrCopyTransient):
		log.Warn("failed to delete a purged object's replica; deferring the bucket to the next tick", zap.Error(err))
		r.retry(ctx, log, pass, c, err)
		pass.stopped = true
	default:
		log.Warn("failed to delete a purged object's replica; retrying it later", zap.Error(err))
		r.retry(ctx, log, pass, c, err)
	}
}

// settle records a row as decided at its claim's generation.
func (r *ReplicationWorker) settle(ctx context.Context, log *zap.Logger, pass *bucketPass, c ReplicaClaim) {
	if err := r.Replicas.SettleReplica(ctx, c); err != nil {
		log.Warn("failed to settle a replica; it is decided again", zap.Error(err))
		keepFirst(&pass.failed, err)
	}
}

// forget drops the row of an object that is gone or left the bucket.
func (r *ReplicationWorker) forget(ctx context.Context, log *zap.Logger, pass *bucketPass, c ReplicaClaim) {
	if err := r.Replicas.ForgetReplica(ctx, c); err != nil {
		log.Warn("failed to forget a replica; it is decided again", zap.Error(err))
		keepFirst(&pass.failed, err)
		return
	}
	pass.forgotten++
}

// retry puts a failed row off by its backoff, and reports the failure for the
// tick.
func (r *ReplicationWorker) retry(ctx context.Context, log *zap.Logger, pass *bucketPass, c ReplicaClaim, cause error) {
	pass.retried++
	keepFirst(&pass.failed, cause)
	if ctx.Err() != nil {
		return
	}
	delay := replicationRetryDelay(c.Attempts + 1)
	if err := r.Replicas.RetryReplica(ctx, c, delay, cause.Error()); err != nil {
		log.Warn("failed to put a replica off; it waits out its lease", zap.Error(err))
		return
	}
	log.Debug("replica retry scheduled", zap.Int("attempt", c.Attempts+1), zap.Duration("backoff", delay))
}

// admitBoth asks the gate about both sides of a bucket's replication: Read on
// the source, BytesWrite on the destination. A refusal is a deferral, not a
// failure — false with no error, logged once a tick by the gate's deferrals —
// while a destination whose state cannot be read is a failure. It returns
// the destination's id and state.
func (r *ReplicationWorker) admitBoth(
	ctx context.Context, log *zap.Logger, rb ReplicationBucket, dstBackend, dstBucket string, gates replicationGates,
) (BucketGateState, bool, error) {
	if !gates.src.admit(bucketGateRow{
		ID: rb.ID, BackendName: rb.BackendID, BucketName: rb.BucketName,
		Enabled: rb.State.Enabled, ReadOnly: rb.State.ReadOnly, ProvisionState: rb.State.ProvisionState,
	}) {
		return BucketGateState{}, false, nil
	}
	dst, err := r.Buckets.BucketGateState(ctx, dstBackend, dstBucket)
	if err != nil {
		log.Warn("failed to read the replication destination's state; deferring the bucket to the next tick", zap.Error(err))
		return BucketGateState{}, false, err
	}
	return dst, gates.dst.admit(bucketGateRow{
		ID: dst.ID, BackendName: dstBackend, BucketName: dstBucket,
		Enabled: dst.State.Enabled, ReadOnly: dst.State.ReadOnly, ProvisionState: dst.State.ProvisionState,
	}), nil
}

// compileFilter compiles a bucket's replication filter; an empty one is nil,
// which selects every object.
func (r *ReplicationWorker) compileFilter(expr string) (celpkg.Program, error) {
	if expr == "" {
		return nil, nil //nolint:nilnil // no filter: every object is selected
	}
	if r.CELEvaluator == nil {
		return nil, errNoEvaluator
	}
	return r.CELEvaluator.Compile(cel.ObjectSchema, expr)
}

// replicationRetryDelay is the wait after the given failed attempt
// (1-based): replicationRetryBase doubled per earlier attempt, capped at
// replicationRetryMax.
func replicationRetryDelay(attempt int) time.Duration {
	d := replicationRetryBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= replicationRetryMax {
			return replicationRetryMax
		}
	}
	return d
}

// confirmSourceGone checks a copy's not-found before the object is passed
// over: the row must be unchanged — still AVAILABLE, at the version the claim
// read — and a fresh HEAD must find the key absent from a reachable bucket.
// Anything short of that answers false with the reason.
func (r *ReplicationWorker) confirmSourceGone(ctx context.Context, src ReplicationTarget, row LifecycleObjectRow) (bool, string) {
	st, err := r.Buckets.ObjectReplicationState(ctx, src.TenantID, row.ObjectID)
	switch {
	case err != nil:
		return false, "re-reading the row failed: " + err.Error()
	case !st.Found || st.State != string(statemachine.StateAvailable):
		return false, "the row is no longer available"
	case st.ResourceVersion != row.ResourceVersion:
		return false, "the row changed since it was read"
	}
	missing, err := r.Replicator.SourceMissing(ctx, src)
	switch {
	case err != nil:
		return false, "the source HEAD failed: " + err.Error()
	case !missing:
		return false, "the source HEAD finds the object"
	}
	return true, ""
}

func (r *ReplicationWorker) log() *zap.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return zap.NewNop()
}

// splitBucketName parses "storageBackends/{backend}/buckets/{name}".
func splitBucketName(name string) (backend, bucket string, err error) {
	const sep = "/"
	parts := splitN(name, sep, -1)
	if len(parts) == 4 && parts[0] == "storageBackends" && parts[2] == "buckets" {
		return parts[1], parts[3], nil
	}
	return "", "", &replicationParseError{name: name}
}

type replicationParseError struct{ name string }

func (e *replicationParseError) Error() string {
	return "replication: invalid destination_bucket " + e.name
}

// splitN is a tiny re-impl of strings.Split to avoid pulling strings here
// (keeps the worker package's import surface narrow).
func splitN(s, sep string, n int) []string {
	out := []string{}
	last := 0
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			out = append(out, s[last:i])
			last = i + len(sep)
			i += len(sep) - 1
		}
	}
	out = append(out, s[last:])
	if n > 0 && len(out) > n {
		return out[:n]
	}
	return out
}

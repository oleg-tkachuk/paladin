// Replication worker — copies a replicated bucket's newly promoted objects to
// its `replication.destination_bucket`, on the same backend or another one.
//
// Each tick, for every bucket with replication enabled:
//
//  0. Ask the bucket gate (ADR-0015): Read on the source, BytesWrite on the
//     destination, whose state is read by backend and bucket name. A refused
//     bucket's copies are deferred — nothing copied, the watermark left where
//     it is — and the refusal is logged once per tick; the first tick the gate
//     admits both sides takes them.
//  1. Read the bucket's watermark: every object committed at or before it has
//     been replicated. A bucket whose watermark cannot be read is deferred for
//     the tick, since guessing a window could pass objects never copied.
//  2. Walk the objects of every collection bound to the bucket and select the
//     AVAILABLE ones committed after the watermark and before the horizon —
//     the database clock at the scan's start, less SettleDelay. committed_at
//     is stamped by the database when the promote's transaction starts, and
//     the row is visible only once it commits; the delay leaves a promote that
//     takes less than SettleDelay to commit for a later pass, never behind it.
//     An object the bucket's `replication.filter` (CEL, over the same fields a
//     ListObjects filter sees) does not match is passed over; one the filter
//     fails to evaluate on holds the watermark, as a failed copy does.
//  3. Copy each through StorageReplicator to the same collection and key in
//     the destination bucket. A transient failure is retried with exponential
//     backoff; one that outlasts the retries stops the bucket for the tick,
//     since its backend is likely down for every object after it.
//  4. Advance the watermark to the latest committed_at the pass decided —
//     copied, filtered out, or confirmed gone from the source — and only when
//     the whole walk completed with every selected object decided. Never to
//     the horizon itself: a row with an older stamp than the horizon that
//     becomes visible after the pass is still after the watermark. Any other
//     failure holds the watermark where it is, so the next tick selects the
//     failed object again — and with it the objects after it, which copy
//     again: a replica is overwritten with the same bytes.
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

// Retry and timing of the replication worker.
const (
	// replicationCopyAttempts is how many times one object's copy is tried
	// while it fails transiently, the first attempt included.
	replicationCopyAttempts = 4
	// replicationBackoffBase is the wait before the first retry; each later
	// one doubles it, up to replicationBackoffMax.
	replicationBackoffBase = time.Second
	replicationBackoffMax  = 30 * time.Second
	// replicationSettleDelay is how far behind the scan's start the horizon
	// sits: an object's committed_at is stamped when its transaction starts,
	// and the row is visible only once it commits.
	replicationSettleDelay = time.Minute
	// replicationDefaultInterval and replicationDefaultLookback stand in for
	// an unset Interval and LookbackWindow.
	replicationDefaultInterval = 5 * time.Minute
	replicationDefaultLookback = time.Hour
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
type StorageReplicator interface {
	Replicate(ctx context.Context, src, dst ReplicationTarget, sizeBytes int64) error
	SourceMissing(ctx context.Context, src ReplicationTarget) (bool, error)
}

// ErrCopyTransient marks a copy failure worth retrying: throttling, a server
// error, a dropped connection.
var ErrCopyTransient = errors.New("transient copy failure")

// ErrCopyMismatch marks a replica that did not match its source, which the
// implementation removed. It is retried as a transient failure, since the
// source may have changed under the copy; one that outlasts the retries is
// the object's own problem, not its backend's, so it holds the watermark
// without stopping the bucket.
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
	ListCollectionBindings(ctx context.Context, backendID, bucketName string) ([]CollectionBinding, error)
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

// ReplicationClock is the database's clock, the one that stamps
// objects.committed_at.
type ReplicationClock interface {
	Now(ctx context.Context) (time.Time, error)
}

// WatermarkStore persists the per-bucket replication high-water mark
// across restarts. Implementations live in postgres adapters; the worker
// only sees Get/Advance and degrades gracefully when the store is unset
// (in-memory mode, suitable for dev / tests).
type WatermarkStore interface {
	Get(ctx context.Context, backendID, bucketName string) (time.Time, error)
	Advance(ctx context.Context, backendID, bucketName string, t time.Time) error
}

// ReplicationWorker fans out cross-backend copies. A nil StorageReplicator
// yields a worker that walks objects and logs intent without copying, and
// never advances a watermark.
//
// WatermarkStore is optional. When set, the worker reads the per-bucket
// high-water mark from the store on each tick and writes back after a
// completed pass; an in-memory cache fronts both calls so the hot path
// stays one DB roundtrip per bucket per tick. When unset, watermarks
// live only in process memory (lost on restart — fine for dev).
type ReplicationWorker struct {
	Buckets    ReplicationSource
	Objects    LifecycleObjectIter
	Replicator StorageReplicator
	Watermarks WatermarkStore
	// Clock measures the horizon; nil measures it on Now, which is skewed
	// from the database's by however far the pod's clock is.
	Clock    ReplicationClock
	Interval time.Duration
	Logger   *zap.Logger
	Now      func() time.Time

	// LookbackWindow caps how far back the worker scans on first run when
	// no watermark is found in the store.
	LookbackWindow time.Duration
	// CELEvaluator compiles each bucket's replication.filter, once per bucket
	// per tick through its cache. A bucket with a filter and no evaluator is
	// not replicated: copying everything would ignore the filter.
	CELEvaluator *cel.Evaluator
	// SettleDelay holds the horizon this far behind the scan's start; zero
	// takes replicationSettleDelay in Run.
	SettleDelay time.Duration

	// sleep waits between retries; nil waits on a timer. Tests replace it.
	sleep func(ctx context.Context, d time.Duration) error

	watermarks map[string]time.Time // in-memory cache of last-seen committed_at
}

func (r *ReplicationWorker) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = replicationDefaultInterval
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.LookbackWindow <= 0 {
		r.LookbackWindow = replicationDefaultLookback
	}
	if r.SettleDelay <= 0 {
		r.SettleDelay = replicationSettleDelay
	}
	if r.watermarks == nil {
		r.watermarks = map[string]time.Time{}
	}
	r.log().Info("replication worker starting",
		zap.Duration("interval", r.Interval),
		zap.Duration("lookback_window", r.LookbackWindow),
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

// bucketPass is what one tick's walk of a bucket did.
type bucketPass struct {
	copied, filtered, missing int
	// covered is the latest committed_at among the objects the pass decided.
	covered time.Time
	// held is the first failure that keeps the watermark where it is.
	held error
	// stopped is set once a failure ended the walk early.
	stopped bool
}

// decided records an object the pass is done with.
func (p *bucketPass) decided(committed time.Time) {
	if committed.After(p.covered) {
		p.covered = committed
	}
}

// errPassStopped ends an object walk early; the failure that caused it is the
// pass's held error.
var errPassStopped = errors.New("replication pass stopped")

// processBucket copies one bucket's new objects and returns the first failure
// it logged. An object that fails to copy for good does not stop the others;
// one still failing transiently after its retries stops the bucket for the
// tick. A dry run is not a failure.
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
	now, err := r.clockNow(ctx)
	if err != nil {
		bucketLog.Warn("failed to read the database clock; deferring the bucket to the next tick", zap.Error(err))
		return err
	}
	// Before the gate: a bucket seen for the first time has its window's
	// start written down now, so deferring it does not slide the window.
	cutoff, err := r.cutoff(ctx, b.BackendID, b.BucketName, now)
	if err != nil {
		return err
	}
	if admitted, err := r.admitBoth(ctx, bucketLog, rb, dstBackend, dstBucket, gates); !admitted {
		return err
	}
	filter, err := r.compileFilter(b.Replication.Filter)
	if err != nil {
		bucketLog.Error("replication filter does not compile; the bucket is not replicated until it is fixed",
			zap.String("filter", b.Replication.Filter), zap.Error(err))
		return err
	}
	horizon := now.Add(-r.SettleDelay)
	bindings, err := r.Buckets.ListCollectionBindings(ctx, b.BackendID, b.BucketName)
	if err != nil {
		bucketLog.Warn("failed to list collection bindings", zap.Error(err))
		return err
	}
	var pass bucketPass
	for _, bind := range bindings {
		if ctx.Err() != nil || pass.stopped {
			break
		}
		log := bucketLog.With(
			zap.String(logKeyTenant, bind.TenantID.String()),
			zap.String(logKeyCollection, bind.Collection),
		)
		err := r.Objects.IterateObjects(ctx, bind.TenantID, bind.Collection, func(row LifecycleObjectRow) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if row.State != string(statemachine.StateAvailable) {
				return nil
			}
			committed := row.CreatedAt
			if row.CommittedAt != nil {
				committed = *row.CommittedAt
			}
			if !committed.After(cutoff) || committed.After(horizon) {
				return nil // replicated already, or left for a later tick
			}
			return r.replicateRow(ctx, log, &pass, row, committed, replicaRoute{
				bucket: b, dstBackend: dstBackend, dstBucket: dstBucket, bind: bind, filter: filter,
			})
		})
		if err != nil && !errors.Is(err, errPassStopped) && ctx.Err() == nil {
			log.Warn("failed to iterate objects", zap.Error(err))
			keepFirst(&pass.held, err)
			pass.stopped = true
		}
	}
	if ctx.Err() != nil {
		return pass.held // stopping: nothing failed, and nothing advances
	}
	if pass.held != nil || r.Replicator == nil {
		bucketLog.Info("replication pass held the watermark",
			zap.Int("copied", pass.copied), zap.Int("filtered", pass.filtered), zap.Int("source_missing", pass.missing),
			zap.Time("watermark", cutoff), zap.Bool("dry_run", r.Replicator == nil))
		return pass.held
	}
	if pass.covered.IsZero() {
		bucketLog.Debug("replication pass found nothing new", zap.Time("watermark", cutoff))
		return nil
	}
	if err := r.advance(ctx, b.BackendID, b.BucketName, pass.covered); err != nil {
		return err
	}
	bucketLog.Info("replication pass complete",
		zap.Int("copied", pass.copied), zap.Int("filtered", pass.filtered), zap.Int("source_missing", pass.missing),
		zap.Time("watermark", pass.covered))
	return nil
}

// admitBoth asks the gate about both sides of a bucket's replication: Read on
// the source, BytesWrite on the destination. A refusal is a deferral, not a
// failure — false with no error, logged once a tick by the gate's deferrals —
// while a destination whose state cannot be read is a failure.
func (r *ReplicationWorker) admitBoth(
	ctx context.Context, log *zap.Logger, rb ReplicationBucket, dstBackend, dstBucket string, gates replicationGates,
) (bool, error) {
	if !gates.src.admit(bucketGateRow{
		ID: rb.ID, BackendName: rb.BackendID, BucketName: rb.BucketName,
		Enabled: rb.State.Enabled, ReadOnly: rb.State.ReadOnly, ProvisionState: rb.State.ProvisionState,
	}) {
		return false, nil
	}
	dst, err := r.Buckets.BucketGateState(ctx, dstBackend, dstBucket)
	if err != nil {
		log.Warn("failed to read the replication destination's state; deferring the bucket to the next tick", zap.Error(err))
		return false, err
	}
	return gates.dst.admit(bucketGateRow{
		ID: dst.ID, BackendName: dstBackend, BucketName: dstBucket,
		Enabled: dst.State.Enabled, ReadOnly: dst.State.ReadOnly, ProvisionState: dst.State.ProvisionState,
	}), nil
}

// replicaRoute is where one collection's objects replicate from and to, and
// the bucket's compiled filter (nil: every object).
type replicaRoute struct {
	bucket                admindomain.Bucket
	dstBackend, dstBucket string
	bind                  CollectionBinding
	filter                celpkg.Program
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

// replicateRow copies one selected row and records the outcome on pass. It
// returns errPassStopped when the walk must end.
func (r *ReplicationWorker) replicateRow(
	ctx context.Context, log *zap.Logger, pass *bucketPass, row LifecycleObjectRow, committed time.Time, route replicaRoute,
) error {
	// The success line names the object by id alone; a failure line adds its
	// key, which an operator needs to find the object.
	okLog := log.With(zap.String(logKeyObjectID, row.ObjectID.String()))
	log = okLog.With(zap.String(logKeyKey, row.Key))
	if route.filter != nil {
		match, err := cel.Match(route.filter, lifecycleRowToCELVars(row))
		if err != nil {
			// Neither copied nor known to be outside the filter: the object
			// holds the watermark until the filter or the object changes.
			log.Warn("failed to evaluate the replication filter", zap.Error(err))
			keepFirst(&pass.held, err)
			return nil
		}
		if !match {
			pass.filtered++
			pass.decided(committed)
			return nil
		}
	}
	if r.Replicator == nil {
		okLog.Info("dry-run match")
		return nil
	}
	src := ReplicationTarget{
		BackendID: route.bucket.BackendID, BucketName: route.bucket.BucketName,
		TenantID: route.bind.TenantID, Collection: route.bind.Collection, Key: row.Key,
	}
	dst := ReplicationTarget{
		BackendID: route.dstBackend, BucketName: route.dstBucket,
		TenantID: route.bind.TenantID, Collection: route.bind.Collection, Key: row.Key,
	}
	err := r.copyWithRetry(ctx, log, src, dst, row.SizeBytes)
	switch {
	case err == nil:
		pass.copied++
		pass.decided(committed)
		okLog.Debug("replicated object", zap.Int64("size_bytes", row.SizeBytes))
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ErrObjectMissing):
		gone, why := r.confirmSourceGone(ctx, src, row)
		if !gone {
			log.Warn("source object reported missing but not confirmed; holding the watermark",
				zap.String("reason", why), zap.Error(err))
			keepFirst(&pass.held, err)
			return nil
		}
		// The row is unchanged and still AVAILABLE, and the source's bucket
		// holds no such key: nothing to copy now or later, so the object
		// does not hold the watermark.
		pass.missing++
		pass.decided(committed)
		log.Warn("source object missing; passing over it", zap.Error(err))
		return nil
	case errors.Is(err, ErrCopyMismatch):
		log.Warn("replica did not match its source after retries; holding the watermark",
			zap.Int("attempts", replicationCopyAttempts), zap.Error(err))
		keepFirst(&pass.held, err)
		return nil
	case errors.Is(err, ErrCopyTransient):
		log.Warn("failed to copy object after retries; deferring the bucket to the next tick",
			zap.Int("attempts", replicationCopyAttempts), zap.Error(err))
		keepFirst(&pass.held, err)
		pass.stopped = true
		return errPassStopped
	default:
		log.Warn("failed to copy object", zap.Error(err))
		// Carry on with the next object; the tick reports this one.
		keepFirst(&pass.held, err)
		return nil
	}
}

// copyWithRetry copies one object, retrying a transient failure with
// exponential backoff up to replicationCopyAttempts tries in all.
func (r *ReplicationWorker) copyWithRetry(ctx context.Context, log *zap.Logger, src, dst ReplicationTarget, size int64) error {
	for attempt := 1; ; attempt++ {
		err := r.Replicator.Replicate(ctx, src, dst, size)
		if err == nil || !errors.Is(err, ErrCopyTransient) || attempt >= replicationCopyAttempts {
			return err
		}
		wait := replicationBackoff(attempt)
		log.Warn("transient copy failure; retrying",
			zap.Int("attempt", attempt), zap.Duration("backoff", wait), zap.Error(err))
		if err := r.wait(ctx, wait); err != nil {
			return err
		}
	}
}

// replicationBackoff is the wait after the given failed attempt (1-based):
// replicationBackoffBase doubled per earlier attempt, capped at
// replicationBackoffMax.
func replicationBackoff(attempt int) time.Duration {
	d := replicationBackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= replicationBackoffMax {
			return replicationBackoffMax
		}
	}
	return d
}

// wait sleeps for d, or until ctx is done.
func (r *ReplicationWorker) wait(ctx context.Context, d time.Duration) error {
	if r.sleep != nil {
		return r.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// confirmSourceGone checks a copy's not-found before the object is passed
// over: the row must be unchanged — still AVAILABLE, at the version the walk
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

// clockNow is the database clock, or Now without one.
func (r *ReplicationWorker) clockNow(ctx context.Context) (time.Time, error) {
	if r.Clock == nil {
		return r.Now(), nil
	}
	return r.Clock.Now(ctx)
}

// cutoff resolves the start of a bucket's window. Layering:
//  1. in-memory cache (hot path, no DB hit)
//  2. WatermarkStore (cold start / new bucket)
//  3. now − LookbackWindow (no prior state), written as the watermark at
//     once: a window computed afresh each tick would slide, and a bucket
//     deferred or held for longer than the lookback would lose the objects
//     that slid out of it.
//
// A watermark the store fails to return is an error: the lookback in its
// place could start after objects the stored watermark had not reached, and
// the pass would then move the watermark past them uncopied.
func (r *ReplicationWorker) cutoff(ctx context.Context, backendID, bucketName string, now time.Time) (time.Time, error) {
	key := backendID + "/" + bucketName
	if t, ok := r.watermarks[key]; ok {
		return t, nil
	}
	if r.Watermarks != nil {
		t, err := r.Watermarks.Get(ctx, backendID, bucketName)
		if err != nil {
			r.log().Warn("failed to read the replication watermark; deferring the bucket to the next tick",
				zap.String(logKeyBackend, backendID), zap.String(logKeyBucket, bucketName), zap.Error(err))
			return time.Time{}, err
		}
		if !t.IsZero() {
			r.watermarks[key] = t
			return t, nil
		}
	}
	start := now.Add(-r.LookbackWindow)
	if err := r.advance(ctx, backendID, bucketName, start); err != nil {
		return time.Time{}, err
	}
	r.log().Info("replication window opened for a bucket with no watermark",
		zap.String(logKeyBackend, backendID), zap.String(logKeyBucket, bucketName), zap.Time("watermark", start))
	return start, nil
}

// advance moves the watermark forward — first in memory, then to the
// store. Monotonic: never moves backward. A store failure is returned for the
// tick to report; the in-memory mark still moves, and the next completed pass
// writes a later one.
func (r *ReplicationWorker) advance(ctx context.Context, backendID, bucketName string, t time.Time) error {
	key := backendID + "/" + bucketName
	if cur, ok := r.watermarks[key]; ok && !t.After(cur) {
		return nil
	}
	r.watermarks[key] = t
	if r.Watermarks != nil {
		if err := r.Watermarks.Advance(ctx, backendID, bucketName, t); err != nil {
			r.log().Warn("failed to advance watermark",
				zap.String(logKeyBackend, backendID),
				zap.String(logKeyBucket, bucketName),
				zap.Error(err))
			return err
		}
	}
	return nil
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

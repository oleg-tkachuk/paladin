package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The storage migration (ADR-0015, ADR-0051): a tenant switched to the
// `dedicated` layout has its objects moved, one by one, from its shared
// bucket into its own.
//
// An object records the bucket its bytes are in (objects.placement_bucket_id),
// and every read follows it; the collection's binding decides only where new
// uploads are signed. So a migration has no instant at which a write can fall
// on the wrong side:
//
//	provisioning -> moving -> completed -> cleaned
//	                   \-> rolling_back -> failed -> (settled)
//
//   - provisioning waits for the target bucket; a failed provisioning fails
//     the migration with nothing changed.
//   - moving points the tenant's private collections and default binding at
//     the target — each pass again, for a collection created from a binding
//     read before — and records the source's write deadline, which a
//     regenerated or part URL for it expires by. Each pass then moves the
//     objects placed in the source whose upload anchor has settled: no URL
//     can write their key there any more. A move copies the bytes, verifies
//     the copy and sets the placement, compare-and-set on the object's row.
//     A write through a URL signed before the switch places its object in
//     the source, and a later pass moves it again.
//   - completed is a data condition, checked at each pass's end: nothing of
//     the tenant placed in or anchored to the source, no multipart session
//     there, nothing bound to it. Every URL that can write the source is one
//     of those anchors or sessions until the reconciler settles it, so no
//     clock is waited for. The source copies are kept until cleanup_after,
//     for the GET URLs signed for them (SourceCopyRetention).
//   - completed keeps passing: a request that read a binding before the
//     switch may still anchor an upload to the source, or create a
//     collection bound to it, after the completion. Its object is moved and
//     its collection pointed at the target like any other, and the cleanup
//     waits for nothing to hold the source.
//   - cleaned: the source copy of every object placed in the target is
//     deleted.
//   - rolling_back is the same pass with the buckets swapped, entered when
//     the target bucket leaves ready or moves keep failing their
//     verification; it ends in failed, which a retry starts over.
//   - failed after a rollback keeps passing, as completed does: a request
//     that read the target binding before the switch back may anchor an
//     upload to the target, or bind a collection to it, after the rollback
//     ended. Its collection is pointed at the source again and its object
//     moved back once settled. The passes stop — the migration settles — at
//     the end of a pass past settle_after with nothing holding the target.
//     A migration that failed in provisioning had nothing pointed at its
//     target and is settled as it fails.
//
// Public collections (ADR-0027) are skipped throughout — the repository's
// lists leave them out. A public collection only binds to a public bucket,
// the tenant's dedicated bucket is private, and the schema refuses to rebind
// one (`collections_public_bucket_fixed`) because every URL a consumer stored
// for its objects names the bucket. A tenant created `dedicated` keeps its
// public collections in a public bucket outside its own just the same.

// Migration states, as package migstate defines them for every package that
// must agree on them.
const (
	MigStateProvisioning = migstate.Provisioning
	MigStateMoving       = migstate.Moving
	MigStateCompleted    = migstate.Completed
	MigStateCleaned      = migstate.Cleaned
	MigStateFailed       = migstate.Failed
	MigStateRollingBack  = migstate.RollingBack
)

// The storage layouts a migration moves a tenant between.
const (
	LayoutShared    = "shared"
	LayoutDedicated = "dedicated"
)

// CopyLocation is a physical object address for a server-side copy. Worker-local
// (not internal/api/data/v1/objecth.Location) because that package imports worker —
// importing it back would be a cycle. build_jobs adapts the router to this.
type CopyLocation struct {
	BackendID  string
	TenantID   uuid.UUID
	Bucket     string
	Collection string
	Key        string
}

// ObjectCopier copies one object between physical locations under the same
// key: server-side on one backend, streamed across two.
type ObjectCopier interface {
	CopyObject(ctx context.Context, src, dst CopyLocation) error
}

// ObjectDeleter permanently removes one physical object. Used by the cleanup
// to drop the source copies.
type ObjectDeleter interface {
	DeleteObject(ctx context.Context, loc CopyLocation) error
}

// ObjectHeader reports a physical object's size. An object the backend says
// is not there is reported as an error wrapping ErrObjectMissing; any other
// error is one the caller cannot read anything into.
type ObjectHeader interface {
	HeadObject(ctx context.Context, loc CopyLocation) (sizeBytes int64, err error)
}

// ErrObjectMissing reports that the backend has no object at the location.
var ErrObjectMissing = errors.New("object not in the bucket")

// ObjectIdentity reports a physical object's ETag. An object the backend says
// is not there is an error wrapping ErrObjectMissing.
type ObjectIdentity interface {
	ObjectETag(ctx context.Context, loc CopyLocation) (string, error)
}

// MigrationBucket is one of a migration's two buckets: its row id, and the
// names S3 is addressed by.
type MigrationBucket struct {
	ID        uuid.UUID
	BackendID string
	Bucket    string
}

// StorageMigration is one tenant's in-flight migration.
type StorageMigration struct {
	TenantID uuid.UUID
	Source   MigrationBucket
	Target   MigrationBucket
	State    string
	// ObjectsTotal is the objects moved plus those still in the bucket being
	// emptied, as of the last pass's end; ObjectsCopied the moves.
	ObjectsTotal  int64
	ObjectsCopied int64
	// CursorCollection and CursorKey are the cleanup's progress.
	CursorCollection string
	CursorKey        string
	// CleanupAfter is when the source copies may be deleted; zero until the
	// migration is 'completed'.
	CleanupAfter time.Time
	// SettleAfter is the least time a rolled-back migration keeps passing
	// after it failed; zero until the rollback ends.
	SettleAfter time.Time
	// Attempts counts the migration's failures; a retry keeps it, so it tells
	// one attempt from the next.
	Attempts int32
}

// MigrationRead is a migration as the worker read it: which tenant's, in what
// state, on which attempt. Every write names it, and applies only while the
// migration is still there — same state and same attempt.
type MigrationRead struct {
	TenantID uuid.UUID
	State    string
	Attempts int32
}

// Read is the migration as read, for the writes that act on it.
func (m StorageMigration) Read() MigrationRead {
	return MigrationRead{TenantID: m.TenantID, State: m.State, Attempts: m.Attempts}
}

// Reverse reports whether m's state moves objects from the target back into
// the source: rolling back, and failed while its passes go on.
func (m StorageMigration) Reverse() bool {
	return Reverses(m.State)
}

// Reverses reports whether a migration in state moves objects back into its
// source.
func Reverses(state string) bool {
	return state == MigStateRollingBack || state == MigStateFailed
}

// direction is the bucket m's state empties, the one it fills and the layout
// the tenant has meanwhile: the source into the target while moving, back
// while rolling back and after it.
func (m StorageMigration) direction() (from, to MigrationBucket, layout string) {
	if m.Reverse() {
		return m.Target, m.Source, LayoutShared
	}
	return m.Source, m.Target, LayoutDedicated
}

// loc is o's address in b.
func (m StorageMigration) loc(b MigrationBucket, collection, key string) CopyLocation {
	return CopyLocation{BackendID: b.BackendID, TenantID: m.TenantID, Bucket: b.Bucket, Collection: collection, Key: key}
}

// MoveCandidate is one object a pass may move: its row, where it is, and
// what the move compares — the ETag the bucket being emptied reported for it
// and its content version (bytes_written_at).
type MoveCandidate struct {
	ID         uuid.UUID
	Collection string
	Key        string
	SizeBytes  int64
	ETag       string
	// Trashed: the row is in the trash. Its bytes stay until the lifecycle
	// purge, so RestoreObject can bring it back, and they move like a live
	// object's — unless a delete straight from the bucket took them first.
	Trashed   bool
	WrittenAt *time.Time
}

// ObjectRef is a logical (collection, key) pair the cleanup walks.
type ObjectRef struct {
	Collection string
	Key        string
}

// MigrationHolds is what keeps the bucket being emptied in use (ADR-0051).
type MigrationHolds struct {
	// Placed: the tenant's objects whose bytes are still there.
	Placed int64
	// Anchored: the tenant's rows whose upload URL was signed for it, and
	// not settled: a URL may still write there.
	Anchored int64
	// Sessions: the tenant's multipart sessions there.
	Sessions int64
	// Bound: the tenant's private collections and default binding still
	// bound to it.
	Bound int64
}

// Clear reports whether nothing keeps the bucket in use. It is decided on
// the rows alone: a URL that can still write the bucket keeps its row
// anchored there, or its multipart session open, until it cannot.
func (h MigrationHolds) Clear() bool {
	return h.Placed == 0 && h.Anchored == 0 && h.Sessions == 0 && h.Bound == 0
}

// MigrationRepo is the persistence seam for the migration.
//
// The writes name the migration as it was read (MigrationRead) and apply
// only while it still holds — the same state and the same attempt; otherwise
// they change nothing and return ErrMigrationMoved. A worker resumed after a
// pause past its lease thereby cannot drag back a migration its successor
// moved on, nor, by the attempt, act on a retry of the attempt it read.
type MigrationRepo interface {
	ListActive(ctx context.Context, limit int) ([]StorageMigration, error)
	// BucketProvisionState returns a bucket's provision_state.
	BucketProvisionState(ctx context.Context, backendID, bucketName string) (string, error)
	// EnterMoving moves provisioning -> moving.
	EnterMoving(ctx context.Context, at MigrationRead) error
	// Repoint, in one transaction under the migration row's lock, points
	// the tenant's private collections bound to from at to, and its default
	// binding when it names from or none; sets the tenant's storage layout;
	// and records, once per direction, when the last URL signed for from
	// expires: now plus writeWindow. A collection the schema refuses is a
	// *RebindRefusedError, and nothing changes.
	Repoint(ctx context.Context, at MigrationRead, from, to MigrationBucket, layout string, writeWindow time.Duration) error
	// ListMovable lists one batch of the tenant's live and trashed private
	// objects placed in from whose upload anchor has settled, after the id
	// after, by id.
	ListMovable(ctx context.Context, at MigrationRead, from MigrationBucket, after uuid.UUID, limit int) ([]MoveCandidate, error)
	// MoveObject moves one object: on one transaction it holds the
	// object's row against a delete and the migration row as read, re-reads
	// the candidate, runs fn — the copy — and places the object in the
	// bucket being filled with the ETag fn returns, compare-and-set on the
	// row being still placed in the bucket being emptied, settled, at the
	// content version fn copied. It reports whether the object moved: no row, or a row that
	// changed, is false. A migration no longer as read is ErrMigrationMoved.
	MoveObject(ctx context.Context, at MigrationRead, from, to MigrationBucket, c MoveCandidate,
		fn func(context.Context, MoveCandidate) (string, error)) (bool, error)
	// Holds reads what keeps from in use.
	Holds(ctx context.Context, at MigrationRead, from MigrationBucket) (MigrationHolds, error)
	// RecordPass records a pass's end — whether a move failed its
	// verification, and how many objects remain in the bucket being
	// emptied — and returns the passes in a row that failed.
	RecordPass(ctx context.Context, at MigrationRead, failed bool, remaining int64) (int32, error)
	// Complete moves moving -> completed, keeping the source copies until
	// the later of the operator's retention and sourceRetention from now.
	Complete(ctx context.Context, at MigrationRead, sourceRetention time.Duration) error
	// ListCleanable lists the tenant's private objects placed in target
	// after the cursor, by (collection, key).
	ListCleanable(ctx context.Context, at MigrationRead, target MigrationBucket, afterCollection, afterKey string, limit int) ([]ObjectRef, error)
	// AdvanceCleanup records the cleanup past (collection, key).
	AdvanceCleanup(ctx context.Context, at MigrationRead, collection, key string) error
	// MarkCleaned is the terminal transition after the source copies are
	// deleted.
	MarkCleaned(ctx context.Context, at MigrationRead) error
	// StartRollback moves moving -> rolling_back, recording reason as the
	// migration's error and counting the failure.
	StartRollback(ctx context.Context, at MigrationRead, reason string) error
	// FinishRollback moves rolling_back -> failed, keeping the reason; the
	// failed migration's passes go on until at least settleWindow from now.
	FinishRollback(ctx context.Context, at MigrationRead, settleWindow time.Duration) error
	// Settle stops a failed migration's passes.
	Settle(ctx context.Context, at MigrationRead) error
	// Fail moves provisioning -> failed, settled: nothing was pointed at the
	// target.
	Fail(ctx context.Context, at MigrationRead, reason string) error
}

// ErrMigrationMoved reports a transition that did not apply because the
// migration is no longer as it was read: another state, or another attempt.
var ErrMigrationMoved = errors.New("storage migration moved on since it was read")

// ErrRebindRefused reports a rebind the schema refused (a collection the
// bucket rules will not let move). Retrying cannot change the answer.
var ErrRebindRefused = errors.New("schema refused the rebind")

// RebindRefusedError is ErrRebindRefused for one collection, with the
// schema's refusal as its cause: the collection is the migration's recorded
// reason, the cause is the log's.
type RebindRefusedError struct {
	Collection string
	cause      error
}

// RebindRefused is the schema refusing to move collection, for cause.
func RebindRefused(collection string, cause error) error {
	return &RebindRefusedError{Collection: collection, cause: cause}
}

func (e *RebindRefusedError) Error() string {
	return fmt.Sprintf("rebind %q: %v: %v", e.Collection, ErrRebindRefused, e.cause)
}

func (e *RebindRefusedError) Unwrap() []error { return []error{ErrRebindRefused, e.cause} }

// ErrMoveMismatch is a move whose copy did not verify: the bucket being
// emptied holds other bytes than the row records, or the copy came out
// another size or digest. Nothing moved; the pass counts it.
var ErrMoveMismatch = errors.New("the copy does not match the object")

// The reasons a migration records, which GetTenantStorageMigration returns to
// the operator: what failed, in the product's terms. The error behind each is
// logged where it happens.
const (
	reasonTargetProvisionFailed = "target bucket provisioning failed"
	reasonTargetNotReady        = "target bucket is %s"
	reasonRebindRefused         = "rebind refused: collection %q cannot move to the dedicated bucket"
	reasonRebindRefusedAny      = "rebind refused: a collection cannot move to the dedicated bucket"
	reasonMovesFailing          = "moves failed their verification on %d passes in a row: %s"
	problemSourceChanged        = "the bucket holds other bytes than the object records: ETag %q, recorded %q"
	problemSize                 = "the copy's size %d is not the object's %d"
	problemDigest               = "the copy's digest %q is not the source's %q"
)

// rebindReason is the recorded reason for a rebind the schema refused.
func rebindReason(err error) string {
	if rr, ok := errors.AsType[*RebindRefusedError](err); ok {
		return fmt.Sprintf(reasonRebindRefused, rr.Collection)
	}
	return reasonRebindRefusedAny
}

// Defaults for a StorageMigrationWorker field left zero.
const (
	DefaultStorageMigrationInterval = 30 * time.Second
	DefaultStorageMigrationBatch    = 100
	// activeMigrationsPerTick bounds the migrations one tick advances.
	activeMigrationsPerTick = 50
)

// StorageMigrationWorker drives active migrations to completion.
type StorageMigrationWorker struct {
	Repo    MigrationRepo
	Copier  ObjectCopier
	Deleter ObjectDeleter
	Header  ObjectHeader
	// Identity reads ETags, which a move compares; nil compares none.
	Identity ObjectIdentity
	// WriteWindow is the longest a presigned URL lives
	// (limits.presign.max_ttl): the bucket being emptied gets its write
	// deadline that long after its first pass, and a regenerated or part URL
	// for it expires by then. The completion does not wait for it.
	WriteWindow time.Duration
	// SourceCopyRetention is the least the source copies are kept after the
	// completion (worker.jobs.storage_migration.source_copy_retention), for
	// the GET URLs signed for them before their objects moved: GET URLs leave
	// no row to wait for.
	SourceCopyRetention time.Duration
	// RollbackSettleWindow is the least a rolled-back migration keeps passing
	// after it failed, for a request that read the target binding before the
	// switch back and anchors an upload or binds a collection to it after.
	// Wired from source_copy_retention, the bound the forward direction gives
	// the same request before its irreversible step (ADR-0051 amendment).
	RollbackSettleWindow time.Duration
	Interval             time.Duration
	CopyBatch            int
	Logger               *zap.Logger
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time

	mu sync.Mutex
	// passes is each migration's pass in progress, by tenant: a pass walks
	// the movable objects by id over as many ticks as it takes. A restarted
	// worker starts the pass over, which moves nothing twice: a moved
	// object is no longer placed in the bucket being emptied.
	passes map[uuid.UUID]*movePass
}

// movePass is one walk over a migration's movable objects.
type movePass struct {
	at     MigrationRead
	after  uuid.UUID
	failed bool
	// lastFailure is the last move that failed its verification, for the
	// reason a rollback records.
	lastFailure string
}

func (w *StorageMigrationWorker) Run(ctx context.Context) error {
	return RunTicker(ctx, "storage-migration", w.interval(), w.Tick)
}

// Tick advances every active migration one step, carrying on past one that
// fails, and returns the first failure. A step dropped because the migration
// moved on is not one. Exported for tests, which drive the worker a tick at a
// time.
func (w *StorageMigrationWorker) Tick(ctx context.Context) error {
	migs, err := w.Repo.ListActive(ctx, activeMigrationsPerTick)
	if err != nil {
		w.log().Warn("list active migrations failed", zap.Error(err))
		return err
	}
	var first error
	for _, m := range migs {
		if ctx.Err() != nil {
			return first
		}
		// A step error is transient — log and retry on the next tick. Terminal
		// failures set state='failed' inside the step and return nil.
		if err := w.advance(ctx, m); errors.Is(err, ErrMigrationMoved) {
			w.forgetPass(m.TenantID)
			w.log().Info("migration moved on since it was listed; step dropped",
				zap.String("tenant_id", m.TenantID.String()), zap.String("state", m.State))
		} else if err != nil {
			w.log().Warn("migration step failed; will retry",
				zap.String("tenant_id", m.TenantID.String()),
				zap.String("state", m.State), zap.Error(err))
			keepFirst(&first, err)
		}
	}
	return first
}

func (w *StorageMigrationWorker) advance(ctx context.Context, m StorageMigration) error {
	switch m.State {
	case MigStateProvisioning:
		return w.stepProvisioning(ctx, m)
	case MigStateMoving:
		return w.stepMoving(ctx, m)
	case MigStateCompleted:
		return w.stepCompleted(ctx, m)
	case MigStateRollingBack, MigStateFailed:
		return w.stepRollingBack(ctx, m)
	default:
		return nil
	}
}

// stepProvisioning waits for the reconciler to make the target bucket ready,
// then starts moving.
func (w *StorageMigrationWorker) stepProvisioning(ctx context.Context, m StorageMigration) error {
	state, err := w.Repo.BucketProvisionState(ctx, m.Target.BackendID, m.Target.Bucket)
	if err != nil {
		return fmt.Errorf("read target bucket state: %w", err)
	}
	switch state {
	case bucketgate.ProvisionReady:
	case bucketgate.ProvisionFailed:
		w.log().Warn("target bucket provisioning failed; failing the migration",
			zap.String("tenant_id", m.TenantID.String()), zap.String("target_bucket", m.Target.Bucket))
		return w.Repo.Fail(ctx, m.Read(), reasonTargetProvisionFailed)
	default:
		return nil // still pending; wait for the bucket reconciler
	}
	if err := w.Repo.EnterMoving(ctx, m.Read()); err != nil {
		return fmt.Errorf("start moving: %w", err)
	}
	w.log().Info("migration starts moving objects",
		zap.String("tenant_id", m.TenantID.String()),
		zap.String("source_bucket", m.Source.Bucket), zap.String("target_bucket", m.Target.Bucket))
	return nil
}

// stepMoving runs one tick of a pass from the source into the target, after
// checking the target can still take the objects: one that left ready rolls
// the migration back.
func (w *StorageMigrationWorker) stepMoving(ctx context.Context, m StorageMigration) error {
	state, err := w.Repo.BucketProvisionState(ctx, m.Target.BackendID, m.Target.Bucket)
	if err != nil {
		return fmt.Errorf("read target bucket state: %w", err)
	}
	if state != bucketgate.ProvisionReady {
		return w.rollBack(ctx, m, fmt.Sprintf(reasonTargetNotReady, state))
	}
	return w.pass(ctx, m)
}

// stepRollingBack runs one tick of a pass from the target back into the
// source, rolling back or failed and not yet settled. A source that cannot
// take them is waited for.
func (w *StorageMigrationWorker) stepRollingBack(ctx context.Context, m StorageMigration) error {
	state, err := w.Repo.BucketProvisionState(ctx, m.Source.BackendID, m.Source.Bucket)
	if err != nil {
		return fmt.Errorf("read source bucket state: %w", err)
	}
	if state != bucketgate.ProvisionReady {
		w.log().Warn("source bucket is not ready; the rollback waits for it",
			zap.String("tenant_id", m.TenantID.String()), zap.String("source_bucket", m.Source.Bucket),
			zap.String("provision_state", state))
		return nil
	}
	return w.pass(ctx, m)
}

// pass runs one tick of a pass in m's direction: it points the tenant's
// uploads at the bucket being filled, moves one batch, and at the pass's end
// records it and finishes the direction when nothing holds the bucket being
// emptied.
func (w *StorageMigrationWorker) pass(ctx context.Context, m StorageMigration) error {
	from, to, layout := m.direction()
	log := w.log().With(zap.String("tenant_id", m.TenantID.String()), zap.String("state", m.State),
		zap.String("from_bucket", from.Bucket), zap.String("to_bucket", to.Bucket))
	if err := w.Repo.Repoint(ctx, m.Read(), from, to, layout, w.WriteWindow); err != nil {
		if errors.Is(err, ErrRebindRefused) && m.State == MigStateMoving {
			log.Error("the schema refuses to move a collection; rolling the migration back", zap.Error(err))
			return w.rollBack(ctx, m, rebindReason(err))
		}
		return fmt.Errorf("point the tenant's uploads at %s: %w", to.Bucket, err)
	}
	p := w.passFor(m)
	batch, err := w.Repo.ListMovable(ctx, m.Read(), from, p.after, w.batch())
	if err != nil {
		return fmt.Errorf("list movable objects: %w", err)
	}
	for _, c := range batch {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.move(ctx, log, m, from, to, c, p); err != nil {
			return err
		}
	}
	if len(batch) == w.batch() {
		return nil // more to list: the pass goes on next tick
	}
	return w.endPass(ctx, log, m, from, p)
}

// move moves one candidate and records how it went on the pass: a move that
// failed its verification is counted and passed over; any other failure
// ends the tick, and the next one tries the object again.
func (w *StorageMigrationWorker) move(ctx context.Context, log *zap.Logger, m StorageMigration, from, to MigrationBucket, c MoveCandidate, p *movePass) error {
	log = log.With(zap.String("object_id", c.ID.String()), zap.String("collection", c.Collection), zap.String("key", c.Key))
	moved, err := w.Repo.MoveObject(ctx, m.Read(), from, to, c, func(ctx context.Context, held MoveCandidate) (string, error) {
		return w.moveBytes(ctx, log, m, from, to, held)
	})
	switch {
	case errors.Is(err, ErrMoveMismatch):
		log.Warn("move did not verify; the object stays where it is this pass", zap.Error(err))
		p.failed, p.lastFailure = true, fmt.Sprintf("%s/%s: %v", c.Collection, c.Key, err)
	case err != nil:
		return fmt.Errorf("move %s/%s: %w", c.Collection, c.Key, err)
	case !moved:
		log.Info("object changed since it was listed; a later pass moves it")
	default:
		log.Debug("object moved")
	}
	p.after = c.ID
	return nil
}

// endPass records the pass, rolls back a migration whose moves keep failing,
// and finishes the direction when nothing holds the bucket being emptied —
// on a completed migration, cleans the source then.
func (w *StorageMigrationWorker) endPass(ctx context.Context, log *zap.Logger, m StorageMigration, from MigrationBucket, p *movePass) error {
	w.forgetPass(m.TenantID)
	holds, err := w.Repo.Holds(ctx, m.Read(), from)
	if err != nil {
		return fmt.Errorf("read what holds %s: %w", from.Bucket, err)
	}
	failedPasses, err := w.Repo.RecordPass(ctx, m.Read(), p.failed, holds.Placed)
	if err != nil {
		return fmt.Errorf("record the pass: %w", err)
	}
	if p.failed && m.State == MigStateMoving && failedPasses >= migstate.MaxFailedPasses {
		log.Error("moves keep failing their verification; rolling the migration back",
			zap.Int32("failed_passes", failedPasses), zap.String("last_failure", p.lastFailure))
		return w.rollBack(ctx, m, fmt.Sprintf(reasonMovesFailing, failedPasses, p.lastFailure))
	}
	if !holds.Clear() {
		log.Info("pass done; the bucket being emptied is still in use",
			zap.Int64("placed", holds.Placed), zap.Int64("anchored", holds.Anchored),
			zap.Int64("sessions", holds.Sessions), zap.Int64("bound", holds.Bound),
			zap.Bool("failed", p.failed))
		return nil
	}
	switch m.State {
	case MigStateCompleted:
		return w.clean(ctx, log, m)
	case MigStateRollingBack:
		if err := w.Repo.FinishRollback(ctx, m.Read(), w.RollbackSettleWindow); err != nil {
			return fmt.Errorf("finish the rollback: %w", err)
		}
		log.Info("migration rolled back; the tenant is on its source bucket again",
			zap.Duration("settle_window", w.RollbackSettleWindow))
		return nil
	case MigStateFailed:
		return w.settle(ctx, log, m)
	}
	if err := w.Repo.Complete(ctx, m.Read(), w.SourceCopyRetention); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	log.Info("migration completed; every object is in the target and no URL can write the source",
		zap.Duration("source_copy_retention", w.SourceCopyRetention))
	return nil
}

// settle stops a failed migration's passes once settle_after has passed;
// endPass calls it only while nothing holds the target.
func (w *StorageMigrationWorker) settle(ctx context.Context, log *zap.Logger, m StorageMigration) error {
	if w.now().Before(m.SettleAfter) {
		log.Debug("the rolled-back migration keeps passing for requests that read the target binding before the switch back",
			zap.Time("settle_after", m.SettleAfter))
		return nil
	}
	if err := w.Repo.Settle(ctx, m.Read()); err != nil {
		return fmt.Errorf("settle the rolled-back migration: %w", err)
	}
	log.Info("rolled-back migration settled; nothing of the tenant is left in the target")
	return nil
}

// rollBack starts the rollback of m for reason.
func (w *StorageMigrationWorker) rollBack(ctx context.Context, m StorageMigration, reason string) error {
	w.forgetPass(m.TenantID)
	if err := w.Repo.StartRollback(ctx, m.Read(), reason); err != nil {
		return fmt.Errorf("start the rollback: %w", err)
	}
	w.log().Warn("migration rolling back", zap.String("tenant_id", m.TenantID.String()), zap.String("reason", reason))
	return nil
}

// moveBytes copies c's bytes from the bucket being emptied into the one
// being filled, under the same key, and verifies the copy, returning the ETag
// the row records for it. A trashed
// object whose bytes are already gone moves without a copy: there is
// nothing to read in either bucket, and RestoreObject refuses it wherever it
// is placed.
//
// What it compares (ADR-0051): the ETag from reports with the one the row
// recorded from it — the same bucket's, so comparable — so it never moves
// bytes the row does not record; the copy's size with the row's; and the
// copy's ETag with the source's where both are a plain MD5 digest, which a
// server-side copy and a single-part upload keep.
func (w *StorageMigrationWorker) moveBytes(ctx context.Context, log *zap.Logger, m StorageMigration, from, to MigrationBucket, c MoveCandidate) (string, error) {
	src, dst := m.loc(from, c.Collection, c.Key), m.loc(to, c.Collection, c.Key)
	if c.Trashed {
		gone, err := w.bytesGone(ctx, src)
		if err != nil {
			return "", fmt.Errorf("head source: %w", err)
		}
		if gone {
			log.Warn("trashed object has no bytes in the bucket being emptied; moving it without a copy")
			return "", nil
		}
	}
	var srcETag string
	if w.Identity != nil {
		etag, err := w.Identity.ObjectETag(ctx, src)
		if err != nil {
			return "", fmt.Errorf("read the source's ETag: %w", err)
		}
		srcETag = statemachine.NormalizeETag(etag)
		if c.ETag != "" && srcETag != statemachine.NormalizeETag(c.ETag) {
			return "", fmt.Errorf("%w: "+problemSourceChanged, ErrMoveMismatch, srcETag, c.ETag)
		}
	}
	if err := w.Copier.CopyObject(ctx, src, dst); err != nil {
		return "", fmt.Errorf("copy: %w", err)
	}
	size, err := w.Header.HeadObject(ctx, dst)
	if err != nil {
		return "", fmt.Errorf("head the copy: %w", err)
	}
	if size != c.SizeBytes {
		return "", fmt.Errorf("%w: "+problemSize, ErrMoveMismatch, size, c.SizeBytes)
	}
	if w.Identity == nil {
		return "", nil
	}
	dstETag, err := w.Identity.ObjectETag(ctx, dst)
	if err != nil {
		return "", fmt.Errorf("read the copy's ETag: %w", err)
	}
	dstETag = statemachine.NormalizeETag(dstETag)
	if plainMD5(srcETag) && plainMD5(dstETag) && srcETag != dstETag {
		return "", fmt.Errorf("%w: "+problemDigest, ErrMoveMismatch, dstETag, srcETag)
	}
	return dstETag, nil
}

// md5HexLength is how long an ETag that is a plain MD5 digest is: 16 bytes in
// hex. A multipart ETag carries a "-<parts>" suffix and a store's own scheme
// another shape.
const md5HexLength = 32

// plainMD5 reports whether etag is a plain MD5 digest, comparable across a
// copy.
func plainMD5(etag string) bool {
	if len(etag) != md5HexLength {
		return false
	}
	return strings.IndexFunc(etag, func(r rune) bool {
		return (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F')
	}) < 0
}

// bytesGone reports whether the backend confirms there is no object at loc.
// Without a Header nothing is confirmed, and the copy is attempted.
func (w *StorageMigrationWorker) bytesGone(ctx context.Context, loc CopyLocation) (bool, error) {
	if w.Header == nil {
		return false, nil
	}
	_, err := w.Header.HeadObject(ctx, loc)
	if errors.Is(err, ErrObjectMissing) {
		return true, nil
	}
	return false, err
}

// stepCompleted runs one tick of a pass, as moving does: a request that read
// a binding before the switch may have anchored an upload to the source or
// bound a collection to it after the completion, and the pass moves it like
// any other. At the pass's end, with nothing holding the source, endPass
// cleans it (clean). A target that left ready is waited for: the objects
// are in it already, and nothing is rolled back from here.
func (w *StorageMigrationWorker) stepCompleted(ctx context.Context, m StorageMigration) error {
	state, err := w.Repo.BucketProvisionState(ctx, m.Target.BackendID, m.Target.Bucket)
	if err != nil {
		return fmt.Errorf("read target bucket state: %w", err)
	}
	if state != bucketgate.ProvisionReady {
		w.log().Warn("target bucket is not ready; the completed migration's cleanup waits for it",
			zap.String("tenant_id", m.TenantID.String()), zap.String("target_bucket", m.Target.Bucket),
			zap.String("provision_state", state))
		return nil
	}
	return w.pass(ctx, m)
}

// clean deletes, once cleanup_after has passed, the source copy of every
// object placed in the target, one batch a tick, and marks the migration
// cleaned when the walk ends; endPass calls it only while nothing holds the
// source. Deleting an absent key succeeds, so an object that never had a
// source copy costs one request, and a walk resumed after a crash repeats
// nothing harmful.
func (w *StorageMigrationWorker) clean(ctx context.Context, log *zap.Logger, m StorageMigration) error {
	if m.CleanupAfter.IsZero() || w.now().Before(m.CleanupAfter) {
		log.Debug("the source copies are kept for the GET URLs signed for them; cleanup deferred",
			zap.Time("cleanup_after", m.CleanupAfter))
		return nil
	}
	objs, err := w.Repo.ListCleanable(ctx, m.Read(), m.Target, m.CursorCollection, m.CursorKey, w.batch())
	if err != nil {
		return fmt.Errorf("list objects to clean: %w", err)
	}
	if len(objs) == 0 {
		if err := w.Repo.MarkCleaned(ctx, m.Read()); err != nil {
			return err
		}
		log.Info("migration cleaned; the source copies are deleted")
		return nil
	}
	for _, o := range objs {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.Deleter.DeleteObject(ctx, m.loc(m.Source, o.Collection, o.Key)); err != nil {
			return fmt.Errorf("delete the source copy of %s/%s: %w", o.Collection, o.Key, err)
		}
		if err := w.Repo.AdvanceCleanup(ctx, m.Read(), o.Collection, o.Key); err != nil {
			return err
		}
	}
	log.Info("source copies deleted", zap.Int("objects", len(objs)))
	return nil
}

// passFor is m's pass in progress, a new one when there is none or m is no
// longer the migration it was walked for.
func (w *StorageMigrationWorker) passFor(m StorageMigration) *movePass {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.passes == nil {
		w.passes = map[uuid.UUID]*movePass{}
	}
	p, ok := w.passes[m.TenantID]
	if !ok || p.at != m.Read() {
		p = &movePass{at: m.Read(), after: uuid.Nil()}
		w.passes[m.TenantID] = p
	}
	return p
}

// forgetPass drops the tenant's pass in progress.
func (w *StorageMigrationWorker) forgetPass(tenantID uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.passes, tenantID)
}

func (w *StorageMigrationWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *StorageMigrationWorker) interval() time.Duration {
	if w.Interval > 0 {
		return w.Interval
	}
	return DefaultStorageMigrationInterval
}

func (w *StorageMigrationWorker) batch() int {
	if w.CopyBatch > 0 {
		return w.CopyBatch
	}
	return DefaultStorageMigrationBatch
}

func (w *StorageMigrationWorker) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}

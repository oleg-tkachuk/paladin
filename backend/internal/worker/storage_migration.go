package worker

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
)

// ADR-0015 Phase 3 (slice 1): the shared->dedicated storage migration copy job.
// A tenant switched to the `dedicated` layout keeps serving from its shared
// bucket until this worker copies every object into the tenant's own bucket
// (same key, server-side CopyObject), rebinds the collections in one
// transaction, and flips the layout. Crash-safe + resumable via the
// tenant_storage_migrations row (the schema baseline (001_initial_schema.sql)).
//
// State machine (driven here):
//   provisioning -> copying -> rebinding -> verifying -> completed -> cleaned
//                                               \-> rolling_back -> failed
// Terminal failures before the rebind (target-bucket provisioning failed, a
// copy that ended short, a rebind the schema refuses) set `failed`; the
// tenant was never rebound, so it is still shared and the operator can retry.
// A failed physical verify comes AFTER the rebind, with the tenant already
// serving from the dedicated bucket: it moves to `rolling_back`, which copies
// back what was written to the dedicated bucket since the rebind, rebinds the
// tenant to its source bucket and only then sets `failed` — the same
// retryable state as any other failure (stepRollingBack says how). Transient
// errors (a DB blip, one failed CopyObject) are retried on the next tick
// without abandoning the migration.
//
// Public collections (ADR-0027) are skipped throughout — the repository's
// object and collection lists leave them out, so they are never copied,
// rebound, verified or cleaned up. They cannot move: a public collection only
// binds to a public bucket, the tenant's dedicated bucket is private (a
// migration target is never public), and the schema refuses to rebind one
// (`collections_public_bucket_fixed`) because every URL a consumer stored for
// its objects names the bucket. Skipping, rather than refusing a tenant that
// has any, is what keeps the result consistent: a tenant created `dedicated`
// keeps its public collections in a public bucket outside its own just the
// same, so `dedicated` already means "private data in the tenant's bucket",
// and a migrated tenant ends in exactly that state. Refusing instead would
// bar any tenant that ever published from migrating for good, as a public
// collection can only go away by deleting it, which breaks its URLs. Since
// the lists are read on every step, a public collection created mid-migration
// is skipped too.

// Migration states, as package migstate defines them for every package that
// must agree on them.
const (
	MigStateProvisioning = migstate.Provisioning
	MigStateCopying      = migstate.Copying
	MigStateRebinding    = migstate.Rebinding
	MigStateVerifying    = migstate.Verifying
	MigStateCompleted    = migstate.Completed
	MigStateCleaned      = migstate.Cleaned
	MigStateFailed       = migstate.Failed
	// MigStateRollingBack: the physical verify failed after the rebind; the
	// worker is putting the tenant back on its source bucket.
	MigStateRollingBack = migstate.RollingBack
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

// ObjectCopier performs a server-side copy of one object between physical
// locations. Slice 1 is same-backend only (the s3 router refuses cross-backend).
type ObjectCopier interface {
	CopyObject(ctx context.Context, src, dst CopyLocation) error
}

// ObjectDeleter permanently removes one physical object. Used by the
// retention-gated cleanup phase to drop the old (shared) copies.
type ObjectDeleter interface {
	DeleteObject(ctx context.Context, loc CopyLocation) error
}

// ObjectHeader reports a physical object's size (-1 / error when missing). Used
// by the verify phase to confirm each copy landed in the destination, and by
// the rollback to tell whether the target holds an object. An object the
// backend says is not there is reported as an error wrapping
// ErrObjectMissing; any other error is one the caller cannot read anything
// into.
type ObjectHeader interface {
	HeadObject(ctx context.Context, loc CopyLocation) (sizeBytes int64, err error)
}

// ErrObjectMissing reports that the backend has no object at the location.
var ErrObjectMissing = errors.New("object not in the bucket")

// ObjectIdentity reports a physical object's ETag: what the catch-up records
// of its copy, and what the verify compares the target against. ETags are
// compared only with ETags the same bucket reported for the same copy: a
// server-side copy, a stream-through and an encrypted store each make their
// own, so the target is never compared with the source. An object the backend
// says is not there is an error wrapping ErrObjectMissing.
type ObjectIdentity interface {
	ObjectETag(ctx context.Context, loc CopyLocation) (string, error)
}

// StorageMigration is one tenant's in-flight shared->dedicated copy job.
type StorageMigration struct {
	TenantID         uuid.UUID
	SourceBackendID  string
	SourceBucketName string
	TargetBackendID  string
	TargetBucketName string
	State            string
	ObjectsTotal     int64
	ObjectsCopied    int64
	CursorCollection string
	CursorKey        string
	// CleanupAfter is when the old (shared) copies may be deleted; zero until
	// the migration is 'completed'. Cleanup runs once time.Now >= CleanupAfter.
	CleanupAfter time.Time
	// Attempts counts the migration's failures; a retry keeps it, so it tells
	// one attempt from the next.
	Attempts int32
	// ReboundAt is when the rebind to the target committed (its transaction's
	// start); zero before the rebind. A rollback copies back the objects
	// written from then on.
	ReboundAt time.Time
	// RollbackCursorAt / RollbackCursorID are the rollback's resume cursor:
	// the (written at, id) of the last object copied back; zero until the
	// first batch.
	RollbackCursorAt time.Time
	RollbackCursorID uuid.UUID
	// CopyStartedAt is when the copy started (copy_started_at, migration
	// 116): an object written since may have reached the source only, so the
	// catch-up copies it before the rebind. A migration from before 116 reads
	// its creation, earlier than any copy of it.
	CopyStartedAt time.Time
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

// ObjectRef is a logical (collection, key) pair to copy, with its recorded
// size for the physical verify.
type ObjectRef struct {
	Collection string
	Key        string
	SizeBytes  int64
	// Trashed: the row is in the trash (DELETED). Its bytes stay in the
	// bucket until the lifecycle purge, so RestoreObject can bring it back,
	// and they move with the collection like a live object's. Unlike a live
	// object's they may be gone already: deleting the bytes straight from the
	// bucket trashes the row through a storage event, and such an object has
	// nothing to move.
	Trashed bool
	// BoundBackendID and BoundBucket are the bucket the object's collection
	// is bound to now; cleanup deletes a source copy only of an object read
	// from the target. Set by the list the cleanup walks.
	BoundBackendID string
	BoundBucket    string
	// WrittenAt is when the object's bytes were last written
	// (bytes_written_at); zero for a row not written since migration 111.
	WrittenAt time.Time
	// CaughtUp is what the catch-up copied of the object, nil when it copied
	// nothing. Set by the list the verify walks.
	CaughtUp *CaughtUp
}

// CaughtUp is the catch-up's copy of an object (migration 116): the write
// whose bytes it took, and the ETag the target reported for that copy — nil
// without an Identity to read one, or for a trashed object with no bytes.
type CaughtUp struct {
	WrittenAt time.Time
	ETag      *string
}

// WrittenObject is an object written since a point in time, as the rollback
// lists it: keyed by (WrittenAt, ID) for its cursor.
type WrittenObject struct {
	ObjectRef
	ID uuid.UUID
	// WrittenAt is when the object's bytes were last written — not its row,
	// which a tag or a taint changes too.
	WrittenAt time.Time
}

// MigrationRepo is the persistence seam for the migration state machine.
type MigrationRepo interface {
	ListActive(ctx context.Context, limit int) ([]StorageMigration, error)
	// BucketProvisionState returns the target bucket's provision_state
	// ("pending" | "ready" | "failed").
	BucketProvisionState(ctx context.Context, backendID, bucketName string) (string, error)
	// The writes below name the migration as it was read (MigrationRead) and
	// apply only while it still holds — the same state and the same attempt;
	// otherwise they change nothing and return ErrMigrationMoved. A worker
	// resumed after a pause past its lease thereby cannot drag back a
	// migration its successor moved on, nor, by the attempt, act on a retry
	// of the attempt it read: the state alone repeats across attempts.

	// SetCopying counts the objects to copy, records the total and moves
	// provisioning -> copying, on one transaction that holds the migration
	// row before it counts. A purge takes the row on its own transaction
	// (package purgedebt), so the count sees every purge that got there
	// first, and a later one finds 'copying' and takes its object off the
	// total. It returns the total.
	SetCopying(ctx context.Context, at MigrationRead) (int64, error)
	ListObjects(ctx context.Context, tenantID uuid.UUID, afterCollection, afterKey string, limit int) ([]ObjectRef, error)
	// AdvanceCopy only moves progress forward.
	AdvanceCopy(ctx context.Context, at MigrationRead, copied int64, cursorCollection, cursorKey string) error
	// HoldObject runs fn while o's rows are held against a delete, and
	// reports whether there were any: none — the object was purged since it
	// was listed — runs nothing. A purge of o waits for fn, and then owes
	// what fn copied (package purgedebt); one that ran first left nothing to
	// copy. With progress, it also records, on the same transaction, the copy
	// having got past o with progress.Copied objects copied.
	HoldObject(ctx context.Context, tenantID uuid.UUID, o ObjectRef, progress *CopyProgress, fn func(context.Context) error) (bool, error)
	SetState(ctx context.Context, at MigrationRead, to string) error
	// RebindTenant, in ONE transaction, rebinds every private collection of
	// the tenant to (targetBackendID, targetBucketName), flips storage_layout
	// to 'dedicated' and records the migration's rebound_at. The FK is
	// DEFERRABLE INITIALLY DEFERRED. A rebind the schema refuses is
	// ErrRebindRefused; one for a migration no longer as read is
	// ErrMigrationMoved, and changes nothing. Before any of that, under the
	// migration row's lock — which every promote holds while it runs — it
	// counts the objects ListUncaughtUp would still list: any is a
	// *CopyBehindError, and nothing changes.
	RebindTenant(ctx context.Context, at MigrationRead, targetBackendID, targetBucketName string) error
	// ListUncaughtUp lists the tenant's live and trashed private objects whose
	// bytes were written since the copy started and whose latest write the
	// catch-up has not copied, in (written at, id) order — while the migration
	// is as read; none otherwise.
	ListUncaughtUp(ctx context.Context, at MigrationRead, limit int) ([]WrittenObject, error)
	// CatchUpObject holds o's row against a delete and a promote, runs fn —
	// the copy — and records, on the same transaction, the write fn copied
	// and the ETag it returns. No row (purged since it was listed) runs
	// nothing and reports false. A migration no longer as read records
	// nothing: ErrMigrationMoved.
	CatchUpObject(ctx context.Context, at MigrationRead, o WrittenObject, fn func(context.Context) (*string, error)) (bool, error)
	// StartRollback moves verifying -> rolling_back, recording reason as the
	// migration's error and counting the failure.
	StartRollback(ctx context.Context, at MigrationRead, reason string) error
	// ListWrittenSince lists the tenant's live and trashed private objects whose
	// (bytes written at, id) is after (afterAt, afterID), in that order.
	ListWrittenSince(ctx context.Context, tenantID uuid.UUID, afterAt time.Time, afterID uuid.UUID, limit int) ([]WrittenObject, error)
	// AdvanceRollback records the rollback cursor; it only moves forward.
	AdvanceRollback(ctx context.Context, at MigrationRead, cursorAt time.Time, cursorID uuid.UUID) error
	// RebindBack is the rollback's last step, in ONE transaction under the
	// migration row's lock: provided no object was written past the cursor
	// (else ErrRollbackBehind, and nothing changes), it rebinds every private
	// collection to (sourceBackendID, sourceBucketName), flips storage_layout
	// to 'shared', points the default binding at the source bucket and moves
	// the migration rolling_back -> failed, keeping its error. All of it or
	// none: a tenant is never left half rebound. A migration no longer as read
	// is ErrMigrationMoved.
	RebindBack(ctx context.Context, at MigrationRead, cursorAt time.Time, cursorID uuid.UUID, sourceBackendID, sourceBucketName string) error
	Complete(ctx context.Context, at MigrationRead) error
	// MarkCleaned is the terminal transition after the old copies are deleted.
	MarkCleaned(ctx context.Context, at MigrationRead) error
	// CountUploadsAnchoredTo counts the tenant's PENDING uploads whose URL was
	// signed for (backendID, bucketName): their bytes may still land there
	// (objects.upload_bucket_id).
	CountUploadsAnchoredTo(ctx context.Context, tenantID uuid.UUID, backendID, bucketName string) (int64, error)
	Fail(ctx context.Context, at MigrationRead, reason string) error
}

// CopyProgress is the copy's progress to record with a held object: the
// migration as read, and the count of objects copied once it is.
type CopyProgress struct {
	At     MigrationRead
	Copied int64
}

// ErrMigrationMoved reports a transition that did not apply because the
// migration is no longer as it was read: another state, or another attempt.
var ErrMigrationMoved = errors.New("storage migration moved on since it was read")

// ErrCopyBehind reports a rebind refused because objects were written since
// the copy started that the catch-up has not copied yet: rebinding would
// serve them from a target that lacks them, or holds older bytes. The
// worker catches up first, next tick.
var ErrCopyBehind = errors.New("objects written since the copy started are not in the target yet")

// CopyBehindError is ErrCopyBehind with how many objects are behind.
type CopyBehindError struct{ Objects int64 }

func (e *CopyBehindError) Error() string {
	return fmt.Sprintf("%v: %d objects", ErrCopyBehind, e.Objects)
}

func (e *CopyBehindError) Is(target error) bool { return target == ErrCopyBehind }

// ErrRebindRefused reports a rebind the schema refused (a collection the
// bucket rules will not let move). Retrying cannot change the answer, so the
// worker fails the migration rather than retrying it every tick.
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

// The reasons a migration records, which GetTenantStorageMigration returns to
// the operator: what failed, in the product's terms. The error behind each is
// logged where it happens.
const (
	reasonRebindRefused     = "rebind refused: collection %q cannot move to the dedicated bucket"
	reasonRebindRefusedAny  = "rebind refused: a collection cannot move to the dedicated bucket"
	reasonVerifyPrefix      = "physical verify: "
	reasonVerifyUnlisted    = "the objects to verify could not be listed"
	verifyProblemUnreadable = "could not be read from the target bucket"
	verifyProblemMissing    = "missing in the target bucket"
	// verifyProblemSourceUnreadable: a trashed object missing from the target,
	// and the source could not say whether it ever had bytes to move.
	verifyProblemSourceUnreadable = "missing in the target bucket and could not be read from the source"
	verifyProblemSize             = "size mismatch: target %d != source %d"
	// verifyProblemNotCaughtUp: written after the copy started and before
	// the rebind, and the catch-up did not copy that write.
	verifyProblemNotCaughtUp = "written after the copy started and not caught up"
	// verifyProblemIdentity: the target's copy is not the one the catch-up
	// made of the object's latest write.
	verifyProblemIdentity = "content differs from the catch-up's copy: target ETag %q, copied %q"
)

// rebindReason is the recorded reason for a rebind the schema refused.
func rebindReason(err error) string {
	if rr, ok := errors.AsType[*RebindRefusedError](err); ok {
		return fmt.Sprintf(reasonRebindRefused, rr.Collection)
	}
	return reasonRebindRefusedAny
}

// verifyFailure is a physical verify's finding about one object: which, and
// what is wrong with it. The backend's error, when there is one, is its cause.
type verifyFailure struct {
	object  string
	problem string
	cause   error
}

func (v *verifyFailure) Error() string {
	if v.cause == nil {
		return v.object + " " + v.problem
	}
	return v.object + " " + v.problem + ": " + v.cause.Error()
}

func (v *verifyFailure) Unwrap() error { return v.cause }

// verifyReason is the recorded reason for a failed physical verify: the
// object and the problem, without the backend's or the store's words.
func verifyReason(err error) string {
	if vf, ok := errors.AsType[*verifyFailure](err); ok {
		return reasonVerifyPrefix + vf.object + " " + vf.problem
	}
	return reasonVerifyPrefix + reasonVerifyUnlisted
}

// ErrRollbackBehind reports a rebind back refused because objects were
// written to the target past the rollback cursor since the worker last
// listed: they are copied first, on the next tick.
var ErrRollbackBehind = errors.New("objects written since the rollback's last batch")

// StorageMigrationWorker drives active migrations to completion.
type StorageMigrationWorker struct {
	Repo    MigrationRepo
	Copier  ObjectCopier
	Deleter ObjectDeleter
	Header  ObjectHeader
	// Identity reads a copy's ETag, which the catch-up records and the verify
	// compares; nil records none and compares none.
	Identity  ObjectIdentity
	Interval  time.Duration
	CopyBatch int
	Logger    *zap.Logger
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

func (w *StorageMigrationWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 30 * time.Second
	}
	if w.CopyBatch <= 0 {
		w.CopyBatch = 100
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	return RunTicker(ctx, "storage-migration", w.Interval, w.tick)
}

func (w *StorageMigrationWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// tick advances every active migration one step, carrying on past one that
// fails, and returns the first failure. A step dropped because the migration
// moved on, or deferred behind writes made during a rollback, is not one.
func (w *StorageMigrationWorker) tick(ctx context.Context) error {
	migs, err := w.Repo.ListActive(ctx, 50)
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
			w.log().Info("migration moved on since it was listed; step dropped",
				zap.String("tenant_id", m.TenantID.String()), zap.String("state", m.State))
		} else if errors.Is(err, ErrRollbackBehind) {
			w.log().Info("objects written during the rollback; copying them before rebinding back",
				zap.String("tenant_id", m.TenantID.String()))
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
	case MigStateCopying:
		return w.stepCopying(ctx, m)
	case MigStateRebinding:
		return w.stepRebinding(ctx, m)
	case MigStateVerifying:
		return w.stepVerifying(ctx, m)
	case MigStateCompleted:
		return w.stepCompleted(ctx, m)
	case MigStateRollingBack:
		return w.stepRollingBack(ctx, m)
	default:
		return nil
	}
}

// stepProvisioning waits for the reconciler to make the target bucket ready,
// then records the object count and moves to copying.
func (w *StorageMigrationWorker) stepProvisioning(ctx context.Context, m StorageMigration) error {
	// Both same-backend (server-side CopyObject) and cross-backend
	// (stream-through GET→PUT) are supported now — the s3 router picks the path.
	state, err := w.Repo.BucketProvisionState(ctx, m.TargetBackendID, m.TargetBucketName)
	if err != nil {
		return fmt.Errorf("read target bucket state: %w", err)
	}
	switch state {
	case "ready":
		// proceed below
	case "failed":
		return w.Repo.Fail(ctx, m.Read(), "target bucket provisioning failed")
	default:
		return nil // still pending; wait for the bucket reconciler
	}
	total, err := w.Repo.SetCopying(ctx, m.Read())
	if err != nil {
		return fmt.Errorf("enter the copy phase: %w", err)
	}
	w.log().Info("migration entering copy phase",
		zap.String("tenant_id", m.TenantID.String()), zap.Int64("objects", total))
	return nil
}

// stepCopying copies one batch of objects (server-side, identical key) and
// advances the cursor. Empty batch => every object copied => rebinding.
//
// Each object is copied while its row is held, and the cursor moves past it
// on the same transaction (HoldObject): a purge of it either waits for the
// copy and then owes the target's copy too, or ran first and leaves nothing
// to copy. So no copy outlives its row, and the count stays exact — a purge
// takes an object the cursor has not reached off the total — whatever a
// crash interrupts: an object whose transaction did not commit is copied
// again, under the same key.
func (w *StorageMigrationWorker) stepCopying(ctx context.Context, m StorageMigration) error {
	objs, err := w.Repo.ListObjects(ctx, m.TenantID, m.CursorCollection, m.CursorKey, w.CopyBatch)
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	if len(objs) == 0 {
		// Safety net: never rebind on an incomplete copy. If the list is empty
		// but fewer objects were copied than counted, something hid rows (an RLS
		// GUC gap, a race) — rebinding here would orphan the uncopied objects on
		// the old bucket. Fail loudly instead; the operator retries.
		if m.ObjectsCopied < m.ObjectsTotal {
			w.log().Error("copy ended short of the count; refusing to rebind",
				zap.String("tenant_id", m.TenantID.String()),
				zap.Int64("copied", m.ObjectsCopied), zap.Int64("total", m.ObjectsTotal))
			return w.Repo.Fail(ctx, m.Read(),
				fmt.Sprintf("copy incomplete: %d of %d objects copied; not rebinding", m.ObjectsCopied, m.ObjectsTotal))
		}
		return w.Repo.SetState(ctx, m.Read(), MigStateRebinding)
	}
	copied := m.ObjectsCopied
	for _, o := range objs {
		if ctx.Err() != nil {
			return nil
		}
		held, err := w.Repo.HoldObject(ctx, m.TenantID, o, &CopyProgress{At: m.Read(), Copied: copied + 1},
			func(ctx context.Context) error { return w.copyOne(ctx, m, o) })
		if err != nil {
			// Progress up to o is recorded — the batch retries from here.
			return fmt.Errorf("copy %s/%s: %w", o.Collection, o.Key, err)
		}
		if held {
			copied++
			continue
		}
		// Purged since it was listed: its purge took it off the total.
		w.log().Info("object purged since it was listed; nothing to copy",
			zap.String("tenant_id", m.TenantID.String()),
			zap.String("collection", o.Collection), zap.String("key", o.Key))
		if err := w.Repo.AdvanceCopy(ctx, m.Read(), copied, o.Collection, o.Key); err != nil {
			return err
		}
	}
	return nil
}

// copyOne copies o's bytes from the source to the target bucket, under the
// same key. A trashed object whose bytes are already gone from the source is
// skipped: there is nothing to move, and RestoreObject refuses it wherever
// the collection is bound.
func (w *StorageMigrationWorker) copyOne(ctx context.Context, m StorageMigration, o ObjectRef) error {
	src := m.sourceLoc(o)
	if o.Trashed {
		gone, err := w.bytesGone(ctx, src)
		if err != nil {
			return fmt.Errorf("head source: %w", err)
		}
		if gone {
			w.log().Warn("trashed object has no bytes in the source; nothing to copy",
				zap.String("tenant_id", m.TenantID.String()),
				zap.String("collection", o.Collection), zap.String("key", o.Key))
			return nil
		}
	}
	return w.Copier.CopyObject(ctx, src, m.targetLoc(o))
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

// sourceLoc is o's address in the migration's source bucket.
func (m StorageMigration) sourceLoc(o ObjectRef) CopyLocation {
	return CopyLocation{BackendID: m.SourceBackendID, TenantID: m.TenantID, Bucket: m.SourceBucketName, Collection: o.Collection, Key: o.Key}
}

// targetLoc is o's address in the migration's target bucket.
func (m StorageMigration) targetLoc(o ObjectRef) CopyLocation {
	return CopyLocation{BackendID: m.TargetBackendID, TenantID: m.TenantID, Bucket: m.TargetBucketName, Collection: o.Collection, Key: o.Key}
}

// stepRebinding catches up on the writes the copy's cursor had passed, then
// atomically repoints the tenant's collections at the dedicated bucket and
// flips the layout. From here reads/writes resolve to the copy.
//
// The copy walked the objects once, while the tenant kept writing to the
// source: an object uploaded, or overwritten, behind the cursor is only in
// the source, or stale in the target — and an overwrite of the same size
// would pass a verify by size. So each tick first copies one batch of the
// objects written since the copy started whose latest write is not copied
// (catchUp); only when there are none does it rebind, and the rebind counts
// them again under the migration row's lock, which every promote holds while
// it runs: a write that landed since the last batch refuses it
// (ErrCopyBehind), and the next tick copies that first.
func (w *StorageMigrationWorker) stepRebinding(ctx context.Context, m StorageMigration) error {
	if done, err := w.catchUp(ctx, m); err != nil || !done {
		return err
	}
	err := w.Repo.RebindTenant(ctx, m.Read(), m.TargetBackendID, m.TargetBucketName)
	if behind, ok := errors.AsType[*CopyBehindError](err); ok {
		w.log().Warn("rebind refused: objects were written since the last catch-up pass; catching up first",
			zap.String("tenant_id", m.TenantID.String()), zap.Int64("objects", behind.Objects))
		return nil
	}
	if errors.Is(err, ErrRebindRefused) {
		// The transaction rolled back: nothing rebound, the layout is still
		// shared and the tenant is served from its source copies.
		w.log().Error("rebind refused by the schema; failing the migration",
			zap.String("tenant_id", m.TenantID.String()), zap.Error(err))
		return w.Repo.Fail(ctx, m.Read(), rebindReason(err))
	}
	if err != nil {
		return fmt.Errorf("rebind collections: %w", err)
	}
	return w.Repo.SetState(ctx, m.Read(), MigStateVerifying)
}

// catchUp copies one batch of the objects written since the copy started
// whose latest write the target has not seen, each under its row's hold as
// the copy holds it, and reports whether there were none. An object written
// again while it is copied is listed again: the catch-up records the write
// it copied, not that it copied the object.
func (w *StorageMigrationWorker) catchUp(ctx context.Context, m StorageMigration) (bool, error) {
	objs, err := w.Repo.ListUncaughtUp(ctx, m.Read(), w.batch())
	if err != nil {
		return false, fmt.Errorf("list objects written since the copy started: %w", err)
	}
	if len(objs) == 0 {
		return true, nil
	}
	var copied, purged int64
	for _, o := range objs {
		if ctx.Err() != nil {
			break
		}
		held, err := w.Repo.CatchUpObject(ctx, m.Read(), o, func(ctx context.Context) (*string, error) {
			return w.catchUpOne(ctx, m, o.ObjectRef)
		})
		if errors.Is(err, ErrMigrationMoved) {
			return false, err
		}
		if err != nil {
			w.log().Error("catch-up copy failed; the rebind waits for it",
				zap.String("tenant_id", m.TenantID.String()),
				zap.String("collection", o.Collection), zap.String("key", o.Key), zap.Error(err))
			return false, fmt.Errorf("catch up %s/%s: %w", o.Collection, o.Key, err)
		}
		if held {
			copied++
		} else {
			purged++
		}
	}
	w.log().Info("catch-up pass: copied objects written since the copy started",
		zap.String("tenant_id", m.TenantID.String()),
		zap.Int("listed", len(objs)), zap.Int64("copied", copied), zap.Int64("purged", purged))
	return false, nil
}

// catchUpOne copies o to the target as the copy does, and reports the ETag
// the target holds for the copy: nil without an Identity, or for a trashed
// object with no bytes to move.
func (w *StorageMigrationWorker) catchUpOne(ctx context.Context, m StorageMigration, o ObjectRef) (*string, error) {
	if err := w.copyOne(ctx, m, o); err != nil {
		return nil, err
	}
	if w.Identity == nil {
		return nil, nil
	}
	etag, err := w.Identity.ObjectETag(ctx, m.targetLoc(o))
	if errors.Is(err, ErrObjectMissing) && o.Trashed {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the copy's ETag: %w", err)
	}
	return &etag, nil
}

// stepVerifying confirms the migration is sound before serving from the
// dedicated bucket: the count matches, and — when a Header is wired — every
// object physically exists in the TARGET bucket with the recorded size. A miss
// or size mismatch rolls the migration back (the collections are already
// rebound, so completing on a bad copy would serve a broken object, and
// failing in place would leave the tenant on a bucket missing objects; the
// source copies are still there to go back to). Size against the row; and
// for an object written while the copy ran, the write time and the ETag of
// the catch-up's copy (catchUpProblem, verifyIdentity). Never the source's
// ETag against the target's: S3 ETags differ between a server-side copy and
// a stream-through (multipart) upload, so they can't be compared across
// copy methods.
func (w *StorageMigrationWorker) stepVerifying(ctx context.Context, m StorageMigration) error {
	if m.ObjectsCopied < m.ObjectsTotal {
		return fmt.Errorf("verify: copied %d < total %d", m.ObjectsCopied, m.ObjectsTotal)
	}
	if w.Header != nil {
		if err := w.verifyPhysical(ctx, m); err != nil {
			w.log().Error("physical verify failed; rolling the tenant back to its source bucket",
				zap.String("tenant_id", m.TenantID.String()), zap.Error(err))
			return w.Repo.StartRollback(ctx, m.Read(), verifyReason(err))
		}
	}
	w.log().Info("migration completed",
		zap.String("tenant_id", m.TenantID.String()), zap.Int64("objects", m.ObjectsCopied))
	return w.Repo.Complete(ctx, m.Read())
}

// verifyPhysical HEADs every object in the TARGET bucket and checks its size
// against the source's recorded size_bytes.
func (w *StorageMigrationWorker) verifyPhysical(ctx context.Context, m StorageMigration) error {
	curOK, curKey := "", ""
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		objs, err := w.Repo.ListObjects(ctx, m.TenantID, curOK, curKey, w.CopyBatch)
		if err != nil {
			return fmt.Errorf("list objects: %w", err)
		}
		if len(objs) == 0 {
			return nil
		}
		for _, o := range objs {
			if err := w.verifyObject(ctx, m, o); err != nil {
				return err
			}
			curOK, curKey = o.Collection, o.Key
		}
		if len(objs) < w.CopyBatch {
			return nil
		}
	}
}

// verifyObject HEADs o in the TARGET bucket: a *verifyFailure when it is
// missing, unreadable, or not the size the source recorded. A trashed object
// missing from the target passes only when the source has no bytes for it
// either — the copy skipped it, having nothing to move.
func (w *StorageMigrationWorker) verifyObject(ctx context.Context, m StorageMigration, o ObjectRef) error {
	object := o.Collection + "/" + o.Key
	got, err := w.Header.HeadObject(ctx, m.targetLoc(o))
	if errors.Is(err, ErrObjectMissing) {
		if purged, perr := w.purgedSinceListed(ctx, m, o); perr != nil || purged {
			return perr
		}
	}
	switch {
	case errors.Is(err, ErrObjectMissing) && o.Trashed:
		return w.verifyTrashedMissing(ctx, m, o, err)
	case errors.Is(err, ErrObjectMissing):
		return &verifyFailure{object: object, problem: verifyProblemMissing, cause: err}
	case err != nil:
		return &verifyFailure{object: object, problem: verifyProblemUnreadable, cause: err}
	case got != o.SizeBytes:
		return &verifyFailure{object: object, problem: fmt.Sprintf(verifyProblemSize, got, o.SizeBytes)}
	}
	if problem := catchUpProblem(o, m.CopyStartedAt, m.ReboundAt); problem != "" {
		return &verifyFailure{object: object, problem: problem}
	}
	return w.verifyIdentity(ctx, m, o)
}

// catchUpProblem reports why the target's copy of o may predate o's latest
// write, "" when it cannot. The copy took every object after the copy
// started, so a write before then is in it; a write at or after the rebind
// went to the target, its promote fenced against the rebind; a write
// between the two is in the target only if the catch-up copied that very
// write. A size check cannot tell: an overwrite of the same size passes it.
func catchUpProblem(o ObjectRef, copyStartedAt, reboundAt time.Time) string {
	switch {
	case o.WrittenAt.IsZero(), o.WrittenAt.Before(copyStartedAt), !o.WrittenAt.Before(reboundAt):
		return ""
	case o.CaughtUp == nil || !o.CaughtUp.WrittenAt.Equal(o.WrittenAt):
		return verifyProblemNotCaughtUp
	}
	return ""
}

// verifyIdentity compares the target's ETag for o with the one it reported
// for the catch-up's copy of o's latest write: the same bucket's ETag for the
// same copy, so a difference is a different object there. Objects the
// catch-up did not copy, or copied without an ETag, are not compared.
func (w *StorageMigrationWorker) verifyIdentity(ctx context.Context, m StorageMigration, o ObjectRef) error {
	if w.Identity == nil || o.CaughtUp == nil || o.CaughtUp.ETag == nil || !o.CaughtUp.WrittenAt.Equal(o.WrittenAt) {
		return nil
	}
	object := o.Collection + "/" + o.Key
	etag, err := w.Identity.ObjectETag(ctx, m.targetLoc(o))
	if err != nil {
		return &verifyFailure{object: object, problem: verifyProblemUnreadable, cause: err}
	}
	if etag != *o.CaughtUp.ETag {
		return &verifyFailure{object: object, problem: fmt.Sprintf(verifyProblemIdentity, etag, *o.CaughtUp.ETag)}
	}
	return nil
}

// purgedSinceListed reports whether o's row is gone: a purge since the
// verify listed it, which took the target's copy with it (package
// purgedebt). That is no fault of the copy.
func (w *StorageMigrationWorker) purgedSinceListed(ctx context.Context, m StorageMigration, o ObjectRef) (bool, error) {
	held, err := w.Repo.HoldObject(ctx, m.TenantID, o, nil, func(context.Context) error { return nil })
	if err != nil {
		return false, &verifyFailure{object: o.Collection + "/" + o.Key, problem: verifyProblemUnreadable, cause: err}
	}
	if !held {
		w.log().Info("verify: object purged since it was listed; nothing to verify",
			zap.String("tenant_id", m.TenantID.String()),
			zap.String("collection", o.Collection), zap.String("key", o.Key))
	}
	return !held, nil
}

// verifyTrashedMissing decides a trashed object the target has no bytes for:
// sound when the source has none either, a *verifyFailure when the source
// still holds bytes the copy should have moved, or cannot say.
func (w *StorageMigrationWorker) verifyTrashedMissing(ctx context.Context, m StorageMigration, o ObjectRef, targetErr error) error {
	object := o.Collection + "/" + o.Key
	gone, err := w.bytesGone(ctx, m.sourceLoc(o))
	switch {
	case err != nil:
		return &verifyFailure{object: object, problem: verifyProblemSourceUnreadable, cause: err}
	case !gone:
		return &verifyFailure{object: object, problem: verifyProblemMissing, cause: targetErr}
	}
	w.log().Info("verify: trashed object has no bytes in either bucket; nothing to verify",
		zap.String("tenant_id", m.TenantID.String()),
		zap.String("collection", o.Collection), zap.String("key", o.Key))
	return nil
}

// stepCompleted runs the retention-gated cleanup (slice 2): once the window has
// elapsed, delete the old copies from the SOURCE bucket, then mark cleaned. The
// object rows are unchanged by the migration, so we keyset-scan them and delete
// each physical blob at its source location; the loop terminates because the
// cursor advances past the stable object list. The list is the copy's — live
// and trashed objects alike — so a trashed object's old copy goes too, rather
// than staying in the source once the purge deletes the one in the target.
func (w *StorageMigrationWorker) stepCompleted(ctx context.Context, m StorageMigration) error {
	if m.CleanupAfter.IsZero() || w.now().Before(m.CleanupAfter) {
		return nil // still inside the retention window
	}
	if w.Deleter == nil {
		return nil // cleanup disabled
	}
	if waiting, err := w.uploadsAnchoredToSource(ctx, m); err != nil || waiting {
		return err
	}
	if done, err := w.cleanSource(ctx, m); err != nil || !done {
		return err
	}
	w.log().Info("migration source cleaned",
		zap.String("tenant_id", m.TenantID.String()),
		zap.String("source", m.SourceBackendID+"/"+m.SourceBucketName))
	return w.Repo.MarkCleaned(ctx, m.Read())
}

// uploadsAnchoredToSource reports whether cleanup must wait: an upload whose
// URL was signed for the source bucket before the rebind is still PENDING.
// Its bytes land in the source, and CompleteObject or the reconciler copy
// them into the target only when they settle it — from the source copy, which
// cleanup would delete. Once promoted, the object is listed like any other
// and its source copy goes with the rest; discarded, it left none. The wait
// is bounded by the uploads' presign deadline and the reconciler's grace.
func (w *StorageMigrationWorker) uploadsAnchoredToSource(ctx context.Context, m StorageMigration) (bool, error) {
	n, err := w.Repo.CountUploadsAnchoredTo(ctx, m.TenantID, m.SourceBackendID, m.SourceBucketName)
	if err != nil {
		return false, fmt.Errorf("count uploads anchored to the source: %w", err)
	}
	if n > 0 {
		w.log().Info("cleanup waits for uploads whose URL names the source bucket",
			zap.String("tenant_id", m.TenantID.String()),
			zap.String("source", m.SourceBackendID+"/"+m.SourceBucketName), zap.Int64("uploads", n))
		return true, nil
	}
	return false, nil
}

// cleanSource deletes every listed object's copy from the source bucket that
// the target holds too (cleanOne), and reports whether it got through them
// all: a cancelled context stops it short with nothing to report, and the
// next tick starts over. A source copy that may be an object's only one is
// kept, and the migration is not cleaned while one is: ErrCleanupKept.
func (w *StorageMigrationWorker) cleanSource(ctx context.Context, m StorageMigration) (bool, error) {
	curOK, curKey, batch := "", "", w.batch()
	var tally cleanTally
	for {
		if ctx.Err() != nil {
			return false, nil
		}
		objs, err := w.Repo.ListObjects(ctx, m.TenantID, curOK, curKey, batch)
		if err != nil {
			return false, fmt.Errorf("cleanup list objects: %w", err)
		}
		for _, o := range objs {
			outcome, err := w.cleanOne(ctx, m, o)
			if err != nil {
				return false, fmt.Errorf("cleanup %s/%s: %w", o.Collection, o.Key, err)
			}
			tally[outcome]++
			curOK, curKey = o.Collection, o.Key
		}
		// A short page is the last: an empty one included.
		if len(objs) < batch {
			break
		}
	}
	w.log().Info("migration cleanup pass",
		zap.String("tenant_id", m.TenantID.String()),
		zap.Int64("deleted", tally[cleanDeleted]), zap.Int64("carried", tally[cleanCarried]),
		zap.Int64("bound_elsewhere", tally[cleanBoundElsewhere]), zap.Int64("kept", tally[cleanKept]))
	if tally[cleanKept] > 0 {
		return false, fmt.Errorf("%w: %d objects", ErrCleanupKept, tally[cleanKept])
	}
	return true, nil
}

// ErrCleanupKept is a cleanup that kept source copies the target does not
// hold as their rows record them: deleting them could delete an object's
// only copy. The migration stays completed, and the next tick looks again.
var ErrCleanupKept = errors.New("source copies kept: the target does not hold them as recorded")

// cleanOutcome is what cleanup did with one object's source copy.
type cleanOutcome int

const (
	// cleanDeleted: the target holds the object; its source copy is gone.
	cleanDeleted cleanOutcome = iota
	// cleanCarried: the target lacked the object as recorded and the source
	// held it; the source copy was copied to the target, then deleted.
	cleanCarried
	// cleanBoundElsewhere: the object's collection is not bound to the
	// target, so its source copy may be what it is read from; kept.
	cleanBoundElsewhere
	// cleanKept: neither bucket holds the object as its row records it;
	// the source copy is kept.
	cleanKept
	// cleanOutcomes is the number of outcomes, for a tally.
	cleanOutcomes
)

// cleanTally counts a cleanup's outcomes.
type cleanTally [cleanOutcomes]int64

// cleanOne deletes o's copy from the source, once the target is known to hold
// the object — the size its row records — so cleanup never deletes the only
// copy of a live object, whatever wrote it after the copy passed:
//
//   - a collection not bound to the target (one created while the rebind ran
//     stays bound where the default binding pointed) is read from wherever
//     it is bound, the source maybe; its copy is kept;
//   - an object the target lacks, or holds at another size, while the source
//     holds it as recorded, is carried: copied to the target, checked, and
//     only then deleted from the source;
//   - one neither bucket holds as recorded is kept and logged: deleting the
//     source copy could lose the newer bytes, and only an operator can tell.
//
// A trashed object whose bytes are gone from both buckets has nothing to
// delete. Without a Header nothing can be checked, and the source copy is
// deleted as before.
func (w *StorageMigrationWorker) cleanOne(ctx context.Context, m StorageMigration, o ObjectRef) (cleanOutcome, error) {
	log := w.log().With(zap.String("tenant_id", m.TenantID.String()),
		zap.String("collection", o.Collection), zap.String("key", o.Key))
	if o.BoundBackendID != m.TargetBackendID || o.BoundBucket != m.TargetBucketName {
		log.Warn("cleanup: the object's collection is not bound to the migration's target; keeping its source copy",
			zap.String("bound_bucket", o.BoundBackendID+"/"+o.BoundBucket))
		return cleanBoundElsewhere, nil
	}
	src, dst := m.sourceLoc(o), m.targetLoc(o)
	if w.Header == nil {
		return cleanDeleted, w.Deleter.DeleteObject(ctx, src)
	}
	targetSize, inTarget, err := w.headSize(ctx, dst)
	if err != nil {
		return 0, fmt.Errorf("head target: %w", err)
	}
	if inTarget && targetSize == o.SizeBytes {
		return cleanDeleted, w.Deleter.DeleteObject(ctx, src)
	}
	sourceSize, inSource, err := w.headSize(ctx, src)
	switch {
	case err != nil:
		return 0, fmt.Errorf("head source: %w", err)
	case !inSource && !inTarget:
		log.Info("cleanup: the object has no bytes in either bucket; nothing to delete")
		return cleanDeleted, nil
	case !inSource || sourceSize != o.SizeBytes:
		log.Error("cleanup: neither bucket holds the object at its recorded size; keeping the source copy",
			zap.Int64("recorded_size", o.SizeBytes), zap.Int64("target_size", targetSize), zap.Int64("source_size", sourceSize))
		return cleanKept, nil
	}
	return w.carry(ctx, log, m, o, src, dst)
}

// carry copies o from the source to the target, where cleanup found it
// missing or at another size, and deletes the source copy once the target
// holds it at its recorded size. A copy that does not land keeps the source.
func (w *StorageMigrationWorker) carry(ctx context.Context, log *zap.Logger, m StorageMigration, o ObjectRef, src, dst CopyLocation) (cleanOutcome, error) {
	if err := w.Copier.CopyObject(ctx, src, dst); err != nil {
		log.Error("cleanup: carrying the source copy to the target failed; keeping it", zap.Error(err))
		return 0, fmt.Errorf("carry to the target: %w", err)
	}
	size, ok, err := w.headSize(ctx, dst)
	if err != nil {
		return 0, fmt.Errorf("head target after the carry: %w", err)
	}
	if !ok || size != o.SizeBytes {
		log.Error("cleanup: the carried copy is not the recorded size; keeping the source copy",
			zap.Int64("recorded_size", o.SizeBytes), zap.Int64("target_size", size))
		return cleanKept, nil
	}
	if err := w.Deleter.DeleteObject(ctx, src); err != nil {
		return 0, err
	}
	log.Warn("cleanup: the target lacked the object; carried its source copy there before deleting it",
		zap.String("source", m.SourceBackendID+"/"+m.SourceBucketName))
	return cleanCarried, nil
}

// headSize HEADs loc: its size, and whether the backend holds it at all.
func (w *StorageMigrationWorker) headSize(ctx context.Context, loc CopyLocation) (int64, bool, error) {
	size, err := w.Header.HeadObject(ctx, loc)
	if errors.Is(err, ErrObjectMissing) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

// stepRollingBack puts a tenant whose physical verify failed back on its
// source bucket. The tenant has served from the target since the rebind, so
// what it wrote since then is only there; everything older is still in the
// source, which cleanup never touched (it runs only from 'completed', and a
// rolled-back migration never gets there: the source copies are the live
// data again).
//
//  1. Copy back, one batch per tick, every live or trashed private object
//     whose bytes were written at or after rebound_at, resumable by the
//     (written at, id) cursor. Bytes, not the row: every write that puts
//     bytes under an object's key is a promote — a new upload's first, a
//     storage-event overwrite's later one — and the promote records it
//     (objects.bytes_written_at). Anything else that changes a row after the
//     rebind — a tag, a taint, a lock, a trash move, a restore from the trash
//     or of a version, which repoint a row or a pointer only — leaves the
//     bytes where they were. Such an object's source copy is the original
//     and its target copy may be the broken one the verify failed on, so it
//     is NOT copied, as no object older than the rebind is. rebound_at is
//     the rebind transaction's start, no later than any write that could see
//     the rebind. Same-key copies, so redoing one is harmless.
//  2. With nothing left past the cursor, RebindBack: all or nothing, and
//     refused if anything was written past the cursor meanwhile (that is
//     copied first, next tick). The tenant is shared again and the
//     migration 'failed' — retryable as any failure before a rebind.
//
// Trashed objects are copied back like live ones: an object uploaded after
// the rebind and then trashed has bytes only in the target, and RestoreObject
// on the source would find none. Copying it back makes nothing visible — an
// object row's state is what does that, not the bytes in a bucket — so it
// stays in the trash, restorable. A row purged since has no row to list and
// is not copied.
//
// Any error leaves the migration in rolling_back for the next tick: the
// tenant keeps serving from the target meanwhile, never from a half-rebound
// mix.
func (w *StorageMigrationWorker) stepRollingBack(ctx context.Context, m StorageMigration) error {
	afterAt, afterID := m.ReboundAt, uuid.Nil()
	if !m.RollbackCursorAt.IsZero() {
		afterAt, afterID = m.RollbackCursorAt, m.RollbackCursorID
	}
	objs, err := w.Repo.ListWrittenSince(ctx, m.TenantID, afterAt, afterID, w.batch())
	if err != nil {
		return fmt.Errorf("rollback list objects: %w", err)
	}
	if len(objs) == 0 {
		return w.rebindBack(ctx, m, afterAt, afterID)
	}
	// Claim the batch: a guarded write of the cursor as it stands, so a
	// worker resumed past its lease finds out before it copies, not after.
	// Once the rollback has finished and the tenant is shared again, objects
	// written since go to the source; a stale batch copying the target over
	// them would put old bytes back.
	if err := w.Repo.AdvanceRollback(ctx, m.Read(), afterAt, afterID); err != nil {
		return err
	}
	return w.copyBackBatch(ctx, m, objs, afterAt, afterID)
}

// copyBackBatch copies a claimed batch back to the source in cursor order,
// starting past (afterAt, afterID), the cursor the batch was claimed at, and
// records the cursor past the last object copied — on a failure too, so the
// next tick resumes there. Recording the claimed cursor again, when nothing
// was copied, changes nothing: the cursor only moves forward.
func (w *StorageMigrationWorker) copyBackBatch(ctx context.Context, m StorageMigration, objs []WrittenObject, afterAt time.Time, afterID uuid.UUID) error {
	curAt, curID := afterAt, afterID
	for _, o := range objs {
		if ctx.Err() != nil {
			break
		}
		if err := w.copyBackOne(ctx, m, o.ObjectRef); err != nil {
			// Persist progress so far, then bubble — the batch retries from here.
			_ = w.Repo.AdvanceRollback(ctx, m.Read(), curAt, curID)
			return fmt.Errorf("rollback copy %s/%s: %w", o.Collection, o.Key, err)
		}
		curAt, curID = o.WrittenAt, o.ID
	}
	return w.Repo.AdvanceRollback(ctx, m.Read(), curAt, curID)
}

// rebindBack is the rollback's last step, once nothing is left past the
// cursor: the tenant goes back to its source bucket (RebindBack says how).
func (w *StorageMigrationWorker) rebindBack(ctx context.Context, m StorageMigration, afterAt time.Time, afterID uuid.UUID) error {
	if err := w.Repo.RebindBack(ctx, m.Read(), afterAt, afterID, m.SourceBackendID, m.SourceBucketName); err != nil {
		return err
	}
	w.log().Warn("migration rolled back; tenant is on its source bucket again",
		zap.String("tenant_id", m.TenantID.String()),
		zap.String("source", m.SourceBackendID+"/"+m.SourceBucketName))
	return nil
}

// copyBackOne copies o from the target back to the source, when the target
// holds it as its row records it (targetHolds). The row is held for the
// copy, as the copy phase holds it: a purge either waits and then owes the
// source copy too, or ran first and leaves nothing to copy back.
func (w *StorageMigrationWorker) copyBackOne(ctx context.Context, m StorageMigration, o ObjectRef) error {
	src := m.targetLoc(o)
	held, err := w.Repo.HoldObject(ctx, m.TenantID, o, nil, func(ctx context.Context) error {
		copyIt, err := w.targetHolds(ctx, src, o.SizeBytes)
		if err != nil || !copyIt {
			return err
		}
		return w.Copier.CopyObject(ctx, src, m.sourceLoc(o))
	})
	if err == nil && !held {
		w.log().Info("rollback: object purged since it was listed; nothing to copy back",
			zap.String("tenant_id", m.TenantID.String()),
			zap.String("collection", o.Collection), zap.String("key", o.Key))
	}
	return err
}

// targetHolds reports whether the target bucket holds the object as its row
// records it, so the rollback copies only bytes that are the object's. Not
// there: the write never reached the target (an upload that resolved its
// bucket just before the rebind put its bytes in the source and was promoted
// after it) — the source is all there is. A different size: the target copy
// is not the recorded object (the broken copy the verify failed on, listed
// when its row predates bytes_written_at and falls back to updated_at);
// copying it would overwrite the good source copy. Without a Header the
// rollback copies everything listed.
func (w *StorageMigrationWorker) targetHolds(ctx context.Context, loc CopyLocation, size int64) (bool, error) {
	if w.Header == nil {
		return true, nil
	}
	got, err := w.Header.HeadObject(ctx, loc)
	switch {
	case errors.Is(err, ErrObjectMissing):
		w.log().Warn("rollback: object written since the rebind is not in the target; leaving the source as it is",
			zap.String("tenant_id", loc.TenantID.String()), zap.String("collection", loc.Collection), zap.String("key", loc.Key))
		return false, nil
	case err != nil:
		return false, fmt.Errorf("head target: %w", err)
	case got != size:
		w.log().Warn("rollback: target copy does not match the object's recorded size; leaving the source as it is",
			zap.String("tenant_id", loc.TenantID.String()), zap.String("collection", loc.Collection), zap.String("key", loc.Key),
			zap.Int64("target_size", got), zap.Int64("recorded_size", size))
		return false, nil
	}
	return true, nil
}

func (w *StorageMigrationWorker) batch() int {
	if w.CopyBatch > 0 {
		return w.CopyBatch
	}
	return 100
}

func (w *StorageMigrationWorker) log() *zap.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return zap.NewNop()
}

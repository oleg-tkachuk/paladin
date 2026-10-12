package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ADR-0015 Phase 3 (slice 1): the shared->dedicated storage migration copy job.
// A tenant switched to the `dedicated` layout keeps serving from its shared
// bucket until this worker copies every object into the tenant's own bucket
// (same key, server-side CopyObject), rebinds the collections in one
// transaction, and flips the layout. Crash-safe + resumable via the
// tenant_storage_migrations row (the schema baseline (001_initial_schema.sql)).
//
// State machine (driven here):
//   provisioning -> copying -> rebinding -> verifying -> completed
// Terminal failures (target-bucket provisioning failed, a rebind the schema
// refuses, a failed physical verify) set `failed`; transient errors (a DB
// blip, one failed CopyObject) are retried on the next tick without abandoning
// the migration.
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

// Migration states. tenant_storage_migrations.state (`001_initial_schema.sql`)
// is plain text with no CHECK, so this list is the only definition of the set.
const (
	MigStateProvisioning = "provisioning"
	MigStateCopying      = "copying"
	MigStateRebinding    = "rebinding"
	MigStateVerifying    = "verifying"
	MigStateCompleted    = "completed"
	MigStateCleaned      = "cleaned"
	MigStateFailed       = "failed"
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
// by the verify phase to confirm each copy landed in the destination.
type ObjectHeader interface {
	HeadObject(ctx context.Context, loc CopyLocation) (sizeBytes int64, err error)
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
}

// MigrationRepo is the persistence seam for the migration state machine.
type MigrationRepo interface {
	ListActive(ctx context.Context, limit int) ([]StorageMigration, error)
	// BucketProvisionState returns the target bucket's provision_state
	// ("pending" | "ready" | "failed").
	BucketProvisionState(ctx context.Context, backendID, bucketName string) (string, error)
	CountObjects(ctx context.Context, tenantID uuid.UUID) (int64, error)
	// The writes below name the migration as it was read (MigrationRead) and
	// apply only while it still holds — the same state and the same attempt;
	// otherwise they change nothing and return ErrMigrationMoved. A worker
	// resumed after a pause past its lease thereby cannot drag back a
	// migration its successor moved on, nor, by the attempt, act on a retry
	// of the attempt it read: the state alone repeats across attempts.

	// SetCopying records the total and moves provisioning -> copying.
	SetCopying(ctx context.Context, at MigrationRead, total int64) error
	ListObjects(ctx context.Context, tenantID uuid.UUID, afterCollection, afterKey string, limit int) ([]ObjectRef, error)
	// AdvanceCopy only moves progress forward.
	AdvanceCopy(ctx context.Context, at MigrationRead, copied int64, cursorCollection, cursorKey string) error
	SetState(ctx context.Context, at MigrationRead, to string) error
	// RebindTenant, in ONE transaction, rebinds every private collection of
	// the tenant to (targetBackendID, targetBucketName) and flips
	// storage_layout to 'dedicated'. The FK is DEFERRABLE INITIALLY DEFERRED.
	// A rebind the schema refuses is ErrRebindRefused; one for a migration no
	// longer as read is ErrMigrationMoved, and changes nothing.
	RebindTenant(ctx context.Context, at MigrationRead, targetBackendID, targetBucketName string) error
	Complete(ctx context.Context, at MigrationRead) error
	// MarkCleaned is the terminal transition after the old copies are deleted.
	MarkCleaned(ctx context.Context, at MigrationRead) error
	Fail(ctx context.Context, at MigrationRead, reason string) error
}

// ErrMigrationMoved reports a transition that did not apply because the
// migration is no longer as it was read: another state, or another attempt.
var ErrMigrationMoved = errors.New("storage migration moved on since it was read")

// ErrRebindRefused reports a rebind the schema refused (a collection the
// bucket rules will not let move). Retrying cannot change the answer, so the
// worker fails the migration rather than retrying it every tick.
var ErrRebindRefused = errors.New("schema refused the rebind")

// StorageMigrationWorker drives active migrations to completion.
type StorageMigrationWorker struct {
	Repo      MigrationRepo
	Copier    ObjectCopier
	Deleter   ObjectDeleter
	Header    ObjectHeader
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
	return RunTicker(ctx, "storage-migration", w.Interval, func(ctx context.Context) error {
		w.tick(ctx)
		return nil
	})
}

func (w *StorageMigrationWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *StorageMigrationWorker) tick(ctx context.Context) {
	migs, err := w.Repo.ListActive(ctx, 50)
	if err != nil {
		w.log().Warn("list active migrations failed", zap.Error(err))
		return
	}
	for _, m := range migs {
		if ctx.Err() != nil {
			return
		}
		// A step error is transient — log and retry on the next tick. Terminal
		// failures set state='failed' inside the step and return nil.
		if err := w.advance(ctx, m); errors.Is(err, ErrMigrationMoved) {
			w.log().Info("migration moved on since it was listed; step dropped",
				zap.String("tenant_id", m.TenantID.String()), zap.String("state", m.State))
		} else if err != nil {
			w.log().Warn("migration step failed; will retry",
				zap.String("tenant_id", m.TenantID.String()),
				zap.String("state", m.State), zap.Error(err))
		}
	}
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
	total, err := w.Repo.CountObjects(ctx, m.TenantID)
	if err != nil {
		return fmt.Errorf("count objects: %w", err)
	}
	w.log().Info("migration entering copy phase",
		zap.String("tenant_id", m.TenantID.String()), zap.Int64("objects", total))
	return w.Repo.SetCopying(ctx, m.Read(), total)
}

// stepCopying copies one batch of objects (server-side, identical key) and
// advances the cursor. Empty batch => every object copied => rebinding.
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
	curOK, curKey := m.CursorCollection, m.CursorKey
	for _, o := range objs {
		if ctx.Err() != nil {
			break
		}
		src := CopyLocation{BackendID: m.SourceBackendID, TenantID: m.TenantID, Bucket: m.SourceBucketName, Collection: o.Collection, Key: o.Key}
		dst := CopyLocation{BackendID: m.TargetBackendID, TenantID: m.TenantID, Bucket: m.TargetBucketName, Collection: o.Collection, Key: o.Key}
		if err := w.Copier.CopyObject(ctx, src, dst); err != nil {
			// Persist progress so far, then bubble — the batch retries from here.
			_ = w.Repo.AdvanceCopy(ctx, m.Read(), copied, curOK, curKey)
			return fmt.Errorf("copy %s/%s: %w", o.Collection, o.Key, err)
		}
		copied++
		curOK, curKey = o.Collection, o.Key
	}
	return w.Repo.AdvanceCopy(ctx, m.Read(), copied, curOK, curKey)
}

// stepRebinding atomically repoints the tenant's collections at the dedicated
// bucket and flips the layout. From here reads/writes resolve to the copy.
func (w *StorageMigrationWorker) stepRebinding(ctx context.Context, m StorageMigration) error {
	if err := w.Repo.RebindTenant(ctx, m.Read(), m.TargetBackendID, m.TargetBucketName); errors.Is(err, ErrRebindRefused) {
		// The transaction rolled back: nothing rebound, the layout is still
		// shared and the tenant is served from its source copies.
		w.log().Error("rebind refused by the schema; failing the migration",
			zap.String("tenant_id", m.TenantID.String()), zap.Error(err))
		return w.Repo.Fail(ctx, m.Read(), "rebind: "+err.Error())
	} else if err != nil {
		return fmt.Errorf("rebind collections: %w", err)
	}
	return w.Repo.SetState(ctx, m.Read(), MigStateVerifying)
}

// stepVerifying confirms the migration is sound before serving from the
// dedicated bucket: the count matches, and — when a Header is wired — every
// object physically exists in the TARGET bucket with the recorded size. A miss
// or size mismatch fails the migration (the collections are already rebound, so
// completing on a bad copy would serve a broken object; the source copies are
// still present for a repair). Size, not checksum: S3 ETags differ between a
// server-side copy and a stream-through (multipart) upload, so they can't be
// compared across copy methods.
func (w *StorageMigrationWorker) stepVerifying(ctx context.Context, m StorageMigration) error {
	if m.ObjectsCopied < m.ObjectsTotal {
		return fmt.Errorf("verify: copied %d < total %d", m.ObjectsCopied, m.ObjectsTotal)
	}
	if w.Header != nil {
		if err := w.verifyPhysical(ctx, m); err != nil {
			w.log().Error("physical verify failed; not completing",
				zap.String("tenant_id", m.TenantID.String()), zap.Error(err))
			return w.Repo.Fail(ctx, m.Read(), "physical verify: "+err.Error())
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
			dst := CopyLocation{BackendID: m.TargetBackendID, TenantID: m.TenantID, Bucket: m.TargetBucketName, Collection: o.Collection, Key: o.Key}
			got, err := w.Header.HeadObject(ctx, dst)
			if err != nil {
				return fmt.Errorf("%s/%s missing in target: %w", o.Collection, o.Key, err)
			}
			if got != o.SizeBytes {
				return fmt.Errorf("%s/%s size mismatch: target %d != source %d", o.Collection, o.Key, got, o.SizeBytes)
			}
			curOK, curKey = o.Collection, o.Key
		}
		if len(objs) < w.CopyBatch {
			return nil
		}
	}
}

// stepCompleted runs the retention-gated cleanup (slice 2): once the window has
// elapsed, delete the old copies from the SOURCE bucket, then mark cleaned. The
// object rows are unchanged by the migration, so we keyset-scan them and delete
// each physical blob at its source location; the loop terminates because the
// cursor advances past the stable object list.
func (w *StorageMigrationWorker) stepCompleted(ctx context.Context, m StorageMigration) error {
	if m.CleanupAfter.IsZero() || w.now().Before(m.CleanupAfter) {
		return nil // still inside the retention window
	}
	if w.Deleter == nil {
		return nil // cleanup disabled
	}
	curOK, curKey := "", ""
	for {
		if ctx.Err() != nil {
			return nil
		}
		objs, err := w.Repo.ListObjects(ctx, m.TenantID, curOK, curKey, w.CopyBatch)
		if err != nil {
			return fmt.Errorf("cleanup list objects: %w", err)
		}
		if len(objs) == 0 {
			break
		}
		for _, o := range objs {
			loc := CopyLocation{BackendID: m.SourceBackendID, TenantID: m.TenantID, Bucket: m.SourceBucketName, Collection: o.Collection, Key: o.Key}
			if err := w.Deleter.DeleteObject(ctx, loc); err != nil {
				return fmt.Errorf("cleanup delete %s/%s: %w", o.Collection, o.Key, err)
			}
			curOK, curKey = o.Collection, o.Key
		}
		if len(objs) < w.CopyBatch {
			break
		}
	}
	w.log().Info("migration source cleaned",
		zap.String("tenant_id", m.TenantID.String()),
		zap.String("source", m.SourceBackendID+"/"+m.SourceBucketName))
	return w.Repo.MarkCleaned(ctx, m.Read())
}

func (w *StorageMigrationWorker) log() *zap.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return zap.NewNop()
}

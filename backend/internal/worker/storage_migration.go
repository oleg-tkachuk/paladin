package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ADR-0011 Phase 3 (slice 1): the shared->dedicated storage migration copy job.
// A tenant switched to the `dedicated` layout keeps serving from its shared
// bucket until this worker copies every object into the tenant's own bucket
// (same key, server-side CopyObject), rebinds the object_keys in one
// transaction, and flips the layout. Crash-safe + resumable via the
// tenant_storage_migrations row (migration 056).
//
// State machine (driven here):
//   provisioning -> copying -> rebinding -> verifying -> completed
// Terminal failures (cross-backend, target-bucket provisioning failed) set
// `failed`; transient errors (a DB blip, one failed CopyObject) are retried on
// the next tick without abandoning the migration.

// Migration states (mirror the CHECK constraint in migration 056).
const (
	MigStateProvisioning = "provisioning"
	MigStateCopying      = "copying"
	MigStateRebinding    = "rebinding"
	MigStateVerifying    = "verifying"
	MigStateCompleted    = "completed"
	MigStateFailed       = "failed"
)

// CopyLocation is a physical object address for a server-side copy. Worker-local
// (not internal/api/v1/object.Location) because that package imports worker —
// importing it back would be a cycle. build_jobs adapts the router to this.
type CopyLocation struct {
	BackendID string
	TenantID  uuid.UUID
	Bucket    string
	ObjectKey string
	Key       string
}

// ObjectCopier performs a server-side copy of one object between physical
// locations. Slice 1 is same-backend only (the s3 router refuses cross-backend).
type ObjectCopier interface {
	CopyObject(ctx context.Context, src, dst CopyLocation) error
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
	CursorObjectKey  string
	CursorKey        string
}

// ObjectRef is a logical (object_key, key) pair to copy.
type ObjectRef struct {
	ObjectKey string
	Key       string
}

// MigrationRepo is the persistence seam for the migration state machine.
type MigrationRepo interface {
	ListActive(ctx context.Context, limit int) ([]StorageMigration, error)
	// BucketProvisionState returns the target bucket's provision_state
	// ("pending" | "ready" | "failed").
	BucketProvisionState(ctx context.Context, backendID, bucketName string) (string, error)
	CountObjects(ctx context.Context, tenantID uuid.UUID) (int64, error)
	// SetCopying records the total and moves provisioning -> copying.
	SetCopying(ctx context.Context, tenantID uuid.UUID, total int64) error
	ListObjects(ctx context.Context, tenantID uuid.UUID, afterObjectKey, afterKey string, limit int) ([]ObjectRef, error)
	AdvanceCopy(ctx context.Context, tenantID uuid.UUID, copied int64, cursorObjectKey, cursorKey string) error
	SetState(ctx context.Context, tenantID uuid.UUID, state string) error
	// RebindTenant, in ONE transaction, rebinds every object_key of the tenant
	// to (targetBackendID, targetBucketName) and flips storage_layout to
	// 'dedicated'. The FK is DEFERRABLE INITIALLY DEFERRED.
	RebindTenant(ctx context.Context, tenantID uuid.UUID, targetBackendID, targetBucketName string) error
	Complete(ctx context.Context, tenantID uuid.UUID) error
	Fail(ctx context.Context, tenantID uuid.UUID, reason string) error
}

// StorageMigrationWorker drives active migrations to completion.
type StorageMigrationWorker struct {
	Repo      MigrationRepo
	Copier    ObjectCopier
	Interval  time.Duration
	CopyBatch int
	Logger    *zap.Logger
}

func (w *StorageMigrationWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 30 * time.Second
	}
	if w.CopyBatch <= 0 {
		w.CopyBatch = 100
	}
	return RunTicker(ctx, "storage-migration", w.Interval, func(ctx context.Context) error {
		w.tick(ctx)
		return nil
	})
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
		if err := w.advance(ctx, m); err != nil {
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
	default:
		return nil
	}
}

// stepProvisioning waits for the reconciler to make the target bucket ready,
// then records the object count and moves to copying.
func (w *StorageMigrationWorker) stepProvisioning(ctx context.Context, m StorageMigration) error {
	if m.SourceBackendID != m.TargetBackendID {
		// Cross-backend needs a stream-through GET+PUT (later slice) — terminal here.
		w.log().Error("cross-backend migration not supported; failing",
			zap.String("tenant_id", m.TenantID.String()),
			zap.String("src_backend", m.SourceBackendID), zap.String("dst_backend", m.TargetBackendID))
		return w.Repo.Fail(ctx, m.TenantID,
			fmt.Sprintf("cross-backend migration not supported yet (src %q, dst %q)", m.SourceBackendID, m.TargetBackendID))
	}
	state, err := w.Repo.BucketProvisionState(ctx, m.TargetBackendID, m.TargetBucketName)
	if err != nil {
		return fmt.Errorf("read target bucket state: %w", err)
	}
	switch state {
	case "ready":
		// proceed below
	case "failed":
		return w.Repo.Fail(ctx, m.TenantID, "target bucket provisioning failed")
	default:
		return nil // still pending; wait for the bucket reconciler
	}
	total, err := w.Repo.CountObjects(ctx, m.TenantID)
	if err != nil {
		return fmt.Errorf("count objects: %w", err)
	}
	w.log().Info("migration entering copy phase",
		zap.String("tenant_id", m.TenantID.String()), zap.Int64("objects", total))
	return w.Repo.SetCopying(ctx, m.TenantID, total)
}

// stepCopying copies one batch of objects (server-side, identical key) and
// advances the cursor. Empty batch => every object copied => rebinding.
func (w *StorageMigrationWorker) stepCopying(ctx context.Context, m StorageMigration) error {
	objs, err := w.Repo.ListObjects(ctx, m.TenantID, m.CursorObjectKey, m.CursorKey, w.CopyBatch)
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	if len(objs) == 0 {
		return w.Repo.SetState(ctx, m.TenantID, MigStateRebinding)
	}
	copied := m.ObjectsCopied
	curOK, curKey := m.CursorObjectKey, m.CursorKey
	for _, o := range objs {
		if ctx.Err() != nil {
			break
		}
		src := CopyLocation{BackendID: m.SourceBackendID, TenantID: m.TenantID, Bucket: m.SourceBucketName, ObjectKey: o.ObjectKey, Key: o.Key}
		dst := CopyLocation{BackendID: m.TargetBackendID, TenantID: m.TenantID, Bucket: m.TargetBucketName, ObjectKey: o.ObjectKey, Key: o.Key}
		if err := w.Copier.CopyObject(ctx, src, dst); err != nil {
			// Persist progress so far, then bubble — the batch retries from here.
			_ = w.Repo.AdvanceCopy(ctx, m.TenantID, copied, curOK, curKey)
			return fmt.Errorf("copy %s/%s: %w", o.ObjectKey, o.Key, err)
		}
		copied++
		curOK, curKey = o.ObjectKey, o.Key
	}
	return w.Repo.AdvanceCopy(ctx, m.TenantID, copied, curOK, curKey)
}

// stepRebinding atomically repoints the tenant's object_keys at the dedicated
// bucket and flips the layout. From here reads/writes resolve to the copy.
func (w *StorageMigrationWorker) stepRebinding(ctx context.Context, m StorageMigration) error {
	if err := w.Repo.RebindTenant(ctx, m.TenantID, m.TargetBackendID, m.TargetBucketName); err != nil {
		return fmt.Errorf("rebind object_keys: %w", err)
	}
	return w.Repo.SetState(ctx, m.TenantID, MigStateVerifying)
}

// stepVerifying is a count-based check for slice 1 (every listed object was
// copied). Physical HEAD/checksum verification against the target bucket, and
// old-prefix cleanup, are later slices — the source copies remain as a fallback.
func (w *StorageMigrationWorker) stepVerifying(ctx context.Context, m StorageMigration) error {
	if m.ObjectsCopied < m.ObjectsTotal {
		return fmt.Errorf("verify: copied %d < total %d", m.ObjectsCopied, m.ObjectsTotal)
	}
	w.log().Info("migration completed",
		zap.String("tenant_id", m.TenantID.String()), zap.Int64("objects", m.ObjectsCopied))
	return w.Repo.Complete(ctx, m.TenantID)
}

func (w *StorageMigrationWorker) log() *zap.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return zap.NewNop()
}

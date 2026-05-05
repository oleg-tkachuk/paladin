// Lifecycle worker — periodic CEL evaluator that soft-deletes (and
// eventually transitions) objects per `bucket.lifecycle_rules`.
//
// v2 scope: expiration only. Transition rules are advisory until the S3
// lifecycle-config integration lands (slice 12+). Each tick:
//
//  1. ListBucketsWithLifecycle — only buckets with a non-empty rules array.
//  2. For each bucket, walk its bound object_keys and stream objects.
//  3. For each object, evaluate every rule whose CEL `match` compiles;
//     first matching `Expiration.after` whose age is exceeded triggers
//     soft-delete via the state machine.
//
// Idempotent: SoftDelete is a no-op on already-DELETED rows. Multiple
// replicas can run concurrently — the SQL state guards serialize.
package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// LifecycleObjectIter exposes the read seam the worker needs: enumerate
// objects under a (tenant, object_key) scope. Implementations stream
// pages — the worker iterates lazily to keep memory bounded.
type LifecycleObjectIter interface {
	IterateObjects(ctx context.Context, tenantID uuid.UUID, objectKey string, cb func(LifecycleObjectRow) error) error
}

// LifecycleObjectRow is the minimal projection per object the worker reads.
type LifecycleObjectRow struct {
	ObjectID    uuid.UUID
	State       string
	CreatedAt   time.Time
	CommittedAt *time.Time
	SizeBytes   int64
	ContentType string
	Tags        map[string]string
	Metadata    map[string]string
}

// BucketLifecycleSource lists buckets that carry non-empty lifecycle rules.
// Plus enumerates the (tenant, object_key) pairs bound to each bucket so
// the worker can scope its scans.
type BucketLifecycleSource interface {
	ListBucketsWithLifecycle(ctx context.Context) ([]admindomain.Bucket, error)
	ListObjectKeyBindings(ctx context.Context, backendID, bucketName string) ([]ObjectKeyBinding, error)
}

type ObjectKeyBinding struct {
	TenantID  uuid.UUID
	ObjectKey string
}

// LifecycleWorker is the worker fan entry. SoftDeleter is the state-machine
// seam (just SoftDelete; the worker never touches HardDelete).
type LifecycleWorker struct {
	Buckets     BucketLifecycleSource
	Objects     LifecycleObjectIter
	SoftDeleter SoftDeleter
	Interval    time.Duration
	Logger      *zap.Logger

	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// SoftDeleter is the state-machine seam. *statemachine.Transitioner
// satisfies this with SoftDelete(ctx, objectID, expectedVersion=0).
type SoftDeleter interface {
	SoftDelete(ctx context.Context, objectID uuid.UUID, expectedVersion int64) error
}

func (w *LifecycleWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 30 * time.Minute
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *LifecycleWorker) tick(ctx context.Context) {
	buckets, err := w.Buckets.ListBucketsWithLifecycle(ctx)
	if err != nil {
		w.log().Warn("lifecycle: list buckets failed", zap.Error(err))
		return
	}
	for _, b := range buckets {
		if err := ctx.Err(); err != nil {
			return
		}
		w.processBucket(ctx, b)
	}
}

func (w *LifecycleWorker) processBucket(ctx context.Context, b admindomain.Bucket) {
	expirers := buildExpirers(b.LifecycleRules, w.Now())
	if len(expirers) == 0 {
		return // no expiration rules → nothing to evaluate this tick
	}
	bindings, err := w.Buckets.ListObjectKeyBindings(ctx, b.BackendID, b.BucketName)
	if err != nil {
		w.log().Warn("lifecycle: list bindings failed",
			zap.String("backend", b.BackendID),
			zap.String("bucket", b.BucketName),
			zap.Error(err))
		return
	}
	for _, bind := range bindings {
		if err := ctx.Err(); err != nil {
			return
		}
		err := w.Objects.IterateObjects(ctx, bind.TenantID, bind.ObjectKey, func(row LifecycleObjectRow) error {
			if row.State != string(statemachine.StateAvailable) {
				return nil
			}
			for _, e := range expirers {
				if e.matches(row) {
					if err := w.SoftDeleter.SoftDelete(ctx, row.ObjectID, 0); err != nil {
						w.log().Warn("lifecycle: soft delete failed",
							zap.String("object_id", row.ObjectID.String()),
							zap.Error(err))
						return nil
					}
					w.log().Info("lifecycle: expired object",
						zap.String("rule", e.id),
						zap.String("object_id", row.ObjectID.String()))
					return nil
				}
			}
			return nil
		})
		if err != nil {
			w.log().Warn("lifecycle: iterate objects failed",
				zap.String("tenant", bind.TenantID.String()),
				zap.String("object_key", bind.ObjectKey),
				zap.Error(err))
		}
	}
}

func (w *LifecycleWorker) log() *zap.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return zap.NewNop()
}

// expirer is a compiled lifecycle rule projection. v2 implements only
// "expire after age" — no CEL match yet, just the time window. CEL will
// land alongside the dispatcher CEL upgrade (slice 12+).
type expirer struct {
	id      string
	enabled bool
	cutoff  time.Time
}

func (e expirer) matches(row LifecycleObjectRow) bool {
	if !e.enabled {
		return false
	}
	created := row.CreatedAt
	if row.CommittedAt != nil {
		created = *row.CommittedAt
	}
	return created.Before(e.cutoff)
}

func buildExpirers(rules []admindomain.LifecycleRule, now time.Time) []expirer {
	out := make([]expirer, 0, len(rules))
	for _, r := range rules {
		if r.Expiration == nil || r.Expiration.After <= 0 {
			continue
		}
		out = append(out, expirer{
			id:      r.ID,
			enabled: r.Enabled,
			cutoff:  now.Add(-r.Expiration.After),
		})
	}
	return out
}

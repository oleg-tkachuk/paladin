// Replication worker — async cross-backend copy of newly-promoted objects.
//
// Design (skeleton):
//
//  1. Tick scans bucket rows where `replication.enabled=true`.
//  2. For each replicated bucket, walk recently-AVAILABLE objects via the
//     shared LifecycleObjectIter (newest-first cursor; a per-bucket
//     `replicated_until` watermark caps the scan).
//  3. CEL `replication.filter` selection (slice 14+) — for now, all
//     AVAILABLE objects are copied.
//  4. For each match, call StorageReplicator.Replicate which copies the
//     bytes from the source bucket to `replication.destination_bucket`
//     using the same collection prefix.
//  5. On success, advance the watermark.
//
// v2 ships the worker scaffold + test stubs; the actual S3-side bytewise
// copy lives in slice 15 along with a `replication_state` table that
// tracks per-bucket high-water mark across restarts.
package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// StorageReplicator copies one object from source → destination bucket.
// Implementations live in internal/storage; the worker only sees this seam.
type StorageReplicator interface {
	Replicate(ctx context.Context, src, dst ReplicationTarget) error
}

// ReplicationTarget identifies one side of a copy. Collection + Key are the
// logical pair; BackendID + BucketName are the physical routing.
type ReplicationTarget struct {
	BackendID  string
	BucketName string
	TenantID   uuid.UUID
	Collection string
	Key        string
}

// ReplicationSource lists buckets with replication enabled.
type ReplicationSource interface {
	ListBucketsWithReplication(ctx context.Context) ([]admindomain.Bucket, error)
	ListCollectionBindings(ctx context.Context, backendID, bucketName string) ([]CollectionBinding, error)
}

// WatermarkStore persists the per-bucket replication high-water mark
// across restarts. Implementations live in postgres adapters; the worker
// only sees Get/Advance and degrades gracefully when the store is unset
// (in-memory mode, suitable for dev / tests).
type WatermarkStore interface {
	Get(ctx context.Context, backendID, bucketName string) (time.Time, error)
	Advance(ctx context.Context, backendID, bucketName string, t time.Time) error
}

// ReplicationWorker fans out cross-backend copies. nil-safe constructor —
// passing a nil StorageReplicator yields a worker that walks objects and
// logs intent without actually copying (handy for dry-run validation).
//
// WatermarkStore is optional. When set, the worker reads the per-bucket
// high-water mark from the store on each tick and writes back after
// successful copies; in-memory cache fronts both calls so the hot path
// stays one DB roundtrip per bucket per tick. When unset, watermarks
// live only in process memory (lost on restart — fine for dev).
type ReplicationWorker struct {
	Buckets    ReplicationSource
	Objects    LifecycleObjectIter
	Replicator StorageReplicator
	Watermarks WatermarkStore
	Interval   time.Duration
	Logger     *zap.Logger
	Now        func() time.Time

	// LookbackWindow caps how far back the worker scans on first run when
	// no watermark is found in the store.
	LookbackWindow time.Duration

	watermarks map[string]time.Time // in-memory cache of last-seen committed_at
}

func (r *ReplicationWorker) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = 5 * time.Minute
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.LookbackWindow <= 0 {
		r.LookbackWindow = 1 * time.Hour
	}
	if r.watermarks == nil {
		r.watermarks = map[string]time.Time{}
	}
	return RunTicker(ctx, "replication", r.Interval, func(ctx context.Context) error {
		r.tick(ctx)
		return nil
	})
}

func (r *ReplicationWorker) tick(ctx context.Context) {
	buckets, err := r.Buckets.ListBucketsWithReplication(ctx)
	if err != nil {
		r.log().Warn("failed to list buckets", zap.Error(err))
		return
	}
	for _, b := range buckets {
		if err := ctx.Err(); err != nil {
			return
		}
		r.processBucket(ctx, b)
	}
}

func (r *ReplicationWorker) processBucket(ctx context.Context, b admindomain.Bucket) {
	if !b.Replication.Enabled || b.Replication.DestinationBucket == "" {
		return
	}
	dstBackend, dstBucket, err := splitBucketName(b.Replication.DestinationBucket)
	if err != nil {
		r.log().Warn("invalid destination", zap.String("dst", b.Replication.DestinationBucket), zap.Error(err))
		return
	}
	bindings, err := r.Buckets.ListCollectionBindings(ctx, b.BackendID, b.BucketName)
	if err != nil {
		r.log().Warn("failed to list collection bindings",
			zap.String("backend", b.BackendID),
			zap.String("bucket", b.BucketName),
			zap.Error(err))
		return
	}
	cutoff := r.cutoff(ctx, b.BackendID, b.BucketName)
	for _, bind := range bindings {
		if err := ctx.Err(); err != nil {
			return
		}
		err := r.Objects.IterateObjects(ctx, bind.TenantID, bind.Collection, func(row LifecycleObjectRow) error {
			if row.State != "AVAILABLE" {
				return nil
			}
			committed := row.CreatedAt
			if row.CommittedAt != nil {
				committed = *row.CommittedAt
			}
			if !committed.After(cutoff) {
				return nil // already replicated (or out of window)
			}
			src := ReplicationTarget{
				BackendID:  b.BackendID,
				BucketName: b.BucketName,
				TenantID:   bind.TenantID,
				Collection: bind.Collection,
			}
			dst := ReplicationTarget{
				BackendID:  dstBackend,
				BucketName: dstBucket,
				TenantID:   bind.TenantID,
				Collection: bind.Collection,
			}
			if r.Replicator == nil {
				r.log().Info("dry-run match",
					zap.String("object_id", row.ObjectID.String()))
				return nil
			}
			if err := r.Replicator.Replicate(ctx, src, dst); err != nil {
				r.log().Warn("failed to copy object",
					zap.String("object_id", row.ObjectID.String()),
					zap.Error(err))
				return nil
			}
			r.advance(ctx, b.BackendID, b.BucketName, committed)
			return nil
		})
		if err != nil {
			r.log().Warn("failed to iterate objects", zap.Error(err))
		}
	}
}

// cutoff resolves the start-of-window for a bucket. Layering:
//  1. in-memory cache (hot path, no DB hit)
//  2. WatermarkStore (cold start / new bucket)
//  3. now − LookbackWindow (no prior state)
func (r *ReplicationWorker) cutoff(ctx context.Context, backendID, bucketName string) time.Time {
	key := backendID + "/" + bucketName
	if t, ok := r.watermarks[key]; ok {
		return t
	}
	if r.Watermarks != nil {
		if t, err := r.Watermarks.Get(ctx, backendID, bucketName); err == nil && !t.IsZero() {
			r.watermarks[key] = t
			return t
		}
	}
	return r.Now().Add(-r.LookbackWindow)
}

// advance moves the watermark forward — first in memory, then to the
// store. Monotonic: never moves backward. Persistence errors are
// non-fatal (the next tick re-tries), so a transient DB blip can't stall
// replication progress.
func (r *ReplicationWorker) advance(ctx context.Context, backendID, bucketName string, t time.Time) {
	key := backendID + "/" + bucketName
	if cur, ok := r.watermarks[key]; ok && !t.After(cur) {
		return
	}
	r.watermarks[key] = t
	if r.Watermarks != nil {
		if err := r.Watermarks.Advance(ctx, backendID, bucketName, t); err != nil {
			r.log().Warn("failed to advance watermark",
				zap.String("backend", backendID),
				zap.String("bucket", bucketName),
				zap.Error(err))
		}
	}
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

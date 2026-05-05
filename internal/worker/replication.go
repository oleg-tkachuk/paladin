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
//     using the same object_key prefix.
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

// ReplicationTarget identifies one side of a copy. ObjectKey + Key are the
// logical pair; BackendID + BucketName are the physical routing.
type ReplicationTarget struct {
	BackendID  string
	BucketName string
	TenantID   uuid.UUID
	ObjectKey  string
	Key        string
}

// ReplicationSource lists buckets with replication enabled.
type ReplicationSource interface {
	ListBucketsWithReplication(ctx context.Context) ([]admindomain.Bucket, error)
	ListObjectKeyBindings(ctx context.Context, backendID, bucketName string) ([]ObjectKeyBinding, error)
}

// ReplicationWorker fans out cross-backend copies. nil-safe constructor —
// passing a nil StorageReplicator yields a worker that walks objects and
// logs intent without actually copying (handy for dry-run validation).
type ReplicationWorker struct {
	Buckets    ReplicationSource
	Objects    LifecycleObjectIter
	Replicator StorageReplicator
	Interval   time.Duration
	Logger     *zap.Logger
	Now        func() time.Time

	// LookbackWindow caps how far back the worker scans on first run; the
	// in-memory watermark is good for the lifetime of the process. Slice 15
	// will replace this with a DB-backed watermark.
	LookbackWindow time.Duration

	watermarks map[string]time.Time // backend/bucket → last-seen committed_at
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
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *ReplicationWorker) tick(ctx context.Context) {
	buckets, err := r.Buckets.ListBucketsWithReplication(ctx)
	if err != nil {
		r.log().Warn("replication: list buckets failed", zap.Error(err))
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
		r.log().Warn("replication: invalid destination", zap.String("dst", b.Replication.DestinationBucket), zap.Error(err))
		return
	}
	bindings, err := r.Buckets.ListObjectKeyBindings(ctx, b.BackendID, b.BucketName)
	if err != nil {
		r.log().Warn("replication: list bindings failed",
			zap.String("backend", b.BackendID),
			zap.String("bucket", b.BucketName),
			zap.Error(err))
		return
	}
	cutoff := r.cutoff(b.BackendID + "/" + b.BucketName)
	for _, bind := range bindings {
		if err := ctx.Err(); err != nil {
			return
		}
		err := r.Objects.IterateObjects(ctx, bind.TenantID, bind.ObjectKey, func(row LifecycleObjectRow) error {
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
				ObjectKey:  bind.ObjectKey,
			}
			dst := ReplicationTarget{
				BackendID:  dstBackend,
				BucketName: dstBucket,
				TenantID:   bind.TenantID,
				ObjectKey:  bind.ObjectKey,
			}
			if r.Replicator == nil {
				r.log().Info("replication: dry-run match",
					zap.String("object_id", row.ObjectID.String()))
				return nil
			}
			if err := r.Replicator.Replicate(ctx, src, dst); err != nil {
				r.log().Warn("replication: copy failed",
					zap.String("object_id", row.ObjectID.String()),
					zap.Error(err))
				return nil
			}
			r.advance(b.BackendID+"/"+b.BucketName, committed)
			return nil
		})
		if err != nil {
			r.log().Warn("replication: iterate failed", zap.Error(err))
		}
	}
}

func (r *ReplicationWorker) cutoff(key string) time.Time {
	if t, ok := r.watermarks[key]; ok {
		return t
	}
	return r.Now().Add(-r.LookbackWindow)
}

func (r *ReplicationWorker) advance(key string, t time.Time) {
	if cur, ok := r.watermarks[key]; ok && !t.After(cur) {
		return
	}
	r.watermarks[key] = t
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

package app

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// replicaStore is the part of s3adapter.ObjectRouter replication uses.
type replicaStore interface {
	Replicate(ctx context.Context, src, dst objecth.Location, sizeBytes int64) error
	Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, checksumAlgo string) (string, int64, string, string, error)
}

// storageReplicator adapts the storage router to worker.StorageReplicator,
// translating the adapter's errors into the worker's: a confirmed-missing
// source into worker.ErrObjectMissing, and a failure the SDK would retry
// into worker.ErrCopyTransient — a replica that did not match into
// worker.ErrCopyMismatch as well. The worker never imports the SDK's types.
type storageReplicator struct {
	s           replicaStore
	isTransient func(error) bool
}

// NewStorageReplicator is the replicator the worker is wired with, routed
// through reg's backends.
func NewStorageReplicator(reg *s3adapter.BackendRegistry) worker.StorageReplicator {
	return newStorageReplicator(s3adapter.NewObjectRouter(reg))
}

func newStorageReplicator(s replicaStore) storageReplicator {
	return storageReplicator{s: s, isTransient: s3adapter.IsTransient}
}

var _ worker.StorageReplicator = storageReplicator{}

func (r storageReplicator) Replicate(ctx context.Context, src, dst worker.ReplicationTarget, sizeBytes int64) error {
	err := r.s.Replicate(ctx, replicaLoc(src), replicaLoc(dst), sizeBytes)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, objecth.ErrStoredObjectNotFound):
		return fmt.Errorf("%w: %w", worker.ErrObjectMissing, err)
	case errors.Is(err, s3adapter.ErrReplicaMismatch):
		return fmt.Errorf("%w: %w: %w", worker.ErrCopyTransient, worker.ErrCopyMismatch, err)
	case r.isTransient(err):
		return fmt.Errorf("%w: %w", worker.ErrCopyTransient, err)
	default:
		return err
	}
}

// SourceMissing HEADs the source: the router answers ErrStoredObjectNotFound
// only once the bucket is confirmed reachable.
func (r storageReplicator) SourceMissing(ctx context.Context, src worker.ReplicationTarget) (bool, error) {
	_, _, _, _, err := r.s.Head(ctx, src.BackendID, src.BucketName, src.TenantID, src.Collection, src.Key, "")
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, objecth.ErrStoredObjectNotFound):
		return true, nil
	default:
		return false, err
	}
}

func replicaLoc(t worker.ReplicationTarget) objecth.Location {
	return objecth.Location{
		BackendID: t.BackendID, Bucket: t.BucketName, TenantID: t.TenantID,
		Collection: t.Collection, Key: t.Key,
	}
}

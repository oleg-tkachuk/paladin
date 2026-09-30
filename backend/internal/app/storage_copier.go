package app

import (
	"context"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// storageCopier adapts object.Storage (the s3 router) to worker.ObjectCopier,
// translating the worker-local CopyLocation to object.Location. The worker
// can't reference object.Location directly — internal/api/data/v1/objecth imports
// internal/worker, so the reverse would be an import cycle.
type storageCopier struct{ s objecth.Storage }

func (c storageCopier) CopyObject(ctx context.Context, src, dst worker.CopyLocation) error {
	return c.s.CopyObject(ctx, objLoc(src), objLoc(dst))
}

// DeleteObject removes one physical object — used by the migration cleanup
// phase to delete the old (shared) copies after the retention window.
func (c storageCopier) DeleteObject(ctx context.Context, loc worker.CopyLocation) error {
	return c.s.DeleteObject(ctx, loc.BackendID, loc.Bucket, loc.TenantID, loc.Collection, loc.Key)
}

// HeadObject returns the physical size of one object — used by the verify phase
// to confirm each copy landed in the target bucket.
func (c storageCopier) HeadObject(ctx context.Context, loc worker.CopyLocation) (int64, error) {
	_, size, _, _, err := c.s.Head(ctx, loc.BackendID, loc.Bucket, loc.TenantID, loc.Collection, loc.Key)
	return size, err
}

func objLoc(l worker.CopyLocation) objecth.Location {
	return objecth.Location{
		BackendID:  l.BackendID,
		TenantID:   l.TenantID,
		Bucket:     l.Bucket,
		Collection: l.Collection,
		Key:        l.Key,
	}
}

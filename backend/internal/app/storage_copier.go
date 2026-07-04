package app

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// storageCopier adapts object.Storage (the s3 router) to worker.ObjectCopier,
// translating the worker-local CopyLocation to object.Location. The worker
// can't reference object.Location directly — internal/api/v1/object imports
// internal/worker, so the reverse would be an import cycle.
type storageCopier struct{ s object.Storage }

func (c storageCopier) CopyObject(ctx context.Context, src, dst worker.CopyLocation) error {
	return c.s.CopyObject(ctx, objLoc(src), objLoc(dst))
}

func objLoc(l worker.CopyLocation) object.Location {
	return object.Location{
		BackendID: l.BackendID,
		TenantID:  l.TenantID,
		Bucket:    l.Bucket,
		ObjectKey: l.ObjectKey,
		Key:       l.Key,
	}
}

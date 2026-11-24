package objecth

import (
	"context"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// Placement is where an object's bytes physically live: the storage backend,
// the bucket on it, and the object's key inside that bucket.
type Placement struct {
	BackendID   string
	Bucket      string
	StoragePath string // "<tenant>/<collection segment>/<key>" (storagepath.Compose)
}

// placementFor builds obj's placement from the bucket its bytes are in
// (Object.Stored, ADR-0051), which the collection's binding need not be; for
// a PENDING upload, placed nowhere yet, the bucket its URL was signed for
// (Object.Upload), where the bytes land. An object with neither has no
// placement to report: nil, not a half-empty one. Nor has an object whose
// key predates the key contract: its key composes to no path of its own
// (storagepath.Compose refuses it).
func placementFor(ctx context.Context, tenantID uuid.UUID, obj *Object) *Placement {
	ref := obj.Stored
	if ref == nil {
		ref = obj.Upload
	}
	if ref == nil {
		return nil
	}
	backendID, bucket := ref.BackendID, ref.Bucket
	path, err := storagepath.Compose(tenantID, obj.Collection, obj.Key)
	if err != nil {
		logger.FromContext(ctx).Warn("object key is outside the key contract; reporting no placement",
			zap.String(fieldCollection, obj.Collection), zap.String(fieldObjectID, obj.ObjectID.String()), zap.Error(err))
		return nil
	}
	return &Placement{
		BackendID:   backendID,
		Bucket:      bucket,
		StoragePath: path,
	}
}

// mayReadPlacement asks Cedar whether principal may see the physical
// placement of res (ActionReadObjectPlacement). It runs after the read itself
// was authorised, on the same resource, and is enrichment in the attachLock
// sense: a denial or an engine fault answers false — the field stays unset —
// and never turns a successful read into an error.
//
// Placement is infrastructure detail, so GetObject does not imply it: only
// platform.admin holds it by default (the built-in permit), and a tenant
// policy may grant it to anyone else. A capability-only principal carries no
// roles, so it sees placement only when a policy permits it explicitly.
func (h *Handler) mayReadPlacement(ctx context.Context, principal *auth.Principal, res *cedar.Resource, sizeBytes int64, contentType string) bool {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(principal),
		cedar.ActionReadObjectPlacement,
		res,
		cedar.RequestContext{SizeBytes: sizeBytes, ContentType: contentType, Now: time.Now()},
	)
	if err != nil {
		logger.FromContext(ctx).Warn("placement authorization failed; reporting object without placement",
			zap.String("collection", res.Collection), zap.Error(err))
		return false
	}
	return decision == cedar.DecisionAllow
}

// attachPlacement sets obj.Placement for a single-object read when the caller
// may see it. res is the resource the read was authorised against.
func (h *Handler) attachPlacement(ctx context.Context, principal *auth.Principal, res *cedar.Resource, obj *Object) {
	p := placementFor(ctx, res.TenantID, obj)
	if p == nil {
		return
	}
	if h.mayReadPlacement(ctx, principal, res, obj.SizeBytes, obj.ContentType) {
		obj.Placement = p
	}
}

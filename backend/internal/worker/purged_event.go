package worker

import (
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
)

// purgeSourceLifecycle is the source a lifecycle hard delete's purge event
// names, telling it apart from a permanent delete's.
const purgeSourceLifecycle = "lifecycle"

// purgedObject is what a paladin.object.purged event says was reclaimed.
type purgedObject struct {
	TenantID   uuid.UUID
	ObjectID   uuid.UUID
	Collection string
	Key        string
	Backend    string
	Bucket     string
}

// objectPurgedPayload is the payload every worker that reclaims an object's
// bytes emits, with the keys the object handler's purge event uses.
func objectPurgedPayload(o purgedObject) map[string]any {
	return map[string]any{
		eventtype.PayloadTenantID:   o.TenantID.String(),
		eventtype.PayloadCollection: o.Collection,
		eventtype.PayloadKey:        o.Key,
		eventtype.PayloadObjectID:   o.ObjectID.String(),
		eventtype.PayloadBackendID:  o.Backend,
		eventtype.PayloadBucket:     o.Bucket,
		eventtype.PayloadReclaimed:  true,
	}
}

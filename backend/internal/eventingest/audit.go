package eventingest

import (
	"context"
	"encoding/json"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// AuditWriter records an audit row. Satisfied by *adapters.AuditRepoV2,
// which scopes the insert to the row's actor tenant.
type AuditWriter interface {
	Insert(ctx context.Context, e admindomain.AuditEntry) error
}

const (
	// ingestActor is who an ingested transition is recorded as: the
	// pipeline, acting on what the storage backend reported, not a client.
	ingestActor = auth.SubjectPrefixSystem + "event-ingest"
	// The actions of the rows a storage event's transitions write, as the
	// data-plane RPCs that make the same transitions are recorded.
	actionObjectUploaded = "ingest.ObjectUploaded"
	actionObjectDeleted  = "ingest.ObjectDeleted"
)

// ingestDetail is what an audit row records of the event behind it.
type ingestDetail struct {
	ObjectID    string `json:"object_id"`
	EventID     string `json:"event_id"`
	EventSource string `json:"event_source"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	ETag        string `json:"etag,omitempty"`
}

// record writes the audit row of a transition a storage event made, after it
// committed — as the interceptor writes an RPC's — and logs a row that could
// not be written. A nil writer writes nothing.
func (h *PromoteHandler) record(ctx context.Context, logger *zap.Logger, ev CloudEvent, action, resourceName string, tenantID, objectID uuid.UUID) {
	if h.Audit == nil {
		return
	}
	detail, err := json.Marshal(ingestDetail{
		ObjectID: objectID.String(), EventID: ev.ID, EventSource: ev.Source,
		SizeBytes: ev.SubjectFields.SizeBytes, ETag: ev.SubjectFields.Etag,
	})
	if err != nil {
		detail = nil
	}
	if err := h.Audit.Insert(ctx, admindomain.AuditEntry{
		EntryID:       uuid.NewV7(),
		At:            time.Now().UTC(),
		ActorSubject:  ingestActor,
		ActorTenantID: tenantID,
		ActorAudience: auth.AudienceData,
		Action:        action,
		ResourceName:  resourceName,
		RequestJSON:   detail,
	}); err != nil {
		logger.Error("audit entry not written", zap.String("action", action), zap.Error(err))
	}
}

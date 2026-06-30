package app

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// auditMirror adapts *worker.Dispatcher to middleware.AuditMirrorEmitter.
//
// Same shape as chargeEmitter — wiring layer bridges concerns the
// auth / middleware packages can't import directly without cycles.
//
// Cardinality warning lives at the wiring site (only constructed
// when cfg.Dispatcher.AuditMirrorEnabled is true). Subscribers MUST
// CEL-filter — every successful mutation across api / admin / iam
// fires through here, so an unfiltered NATS / HTTP sink will get
// the full mutation rate of the cluster.
type auditMirror struct {
	dispatcher eventDispatcher
	log        *zap.Logger
}

func newAuditMirror(d *worker.Dispatcher, l *zap.Logger) middleware.AuditMirrorEmitter {
	if d == nil {
		return nil
	}
	return &auditMirror{dispatcher: d, log: l}
}

// optionalAuditMirror returns a wired emitter when the toggle is on
// AND the dispatcher is non-nil, else nil. Single helper so admin /
// api / iam wiring sites read the same way: pass cfg + dispatcher,
// get either an emitter or nil. AuditWithMirror handles nil natively.
func optionalAuditMirror(enabled bool, d *worker.Dispatcher, l *zap.Logger) middleware.AuditMirrorEmitter {
	if !enabled {
		return nil
	}
	return newAuditMirror(d, l)
}

// EmitAudited fans out one paladin.audit.<action> event per AuditEntry
// row. Best-effort — same semantics as auditInterceptor.write
// itself: the row already committed; a mirror failure must not
// flip the RPC reply.
//
// The event type is derived from the entry's Action field by
// trimming the leading service path and lower-casing the method
// (`/paladin.admin.v1.TenantService/CreateTenant` →
// `paladin.audit.create_tenant`). Subscribers route on this; handlers
// that share an Action prefix become a subject family.
func (m *auditMirror) EmitAudited(ctx context.Context, entry admindomain.AuditEntry) {
	if entry.ActorTenantID == uuid.Nil {
		// No tenant on the entry → no fan-out target. Most often a
		// pre-auth or platform-level call (Login, refresh, federated
		// IdP callback). Subscribers haven't asked for these in v1.
		return
	}
	queued, err := m.dispatcher.Dispatch(ctx, entry.ActorTenantID.String(), worker.Event{
		Type:         auditEventType(entry.Action),
		At:           entry.At,
		TenantID:     entry.ActorTenantID.String(),
		ResourceName: entry.ResourceName,
		ActorSubject: entry.ActorSubject,
		Payload: map[string]any{
			"audit_entry_id": entry.EntryID.String(),
			"action":         entry.Action,
			"audience":       entry.ActorAudience,
			"resource":       entry.ResourceName,
			"request_id":     entry.RequestID,
			"source_ip":      entry.SourceIP,
			"capability_id":  entry.CapabilityID.String(),
			"error_message":  entry.ErrorMessage,
		},
	})
	if err != nil {
		if m.log != nil {
			m.log.Warn("audit mirror fan-out failed",
				zap.String("audit_entry_id", entry.EntryID.String()),
				zap.String("action", entry.Action),
				zap.String("tenant_id", entry.ActorTenantID.String()),
				zap.Error(err),
			)
		}
		return
	}
	if m.log != nil {
		m.log.Debug("audit mirror queued",
			zap.String("action", entry.Action),
			zap.Int("subscriptions_matched", queued),
		)
	}
	_ = time.Now // keep imports tidy if future expansion uses time directly
}

// auditEventType derives an event class string from the audit
// entry's Action field. Inputs are connect-rpc procedure paths like:
//
//	"/paladin.admin.v1.TenantService/CreateTenant"
//
// We strip the leading "/paladin.<plane>.v1.<Service>/" prefix and
// snake_case the method, prepending `paladin.audit.`. Falls back to a
// hashed-but-still-meaningful representation when the input doesn't
// match the expected shape.
func auditEventType(action string) string {
	method := action
	if i := strings.LastIndexByte(action, '/'); i >= 0 {
		method = action[i+1:]
	}
	return "paladin.audit." + snakeCase(method)
}

// snakeCase lowers + inserts underscores at uppercase boundaries.
// "CreateTenant" → "create_tenant". Cheap, no regex.
func snakeCase(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			r = r + 32
		}
		b.WriteRune(r)
	}
	return b.String()
}

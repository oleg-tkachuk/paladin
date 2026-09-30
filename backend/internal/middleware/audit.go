// Audit-write interceptor.
//
// Wraps every Connect RPC and inserts an entry into admindomain.AuditRepository
// when the call mutates state. Idempotent reads (RPC names starting with
// `Get`, `List`, `Lookup`, `Validate`, `Simulate`, `WhoAmI`) are skipped.
//
// The interceptor is plane-aware — `audience` is recorded so audits can be
// filtered per plane.
package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// auditBeforeKey carries a pre-mutation snapshot stashed by a handler so the
// audit interceptor can record before/after JSON without re-reading the row.
type auditBeforeKey struct{}

// Canonical resource-name stash lives in apiutil (see audit_stash.go)
// so domain handlers can call StashResource without importing this
// middleware package (which sits above them in the dependency graph).

func beforeFromContext(ctx context.Context) []byte {
	v := ctx.Value(auditBeforeKey{})
	if v == nil {
		return nil
	}
	return marshalAuditPayload(v)
}

func marshalAuditPayload(v any) []byte {
	if v == nil {
		return nil
	}
	if m, ok := v.(proto.Message); ok {
		b, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}.Marshal(m)
		if err == nil {
			return b
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// AuditWriter inserts audit log entries. Implementations are typically the
// Postgres adapter via admindomain.AuditRepository.
//
// InsertWithOutbox inserts the entry and, when onInserted is non-nil, runs
// it inside the SAME transaction before commit — so the audit row and any
// fan-out outbox rows the mirror enqueues commit atomically (ADR-0003, no
// dual-write window). onInserted == nil behaves exactly like Insert.
type AuditWriter interface {
	Insert(ctx context.Context, e admindomain.AuditEntry) error
	InsertWithOutbox(ctx context.Context, e admindomain.AuditEntry, onInserted func(ctx context.Context, tx pgx.Tx) error) error
}

// AuditMirrorEmitter is the optional fan-out seam every Audit row
// runs through in the audit-insert transaction. nil-safe: when unwired
// the interceptor behaves exactly like the pre-mirror Audit.
// Implementations live in the wiring layer (app/audit_mirror.go).
//
// EmitAuditedTx enqueues the mirror event's outbox rows on `tx` — the
// audit row's own transaction — so the event is atomic with the row. An
// error rolls the audit row back with the fan-out.
type AuditMirrorEmitter interface {
	EmitAuditedTx(ctx context.Context, tx pgx.Tx, entry admindomain.AuditEntry) error
}

// AuditWithMirror is the events-aware variant — fan-out one
// `paladin.audit.<action>` event per Insert when `mirror` is non-nil.
// Wiring at the app layer gates this on
// cfg.Dispatcher.AuditMirrorEnabled (default off — even higher
// cardinality than charge events because every mutation logs).
func AuditWithMirror(w AuditWriter, audience string, recordReads bool, mirror AuditMirrorEmitter) connect.Interceptor {
	return &auditInterceptor{
		w:           w,
		audience:    audience,
		recordReads: recordReads,
		mirror:      mirror,
	}
}

type auditInterceptor struct {
	w           AuditWriter
	audience    string
	recordReads bool
	mirror      AuditMirrorEmitter
}

func (a *auditInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Install a mutable resource-name slot the handler can write
		// the canonical (A-shape) name into. Audit row picks it up
		// after the handler returns (see preferCanonical).
		ctx = apiutil.WithResourceSlot(ctx)
		resp, err := next(ctx, req)
		if a.shouldSkip(req.Spec().Procedure) {
			return resp, err
		}
		// Best-effort in the sense that it never blocks or fails the RPC — NOT
		// in the sense that nobody is told. This is the compliance trail (SOC 2
		// / ISO 27001 / PCI), written synchronously and crash-durably per
		// ADR-0004, and its error was discarded here with `_ =`. A failing
		// insert stopped the trail with no error, no metric and no log line:
		// the one failure mode that makes an audit log worse than none, because
		// its absence reads as "nothing happened".
		//
		// Error, not Warn. An audit write that fails is a defect with an
		// external consequence, and the rate of these is something someone
		// should be paged about.
		if werr := a.write(ctx, req, err); werr != nil {
			logger.FromContext(ctx).Error("audit entry not written",
				zap.String("rpc", req.Spec().Procedure), zap.Error(werr))
		}
		return resp, err
	}
}

func (a *auditInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *auditInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		err := next(ctx, conn)
		if a.shouldSkip(conn.Spec().Procedure) {
			return err
		}
		_ = a.writeStream(ctx, conn.Spec().Procedure, conn.RequestHeader().Get("X-Request-Id"), err)
		return err
	}
}

func (a *auditInterceptor) shouldSkip(procedure string) bool {
	if a.recordReads {
		return false
	}
	// Procedure shape: "/paladin.admin.v1.BackendService/GetBackend"
	method := procedure
	if i := strings.LastIndexByte(procedure, '/'); i >= 0 {
		method = procedure[i+1:]
	}
	switch {
	case strings.HasPrefix(method, "Get"),
		strings.HasPrefix(method, "List"),
		strings.HasPrefix(method, "Lookup"),
		strings.HasPrefix(method, "Count"),
		strings.HasPrefix(method, "Validate"),
		strings.HasPrefix(method, "Simulate"),
		method == "WhoAmI",
		method == "RefreshToken",
		method == "Login":
		return true
	}
	return false
}

func (a *auditInterceptor) write(ctx context.Context, req connect.AnyRequest, rpcErr error) error {
	subject, tenantID := principalCoords(ctx)
	entry := admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            time.Now().UTC(),
		ActorSubject:  subject,
		ActorTenantID: tenantID,
		ActorAudience: a.audience,
		Action:        req.Spec().Procedure,
		ResourceName:  preferCanonical(ctx, req.Any()),
		RequestID:     req.Header().Get("X-Request-Id"),
		SourceIP:      req.Header().Get("X-Forwarded-For"),
		CapabilityID:  capabilityID(ctx),
	}
	if rpcErr != nil {
		entry.ErrorMessage = rpcErr.Error()
	}
	entry.BeforeJSON = beforeFromContext(ctx)
	entry.AfterJSON = marshalAuditPayload(req.Any())
	// The audit row and its mirror event commit atomically: the mirror
	// enqueues its outbox rows on the insert's own transaction (ADR-0003).
	// nil mirror ⇒ nil hook ⇒ InsertWithOutbox degrades to a plain Insert.
	return a.w.InsertWithOutbox(ctx, entry, a.mirrorHook(entry))
}

// mirrorHook returns the transactional fan-out closure for entry, or nil
// when no mirror is wired (so InsertWithOutbox takes the plain-Insert path).
func (a *auditInterceptor) mirrorHook(entry admindomain.AuditEntry) func(context.Context, pgx.Tx) error {
	if a.mirror == nil {
		return nil
	}
	return func(ctx context.Context, tx pgx.Tx) error {
		return a.mirror.EmitAuditedTx(ctx, tx, entry)
	}
}

func (a *auditInterceptor) writeStream(ctx context.Context, procedure, requestID string, rpcErr error) error {
	subject, tenantID := principalCoords(ctx)
	entry := admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            time.Now().UTC(),
		ActorSubject:  subject,
		ActorTenantID: tenantID,
		ActorAudience: a.audience,
		Action:        procedure,
		RequestID:     requestID,
		CapabilityID:  capabilityID(ctx),
	}
	if rpcErr != nil {
		entry.ErrorMessage = rpcErr.Error()
	}
	return a.w.InsertWithOutbox(ctx, entry, a.mirrorHook(entry))
}

func principalCoords(ctx context.Context) (subject string, tenantID uuid.UUID) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil {
		return "", uuid.Nil
	}
	return p.Subject, p.TenantID
}

// capabilityID returns the verified capability's ID if one was presented
// on this request, else uuid.Nil. Read directly from the auth context
// value the CapabilityInterceptor stamps on success — no extra lookup,
// no fallback. JWT-only flows return uuid.Nil and the audit row has
// NULL capability_id.
func capabilityID(ctx context.Context) uuid.UUID {
	c, ok := auth.CapabilityFromContext(ctx)
	if !ok || c == nil {
		return uuid.Nil
	}
	return c.ID
}

// resourceFromMessage extracts a `name`/`parent` field from the request, if
// present, via reflection on the public Get* method names. Avoids importing
// every proto package; falls back to the message type name.
// preferCanonical returns the handler-stashed canonical resource
// name when present, else the C-shape `name`/`parent` from the
// request message. Audit rows for Collection-rooted mutations end up
// carrying the (backend, bucket, tenant, collection) tuple as long as
// the handler called StashResource before returning.
func preferCanonical(ctx context.Context, msg any) string {
	if s := apiutil.ResourceFromContext(ctx); s != "" {
		return s
	}
	return resourceFromMessage(msg)
}

func resourceFromMessage(msg any) string {
	if msg == nil {
		return ""
	}
	type withName interface{ GetName() string }
	type withParent interface{ GetParent() string }
	if m, ok := msg.(withName); ok && m.GetName() != "" {
		return m.GetName()
	}
	if m, ok := msg.(withParent); ok && m.GetParent() != "" {
		return m.GetParent()
	}
	return ""
}

// Audit-write interceptor.
//
// Wraps every Connect RPC and inserts an entry into admindomain.AuditRepository
// when the call mutates state. Reads are skipped: an RPC the contract declares
// `idempotency_level = NO_SIDE_EFFECTS`, and — for the RPCs that declare
// nothing yet — one named like a read (`Get…`, `List…`, `Lookup…`, `Count…`,
// `Validate…`, `Simulate…`). So are the session operations, which mint tokens
// rather than change state.
//
// The interceptor is plane-aware — `audience` is recorded so audits can be
// filtered per plane.
package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
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
		b, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}.Marshal(redacted(m))
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
func AuditWithMirror(w AuditWriter, audience string, recordReads bool, mirror AuditMirrorEmitter) connect.ServerInterceptor {
	return (&auditInterceptor{
		w:           w,
		audience:    audience,
		recordReads: recordReads,
		mirror:      mirror,
	}).interceptor()
}

type auditInterceptor struct {
	w           AuditWriter
	audience    string
	recordReads bool
	mirror      AuditMirrorEmitter
	// onlyElsewhere records only calls acting on a tenant other than the
	// caller's own (AuditActingElsewhere).
	onlyElsewhere bool
}

// AuditActingElsewhere audits only the calls a principal makes inside a
// tenant other than its own — on the data plane, a platform admin's work in a
// tenant it named (ActOnNamedTenant, which must run before it). Those are the
// calls the tenant's trail would otherwise miss: its own principals' writes
// are not audited on the data plane, and auditing every agent upload would
// put a synchronous insert on the hot path.
func AuditActingElsewhere(w AuditWriter, audience string) connect.ServerInterceptor {
	return (&auditInterceptor{w: w, audience: audience, onlyElsewhere: true}).interceptor()
}

// actingElsewhere reports whether ctx acts on a tenant other than the
// principal's own.
func actingElsewhere(ctx context.Context) bool {
	acting, ok := auth.ActingTenant(ctx)
	if !ok {
		return false
	}
	p, err := auth.PrincipalFromContext(ctx)
	return err == nil && acting != p.TenantID
}

// interceptor records a unary call with its request message, and a stream
// with what its headers say.
func (a *auditInterceptor) interceptor() connect.ServerInterceptor {
	return unary.Interceptor(a.unary, a.stream)
}

func (a *auditInterceptor) unary(next unary.Func) unary.Func {
	return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
		// Install a mutable resource-name slot the handler can write
		// the canonical (A-shape) name into. Audit row picks it up
		// after the handler returns (see preferCanonical).
		ctx = apiutil.WithResourceSlot(ctx)
		resp, err := next(ctx, spec, req)
		if a.shouldSkip(spec) || (a.onlyElsewhere && !actingElsewhere(ctx)) {
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
		if werr := a.write(ctx, spec, req, err); werr != nil {
			logger.FromContext(ctx).Error("audit entry not written",
				zap.String("rpc", spec.Procedure), zap.Error(werr))
		}
		return resp, err
	}
}

func (a *auditInterceptor) stream(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		err := next(ctx, spec, stream)
		if a.shouldSkip(spec) || (a.onlyElsewhere && !actingElsewhere(ctx)) {
			return err
		}
		_ = a.writeStream(ctx, spec.Procedure, unary.Info(ctx).RequestHeader().Get(HeaderRequestID), err)
		return err
	}
}

// sessionOperations mint or exchange tokens: every page load of the console
// runs them, and recording each one buried the mutations the log is for.
var sessionOperations = map[string]bool{
	"WhoAmI": true, "Login": true, "RefreshToken": true, "ExchangeAudience": true,
}

func (a *auditInterceptor) shouldSkip(spec connect.Spec) bool {
	if a.recordReads {
		return false
	}
	if spec.IdempotencyLevel == connect.IdempotencyNoSideEffects {
		return true
	}
	// Procedure shape: "/paladin.admin.v1.BackendService/GetBackend"
	method := spec.Procedure
	if i := strings.LastIndexByte(method, '/'); i >= 0 {
		method = method[i+1:]
	}
	if sessionOperations[method] {
		return true
	}
	switch {
	case strings.HasPrefix(method, "Get"),
		strings.HasPrefix(method, "List"),
		strings.HasPrefix(method, "Lookup"),
		strings.HasPrefix(method, "Count"),
		strings.HasPrefix(method, "Validate"),
		strings.HasPrefix(method, "Simulate"):
		return true
	}
	return false
}

func (a *auditInterceptor) write(ctx context.Context, spec connect.Spec, req proto.Message, rpcErr error) error {
	subject, tenantID := principalCoords(ctx)
	entry := admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            time.Now().UTC(),
		ActorSubject:  subject,
		ActorTenantID: tenantID,
		ActorAudience: a.audience,
		Action:        spec.Procedure,
		ResourceName:  preferCanonical(ctx, req),
		RequestID:     unary.Info(ctx).RequestHeader().Get(HeaderRequestID),
		SourceIP:      sourceIP(ctx),
		CapabilityID:  capabilityID(ctx),
	}
	if rpcErr != nil {
		entry.ErrorMessage = rpcErr.Error()
	}
	entry.BeforeJSON = beforeFromContext(ctx)
	entry.AfterJSON = marshalAuditPayload(req)
	// The audit row and its mirror event commit atomically: the mirror
	// enqueues its outbox rows on the insert's own transaction (ADR-0003).
	// nil mirror ⇒ nil hook ⇒ InsertWithOutbox degrades to a plain Insert.
	return a.w.InsertWithOutbox(asActor(ctx, entry.ActorTenantID), entry, a.mirrorHook(entry))
}

// asActor scopes the audit insert to its actor's tenant, which audit_log's
// insert policy requires: a call acting inside another tenant
// (AuditActingElsewhere) would otherwise insert under that tenant's scope and
// be refused.
func asActor(ctx context.Context, actorTenant uuid.UUID) context.Context {
	if actorTenant == uuid.Nil {
		return ctx
	}
	return auth.WithActingTenant(ctx, actorTenant)
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
	return a.w.InsertWithOutbox(asActor(ctx, entry.ActorTenantID), entry, a.mirrorHook(entry))
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

// sourceIP is the client address the listener resolved from its trusted
// proxies, or "" when there is none. Not the forwarding header itself: its
// leftmost entry is whatever the caller wrote, which an audit trail must not
// record as the source.
func sourceIP(ctx context.Context) string {
	if a, ok := clientip.FromContext(ctx); ok {
		return a.String()
	}
	return ""
}

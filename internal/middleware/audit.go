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
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// AuditWriter inserts audit log entries. Implementations are typically the
// Postgres adapter via admindomain.AuditRepository.
type AuditWriter interface {
	Insert(ctx context.Context, e admindomain.AuditEntry) error
}

// Audit returns a Connect interceptor that records every successful and
// failed mutation against the configured AuditRepository. Reads are skipped
// to keep audit volume manageable; turn `recordReads=true` for stricter
// compliance regimes.
func Audit(w AuditWriter, audience string, recordReads bool) connect.Interceptor {
	return &auditInterceptor{w: w, audience: audience, recordReads: recordReads}
}

type auditInterceptor struct {
	w           AuditWriter
	audience    string
	recordReads bool
}

func (a *auditInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)
		if a.shouldSkip(req.Spec().Procedure) {
			return resp, err
		}
		// Best-effort audit write; never block or fail the RPC on insert errors.
		_ = a.write(ctx, req, err)
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
		ResourceName:  resourceFromMessage(req.Any()),
		RequestID:     req.Header().Get("X-Request-Id"),
		SourceIP:      req.Header().Get("X-Forwarded-For"),
	}
	if rpcErr != nil {
		entry.ErrorMessage = rpcErr.Error()
	}
	return a.w.Insert(ctx, entry)
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
	}
	if rpcErr != nil {
		entry.ErrorMessage = rpcErr.Error()
	}
	return a.w.Insert(ctx, entry)
}

func principalCoords(ctx context.Context) (subject string, tenantID uuid.UUID) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil {
		return "", uuid.Nil
	}
	return p.Subject, p.TenantID
}

// resourceFromMessage extracts a `name`/`parent` field from the request, if
// present, via reflection on the public Get* method names. Avoids importing
// every proto package; falls back to the message type name.
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

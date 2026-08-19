// Package audith implements the admin AuditLogService — read access to the
// append-only audit log. Writes are produced by middleware on each
// admin/iam mutation.
package audith

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// exportRowCap bounds how many entries one ExportAuditLog call materialises.
// Above this limit the Operation is returned with `truncated=true` so the
// caller knows to narrow their filter or paginate manually. Tuned so a
// well-formed export fits in well under the 10 MiB Connect body cap.
const exportRowCap = 10_000

type Handler struct {
	repo   admindomain.AuditRepository
	cel    *celpkg.Evaluator
	policy cedar.Authorizer
}

func NewHandler(r admindomain.AuditRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("audith: policy authorizer is required")
	}
	return &Handler{repo: r, cel: celpkg.NewEvaluator(), policy: policy}
}

// authorize gates an audit-log RPC against Cedar. The Resource is a Tenant
// (audit lines are tenant-scoped); compliance roles can be granted
// cross-tenant read by writing a permit without the tenant_id match.
func (h *Handler) authorize(ctx context.Context, action string, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// ListAuditLog returns one page of audit entries. `filter` is an optional
// CEL expression evaluated against AuditLogSchema; rows that fail the
// predicate are dropped before the page is returned. The repo cursor is
// preserved as-is so the caller can paginate even when most rows in a
// page get filtered out.
func (h *Handler) ListAuditLog(ctx context.Context, args admindomain.ListAuditArgs, filter string) ([]admindomain.AuditEntry, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	// Non-platform admins can see only their own tenant's entries.
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		if args.ActorTenantID != uuid.Nil && args.ActorTenantID != caller {
			return nil, "", connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant audit denied"))
		}
		args.ActorTenantID = caller
	}
	if err := h.authorize(ctx, cedar.ActionReadAuditLog, args.ActorTenantID); err != nil {
		return nil, "", err
	}
	prog, err := h.cel.Compile(celpkg.AuditLogSchema, filter)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	// Best-effort SQL pushdown of recognized conjuncts. The full CEL
	// program still runs in-memory below; pushdown only narrows the
	// candidate set on the way out of Postgres.
	applyAuditPushdown(&args, filter)
	page, next, err := h.repo.List(ctx, args)
	if err != nil {
		return nil, "", err
	}
	if filter == "" {
		return page, next, nil
	}
	out := page[:0]
	for i := range page {
		match, err := celpkg.Match(prog, auditEntryRow(page[i]))
		if err != nil {
			return nil, "", connect.NewError(connect.CodeInternal, fmt.Errorf("filter eval: %w", err))
		}
		if match {
			out = append(out, page[i])
		}
	}
	return out, next, nil
}

// applyAuditPushdown extracts the SQL-expressible subset of the CEL
// filter and stamps the recognised predicates onto args. A parse
// failure is silently swallowed: the in-memory CEL eval will hit the
// same expression next and surface the error there. The full CEL
// program ALWAYS still runs after the SQL fetch — pushdown only
// narrows the candidate set, never replaces evaluation.
func applyAuditPushdown(args *admindomain.ListAuditArgs, filter string) {
	if filter == "" {
		return
	}
	pd, err := celpkg.ExtractAuditPushdown(filter)
	if err != nil {
		return
	}
	if args.ActorSubject == "" && pd.ActorSubjectEq != "" {
		args.ActorSubject = pd.ActorSubjectEq
	}
	if args.ActionEq == "" && pd.ActionEq != "" {
		args.ActionEq = pd.ActionEq
	}
	if args.ActionPrefix == "" && pd.ActionPrefix != "" {
		args.ActionPrefix = pd.ActionPrefix
	}
	if args.AtGTE.IsZero() && !pd.AtGTE.IsZero() {
		args.AtGTE = pd.AtGTE
	}
	if args.AtLTE.IsZero() && !pd.AtLTE.IsZero() {
		args.AtLTE = pd.AtLTE
	}
}

// auditEntryRow projects an AuditEntry into the map shape CEL expects.
// Kept private because the field names must match AuditLogSchema verbatim.
func auditEntryRow(e admindomain.AuditEntry) map[string]any {
	return map[string]any{
		"actor_subject":   e.ActorSubject,
		"actor_tenant_id": e.ActorTenantID.String(),
		"actor_audience":  e.ActorAudience,
		"action":          e.Action,
		"resource_name":   e.ResourceName,
		"request_id":      e.RequestID,
		"source_ip":       e.SourceIP,
		"at":              e.At,
		"is_error":        e.ErrorMessage != "",
	}
}

func (h *Handler) GetAuditLogEntry(ctx context.Context, id uuid.UUID) (*admindomain.AuditEntry, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	e, err := h.repo.Get(ctx, id)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && e.ActorTenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("audit entry not found"))
	}
	if err := h.authorize(ctx, cedar.ActionReadAuditLog, e.ActorTenantID); err != nil {
		return nil, err
	}
	return &e, nil
}

// ExportAuditLogResult is the materialised payload returned by ExportAuditLog.
// Embedded as JSON in Operation.response so the caller can consume it
// without a follow-up GetOperation roundtrip.
type ExportAuditLogResult struct {
	GeneratedAt time.Time             `json:"generated_at"`
	Filter      string                `json:"filter,omitempty"`
	Destination string                `json:"destination,omitempty"`
	RowCount    int                   `json:"row_count"`
	Truncated   bool                  `json:"truncated"`
	Entries     []ExportAuditLogEntry `json:"entries"`
}

// ExportAuditLogEntry is the export-shape of one AuditEntry. We project
// before/after JSON as raw messages so a downstream JQ pipeline doesn't
// have to re-decode escaped strings.
type ExportAuditLogEntry struct {
	EntryID       uuid.UUID       `json:"entry_id"`
	At            time.Time       `json:"at"`
	ActorSubject  string          `json:"actor_subject"`
	ActorTenantID uuid.UUID       `json:"actor_tenant_id,omitempty"`
	ActorAudience string          `json:"actor_audience"`
	Action        string          `json:"action"`
	ResourceName  string          `json:"resource_name"`
	RequestID     string          `json:"request_id,omitempty"`
	SourceIP      string          `json:"source_ip,omitempty"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
	ErrorMessage  string          `json:"error_message,omitempty"`
}

// ExportAuditLog materialises a synchronous JSON dump of audit entries
// matching the filter. Used by SOC-2 / ISO-27001 audit prep workflows that
// need a bounded, point-in-time snapshot. Streams are not used: a single
// roundtrip with up to `exportRowCap` rows keeps the protocol simple.
//
// `destination` is currently advisory — recorded in the result envelope
// for traceability but not acted upon. A future slice may persist the
// dump to a Paladin bucket and return a presigned URL instead.
func (h *Handler) ExportAuditLog(ctx context.Context, filter, destination string) (*ExportAuditLogResult, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args := admindomain.ListAuditArgs{PageSize: 1000}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		args.ActorTenantID = caller
	}
	if err := h.authorize(ctx, cedar.ActionExportAuditLog, args.ActorTenantID); err != nil {
		return nil, err
	}
	prog, err := h.cel.Compile(celpkg.AuditLogSchema, filter)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	applyAuditPushdown(&args, filter)

	out := &ExportAuditLogResult{
		GeneratedAt: time.Now().UTC(),
		Filter:      filter,
		Destination: destination,
		Entries:     make([]ExportAuditLogEntry, 0, 200),
	}
	for {
		page, next, err := h.repo.List(ctx, args)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("export: list page: %w", err))
		}
		for i := range page {
			if filter != "" {
				match, err := celpkg.Match(prog, auditEntryRow(page[i]))
				if err != nil {
					return nil, connect.NewError(connect.CodeInternal,
						fmt.Errorf("export: filter eval: %w", err))
				}
				if !match {
					continue
				}
			}
			out.Entries = append(out.Entries, projectExportEntry(page[i]))
			if len(out.Entries) >= exportRowCap {
				out.Truncated = true
				out.RowCount = len(out.Entries)
				return out, nil
			}
		}
		if next == "" {
			break
		}
		// Decode cursor into ListAuditArgs. The List adapter returns a
		// "{rfc3339nano}/{uuid}" cursor; reuse the same parser used by the
		// connectshim. Inline-decoded here to avoid an import cycle.
		args.AfterAt, args.AfterID = decodeCursor(next)
	}
	out.RowCount = len(out.Entries)
	return out, nil
}

func projectExportEntry(e admindomain.AuditEntry) ExportAuditLogEntry {
	return ExportAuditLogEntry{
		EntryID:       e.EntryID,
		At:            e.At.UTC(),
		ActorSubject:  e.ActorSubject,
		ActorTenantID: e.ActorTenantID,
		ActorAudience: e.ActorAudience,
		Action:        e.Action,
		ResourceName:  e.ResourceName,
		RequestID:     e.RequestID,
		SourceIP:      e.SourceIP,
		Before:        json.RawMessage(e.BeforeJSON),
		After:         json.RawMessage(e.AfterJSON),
		ErrorMessage:  e.ErrorMessage,
	}
}

// decodeCursor mirrors adapters.decodeAuditCursor for the export loop.
// Liberal on parse failure — returns zero time + Nil UUID so the next
// List call starts from the top (idempotent re-export rather than an
// abrupt fail).
func decodeCursor(tok string) (time.Time, uuid.UUID) {
	for i := len(tok) - 1; i >= 0; i-- {
		if tok[i] == '/' {
			at, err := time.Parse(time.RFC3339Nano, tok[:i])
			if err != nil {
				return time.Time{}, uuid.Nil
			}
			id, err := uuid.Parse(tok[i+1:])
			if err != nil {
				return time.Time{}, uuid.Nil
			}
			return at, id
		}
	}
	return time.Time{}, uuid.Nil
}

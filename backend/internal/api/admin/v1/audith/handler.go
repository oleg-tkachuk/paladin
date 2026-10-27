// Package audith implements the admin AuditLogService — read access to the
// append-only audit log. Writes are produced by middleware on each
// admin/iam mutation.
package audith

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"cel.dev/cel-go/cel"

	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
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
func (h *Handler) authorize(ctx context.Context, action cedar.Action, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, "denied by policy")
	}
	return nil
}

// maxFilteredBatches bounds how many store pages one ListAuditLog call reads
// to fill a page whose filter the store could not apply. It caps the work a
// filter that matches nothing can cause, at the cost of a short page with a
// cursor once it is spent.
const maxFilteredBatches = 10

// ListAuditLog returns one page of audit entries. `filter` is an optional
// CEL expression evaluated against AuditLogSchema; rows that fail the
// predicate are dropped, and further store pages are read to fill the page
// (up to maxFilteredBatches).
// listScope is args as a listing of the log may read them, with filter's
// compiled program: a caller other than a platform admin held to its own
// tenant, the policy asked, and the filter's SQL-expressible conjuncts
// pushed down — refused when the caller may not list them.
func (h *Handler) listScope(ctx context.Context, args admindomain.ListAuditArgs, filter string) (admindomain.ListAuditArgs, cel.Program, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return args, nil, err
	}
	if args.Order, err = auditOrder(args.Order); err != nil {
		return args, nil, err
	}
	// Non-platform admins can see only their own tenant's entries.
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		if args.ActorTenantID != uuid.Nil() && args.ActorTenantID != caller {
			return args, nil, connect.NewError(connect.CodePermissionDenied,
				"cross-tenant audit denied")
		}
		if args.TrailTenantID != uuid.Nil() && args.TrailTenantID != caller {
			return args, nil, connect.NewError(connect.CodePermissionDenied,
				"cross-tenant audit denied")
		}
		args.ActorTenantID = caller
	}
	if err := h.authorize(ctx, cedar.ActionReadAuditLog, args.ActorTenantID); err != nil {
		return args, nil, err
	}
	prog, err := h.cel.Compile(celpkg.AuditLogSchema, filter)
	if err != nil {
		return args, nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	// Best-effort SQL pushdown of recognized conjuncts. The full CEL
	// program still runs in-memory after the fetch; pushdown only narrows
	// the candidate set on the way out of Postgres.
	applyAuditPushdown(&args, filter)
	return args, prog, nil
}

// CountAuditFacets counts the entries ListAuditLog would list for args and
// filter by each facet's answers (AuditFacetPlane, AuditFacetOutcome,
// AuditFacetService), each facet without its own choices. Counted over what
// the filter pushes down: the conjuncts it cannot are applied to each page
// after, so a count can exceed the entries the pages then hold.
func (h *Handler) CountAuditFacets(ctx context.Context, args admindomain.ListAuditArgs, filter string) ([]admindomain.AuditFacetCount, error) {
	args, _, err := h.listScope(ctx, args, filter)
	if err != nil {
		return nil, err
	}
	counts, err := h.repo.CountFacets(ctx, args)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("count audit facets: %w", err))
	}
	return counts, nil
}

func (h *Handler) ListAuditLog(ctx context.Context, args admindomain.ListAuditArgs, filter string) ([]admindomain.AuditEntry, string, error) {
	args, prog, err := h.listScope(ctx, args, filter)
	if err != nil {
		return nil, "", err
	}
	page, next, err := h.listPage(ctx, args)
	if err != nil {
		return nil, "", err
	}
	if filter == "" {
		return page, next, nil
	}
	// Part of the filter ran after the fetch, so the batch may have kept
	// few of its rows or none — a disjunction, which pushdown does not
	// reach, keeps whatever happens to match among the newest rows of the
	// whole log. Read on until the page is full, the log ends, or the scan
	// bound is spent; past the bound the caller gets what was found and a
	// cursor to go on from.
	//
	// A batch that came with a cursor was full, so its length is the page
	// size the store applied — whatever args.PageSize asked for.
	want := len(page)
	var out []admindomain.AuditEntry
	for batches := 1; ; batches++ {
		for i := range page {
			match, err := celpkg.Match(prog, auditEntryRow(page[i]))
			if err != nil {
				return nil, "", rpcerr.New(connect.CodeInternal, fmt.Errorf("filter eval: %w", err))
			}
			if !match {
				continue
			}
			out = append(out, page[i])
			if len(out) == want {
				// Full mid-batch: resume after the last row returned, not
				// after the batch, or the rest of it would be skipped.
				if i < len(page)-1 || next != "" {
					return out, admindomain.AuditCursor(args.Order, page[i]), nil
				}
				return out, "", nil
			}
		}
		if next == "" || batches == maxFilteredBatches {
			return out, next, nil
		}
		args.PageToken = next
		if page, next, err = h.listPage(ctx, args); err != nil {
			return nil, "", err
		}
	}
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
		"relayed_by":      e.RelayedBy,
		"user_agent":      e.UserAgent,
		"at":              e.At,
		"is_error":        e.ErrorMessage != "",
		"search": celpkg.SearchText(e.Action, e.ActorSubject, e.ActorAudience,
			e.ResourceName, e.RequestID, e.SourceIP, e.ErrorMessage, e.RelayedBy, e.UserAgent),
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
		return nil, connect.NewError(connect.CodeNotFound, "audit entry not found")
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
	RelayedBy     string          `json:"relayed_by,omitempty"`
	UserAgent     string          `json:"user_agent,omitempty"`
	Request       json.RawMessage `json:"request,omitempty"`
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
	args := admindomain.ListAuditArgs{PageSize: 1000, Order: admindomain.DefaultAuditOrder}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		args.ActorTenantID = caller
	}
	if err := h.authorize(ctx, cedar.ActionExportAuditLog, args.ActorTenantID); err != nil {
		return nil, err
	}
	prog, err := h.cel.Compile(celpkg.AuditLogSchema, filter)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
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
			return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("export: list page: %w", err))
		}
		for i := range page {
			if filter != "" {
				match, err := celpkg.Match(prog, auditEntryRow(page[i]))
				if err != nil {
					return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("export: filter eval: %w", err))
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
		args.PageToken = next
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
		RelayedBy:     e.RelayedBy,
		UserAgent:     e.UserAgent,
		Request:       json.RawMessage(e.RequestJSON),
		ErrorMessage:  e.ErrorMessage,
	}
}

// auditOrder is the sort a request names, or InvalidArgument naming the
// fields it may name instead. An empty one is newest first.
func auditOrder(o keyset.Order) (keyset.Order, error) {
	if o.Field == "" {
		return admindomain.DefaultAuditOrder, nil
	}
	if !slices.Contains(admindomain.AuditOrderFields, o.Field) {
		return keyset.Order{}, connect.NewError(connect.CodeInvalidArgument,
			fmt.Sprintf("order_by %q: the audit log sorts by one of %s", o.Field, strings.Join(admindomain.AuditOrderFields, ", ")))
	}
	return o, nil
}

// listPage reads one store page; a page token the store cannot resume is
// the caller's to fix, not an internal error.
func (h *Handler) listPage(ctx context.Context, args admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	page, next, err := h.repo.List(ctx, args)
	if errors.Is(err, keyset.ErrMalformed) || errors.Is(err, keyset.ErrOrderChanged) {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, "page_token: "+err.Error()).WithCause(err)
	}
	return page, next, err
}

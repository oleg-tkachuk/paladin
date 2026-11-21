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
	"go.uber.org/zap"

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

// exportPageSize is how many entries an export reads from the store at a
// time.
const exportPageSize = 1000

// exportInitialEntries is the room an export starts with: enough for a
// typical narrow export without growing, far below exportRowCap.
const exportInitialEntries = 200

// msgCrossTenantAudit refuses a caller other than a platform admin naming
// another tenant's actors or trail.
const msgCrossTenantAudit = "cross-tenant audit denied"

// msgEntryNotFound answers an entry that is not there, or not the caller's.
const msgEntryNotFound = "audit entry not found"

// The operations a log line names (its "op" field).
const (
	opList        = "list"
	opCountFacets = "count_facets"
	opGet         = "get"
	opExport      = "export"
)

type Handler struct {
	repo   admindomain.AuditRepository
	cel    *celpkg.Evaluator
	policy cedar.Authorizer
	log    *zap.Logger
}

func NewHandler(r admindomain.AuditRepository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("audith: policy authorizer is required")
	}
	return &Handler{repo: r, cel: celpkg.NewEvaluator(), policy: policy, log: zap.NewNop()}
}

// SetLogger sets where the handler says how each call ended. Unset, it says
// nothing.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// answer logs err from op by what it is — a server fault at error, a
// refusal the caller can act on at warn — and returns it mapped for the
// caller.
func (h *Handler) answer(op string, err error, fields ...zap.Field) error {
	mapped := apiutil.MapError(err)
	code := connect.CodeOf(mapped)
	fields = append(fields, zap.String("op", op), zap.String("code", code.String()), zap.Error(err))
	if apiutil.ServerFault(code) {
		h.log.Error("audit log read failed", fields...)
	} else {
		h.log.Warn("audit log read refused", fields...)
	}
	return mapped
}

// internal answers a store failure on op as internal, and logs it.
func (h *Handler) internal(op string, err error, fields ...zap.Field) error {
	return h.answer(op, rpcerr.New(connect.CodeInternal, err), fields...)
}

// authorize gates an audit-log RPC against Cedar. The Resource is a Tenant
// (audit lines are tenant-scoped); compliance roles can be granted
// cross-tenant read by writing a permit without the tenant_id match.
func (h *Handler) authorize(ctx context.Context, action cedar.Action, tenantID uuid.UUID) error {
	_, err := apiutil.Authorize(ctx, h.policy, action, &cedar.Resource{TenantID: tenantID})
	return err
}

// maxFilteredBatches bounds how many store pages one ListAuditLog call reads
// to fill a page whose filter the store could not apply. It caps the work a
// filter that matches nothing can cause, at the cost of a short page with a
// cursor once it is spent.
const maxFilteredBatches = 10

// crossTenant refuses args naming actors or a trail of a tenant other than
// caller's.
func crossTenant(args admindomain.ListAuditArgs, caller uuid.UUID) error {
	for _, named := range []uuid.UUID{args.ActorTenantID, args.TrailTenantID} {
		if named != uuid.Nil() && named != caller {
			return connect.NewError(connect.CodePermissionDenied, msgCrossTenantAudit)
		}
	}
	return nil
}

// listScope is args as a listing of the log may read them, with filter's
// compiled program: a caller other than a platform admin held to its own
// tenant, the policy asked, and the filter's SQL-expressible conjuncts
// pushed down — refused when the caller may not list them.
func (h *Handler) listScope(ctx context.Context, op string, args admindomain.ListAuditArgs, filter string) (admindomain.ListAuditArgs, cel.Program, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return args, nil, err
	}
	if args.Order, err = auditOrder(args.Order); err != nil {
		return args, nil, err
	}
	// Non-platform admins can see only their own tenant's entries.
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		if err := crossTenant(args, caller); err != nil {
			return args, nil, h.answer(op, err, zap.String("tenant_id", caller.String()),
				zap.String("actor_tenant_id", args.ActorTenantID.String()),
				zap.String("trail_tenant_id", args.TrailTenantID.String()))
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
	args, _, err := h.listScope(ctx, opCountFacets, args, filter)
	if err != nil {
		return nil, err
	}
	counts, err := h.repo.CountFacets(ctx, args)
	if err != nil {
		return nil, h.internal(opCountFacets, fmt.Errorf("count audit facets: %w", err), scopeFields(args)...)
	}
	return counts, nil
}

// scopeFields names the tenants a listing reads in a log line.
func scopeFields(args admindomain.ListAuditArgs) []zap.Field {
	return []zap.Field{
		zap.String("actor_tenant_id", args.ActorTenantID.String()),
		zap.String("trail_tenant_id", args.TrailTenantID.String()),
	}
}

// ListAuditLog returns one page of audit entries. `filter` is an optional
// CEL expression evaluated against AuditLogSchema; rows that fail the
// predicate are dropped, and further store pages are read to fill the page
// (up to maxFilteredBatches).
func (h *Handler) ListAuditLog(ctx context.Context, args admindomain.ListAuditArgs, filter string) ([]admindomain.AuditEntry, string, error) {
	args, prog, err := h.listScope(ctx, opList, args, filter)
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
	return h.fillFiltered(ctx, args, prog, page, next)
}

// fillFiltered keeps the entries of page, and of the pages after it, that
// prog matches, until it holds as many as page did.
//
// Part of the filter ran after the fetch, so the batch may have kept few of
// its rows or none — a disjunction, which pushdown does not reach, keeps
// whatever happens to match among the newest rows of the whole log. Read on
// until the page is full, the log ends, or the scan bound is spent; past the
// bound the caller gets what was found and a cursor to go on from.
//
// A batch that came with a cursor was full, so its length is the page size
// the store applied — whatever args.PageSize asked for.
func (h *Handler) fillFiltered(ctx context.Context, args admindomain.ListAuditArgs, prog cel.Program, page []admindomain.AuditEntry, next string) ([]admindomain.AuditEntry, string, error) {
	want := len(page)
	var out []admindomain.AuditEntry
	for batches := 1; ; batches++ {
		for i := range page {
			match, err := celpkg.Match(prog, auditEntryRow(page[i]))
			if err != nil {
				return nil, "", h.internal(opList, fmt.Errorf("filter eval: %w", err), scopeFields(args)...)
			}
			if !match {
				continue
			}
			out = append(out, page[i])
			if len(out) == want {
				return out, resumeAfter(args.Order, page, i, next), nil
			}
		}
		if next == "" || batches == maxFilteredBatches {
			return out, next, nil
		}
		args.PageToken = next
		var err error
		if page, next, err = h.listPage(ctx, args); err != nil {
			return nil, "", err
		}
	}
}

// resumeAfter is the cursor of a page that filled at page[i]: after that row,
// not after the batch, or the rest of it would be skipped — and none when the
// batch was the log's last row.
func resumeAfter(order keyset.Order, page []admindomain.AuditEntry, i int, next string) string {
	if i < len(page)-1 || next != "" {
		return admindomain.AuditCursor(order, page[i])
	}
	return ""
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
	fillOpen(&args.ActorSubject, pd.ActorSubjectEq)
	fillOpen(&args.ActionEq, pd.ActionEq)
	fillOpen(&args.ActionPrefix, pd.ActionPrefix)
	fillOpenTime(&args.AtGTE, pd.AtGTE)
	fillOpenTime(&args.AtLTE, pd.AtLTE)
}

// fillOpen sets *dst to v when the request left it open.
func fillOpen(dst *string, v string) {
	if *dst == "" {
		*dst = v
	}
}

// fillOpenTime sets *dst to v when the request left it open.
func fillOpenTime(dst *time.Time, v time.Time) {
	if dst.IsZero() {
		*dst = v
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
		return nil, h.answer(opGet, err, zap.String("entry_id", id.String()))
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && e.ActorTenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, msgEntryNotFound)
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
	caller, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args := admindomain.ListAuditArgs{PageSize: exportPageSize, Order: admindomain.DefaultAuditOrder}
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
		Entries:     make([]ExportAuditLogEntry, 0, exportInitialEntries),
	}
	if err := h.collectExport(ctx, args, filter, prog, out); err != nil {
		return nil, err
	}
	out.RowCount = len(out.Entries)
	h.log.Info("audit log exported",
		zap.String("op", opExport),
		zap.String("actor_tenant_id", args.ActorTenantID.String()),
		zap.String("actor", principalSubject(p)),
		zap.Int("rows", out.RowCount),
		zap.Bool("truncated", out.Truncated))
	return out, nil
}

// principalSubject is p's subject, "" for none.
func principalSubject(p *auth.Principal) string {
	if p == nil {
		return ""
	}
	return p.Subject
}

// collectExport reads args page by page into out, keeping the entries prog
// matches (all of them with no filter), until the log ends or out holds
// exportRowCap entries, which marks it truncated.
func (h *Handler) collectExport(ctx context.Context, args admindomain.ListAuditArgs, filter string, prog cel.Program, out *ExportAuditLogResult) error {
	for {
		page, next, err := h.repo.List(ctx, args)
		if err != nil {
			return h.internal(opExport, fmt.Errorf("export: list page: %w", err), scopeFields(args)...)
		}
		for i := range page {
			keep, err := exportMatch(filter, prog, page[i])
			if err != nil {
				return h.internal(opExport, fmt.Errorf("export: filter eval: %w", err), scopeFields(args)...)
			}
			if !keep {
				continue
			}
			out.Entries = append(out.Entries, projectExportEntry(page[i]))
			if len(out.Entries) >= exportRowCap {
				out.Truncated = true
				return nil
			}
		}
		if next == "" {
			return nil
		}
		args.PageToken = next
	}
}

// exportMatch reports whether an export keeps e: every entry with no filter.
func exportMatch(filter string, prog cel.Program, e admindomain.AuditEntry) (bool, error) {
	if filter == "" {
		return true, nil
	}
	return celpkg.Match(prog, auditEntryRow(e))
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
// the caller's to fix, any other failure the server's.
func (h *Handler) listPage(ctx context.Context, args admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	page, next, err := h.repo.List(ctx, args)
	switch {
	case errors.Is(err, keyset.ErrMalformed) || errors.Is(err, keyset.ErrOrderChanged):
		return nil, "", connect.NewError(connect.CodeInvalidArgument, "page_token: "+err.Error()).WithCause(err)
	case err != nil:
		return nil, "", h.internal(opList, err, scopeFields(args)...)
	}
	return page, next, nil
}

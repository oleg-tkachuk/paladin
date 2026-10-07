// Package systemh implements the admin/v1 SystemService — operator
// diagnostics that show the running configuration with secrets
// redacted. Distinct from iam/v1.SystemService (open to any
// authenticated caller) because the YAML may shape internal
// architecture an attacker shouldn't see.
package systemh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// Handler holds an immutable reference to the loaded config plus the
// on-disk path it came from. Both are set once during process boot —
// SIGHUP-style reload is out of scope, since reloading the config
// safely requires reloading dependent subsystems too.
type Handler struct {
	cfg        config.Config
	sourcePath string
	// pool serves the control-plane half of PlatformStats. Nil-safe:
	// a handler built without one reports zeroed inventory rather than
	// panicking (the config-only unit tests construct it that way).
	pool *pgxpool.Pool
	// httpClient fetches the dispatcher / worker pods' ops endpoints for
	// DispatcherStats and PlatformStats. Short timeout — dashboard reads.
	httpClient *http.Client
}

// New wires the handler with the live config + the on-disk path it
// was loaded from. Pass an empty path when the process bootstrapped
// from env / flags only — the response renders "—" in that case.
func New(cfg config.Config, sourcePath string) *Handler {
	return &Handler{
		cfg:        cfg,
		sourcePath: sourcePath,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// WithPool attaches the admin pod's request pool, enabling the
// control-plane half of PlatformStats. Chained at wiring time so the
// existing New signature (config-only) keeps working for the paths that
// never touch the DB.
func (h *Handler) WithPool(pool *pgxpool.Pool) *Handler {
	h.pool = pool
	return h
}

// MarshalRedacted produces the wire payload: the running config as
// YAML with secrets replaced by Config.Obfuscated()'s "***" sentinels,
// alongside the on-disk path the binary loaded it from.
//
// Gated to platform.admin — this method is the only entry-point the
// connectshim uses, so the role check lives here so the connectshim-
// gating coverage test sees it (rather than tucked into the shim).
func (h *Handler) MarshalRedacted(ctx context.Context) (yamlBlob string, sourcePath string, err error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return "", "", connect.NewError(connect.CodePermissionDenied, rerr.Error()).WithCause(rerr)
	}
	redacted := h.cfg.Obfuscated()
	out, err := yaml.Marshal(&redacted)
	if err != nil {
		return "", "", rpcerr.New(connect.CodeInternal, fmt.Errorf("marshal config: %w", err))
	}
	return string(out), h.sourcePath, nil
}

// DispatcherStats proxies the dispatcher pod's operator view
// (<dispatcher.ops_url>/system/dispatcher-stats.json). The stats are
// computed THERE because event_deliveries is RLS'd per tenant and only the
// dispatcher's BYPASSRLS pool sees the cross-tenant whole; the admin plane
// contributes exactly one thing — this platform.admin gate.
//
// available=false (with a nil stats pointer) when the ops URL is
// unconfigured or unreachable: an operator console renders "stats
// unavailable", it doesn't error — same graceful-degrade contract as
// mcpinspecth.ListSessions.
func (h *Handler) DispatcherStats(ctx context.Context) (stats *worker.DeliveryStats, available bool, err error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return nil, false, connect.NewError(connect.CodePermissionDenied, rerr.Error()).WithCause(rerr)
	}
	url := h.cfg.Dispatcher.OpsURL
	if url == "" {
		return nil, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/system/dispatcher-stats.json", nil)
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	res, err := h.httpClient.Do(req)
	if err != nil {
		// Unreachable dispatcher = degraded view, not an RPC failure.
		return nil, false, nil
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, false, nil
	}
	var s worker.DeliveryStats
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		return nil, false, rpcerr.New(connect.CodeInternal, fmt.Errorf("decode dispatcher stats: %w", err))
	}
	return &s, true, nil
}

// ─── Platform stats ─────────────────────────────────────────────────────────

// PlatformStatsResult is the handler's return shape: the control-plane
// census this pod computes itself, plus the RLS'd census proxied from the
// worker. RLS is nil (and RLSAvailable false) whenever the worker leg is
// unconfigured or unreachable.
type PlatformStatsResult struct {
	ControlPlane *platformstats.ControlPlane
	RLS          *platformstats.RLSCensus
	RLSAvailable bool
	// TenantNames maps tenant_id → (slug, display name) for the tenants
	// the object census mentions. The worker only knows UUIDs; this pod
	// can read `tenants`, so the join happens here rather than shipping
	// tenant metadata over the ops endpoint.
	TenantNames map[string]TenantName
}

// TenantName is the display pair the console needs to label a census row.
type TenantName struct {
	Slug        string
	DisplayName string
}

// PlatformStats assembles the console's /stats payload.
//
// Two legs, deliberately independent: the control-plane census is a
// handful of aggregates on this pod's pool over the un-RLS'd inventory
// tables, and the RLS'd census (objects, quotas, capabilities, API
// tokens, subscriptions) is an HTTP GET against the worker's ops listener
// (see platformstats' package doc for why the split exists). A dead
// worker degrades that second leg to available=false; it does not fail
// the RPC, so an operator still sees the fleet inventory.
func (h *Handler) PlatformStats(ctx context.Context, page platformstats.TenantPage) (*PlatformStatsResult, error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, rerr.Error()).WithCause(rerr)
	}
	out := &PlatformStatsResult{TenantNames: map[string]TenantName{}}

	if h.pool != nil {
		// The census is a cross-tenant aggregate by definition, and its
		// tenants query joins tenant_default_bindings, which is RLS-covered
		// as of 016. Without the flag the subquery would see only the
		// caller's binding and every OTHER tenant would be counted as having
		// none — a wrong number on a page, with no error anywhere.
		cp, err := platformstats.CollectControlPlane(auth.WithCrossTenantRead(ctx), h.pool)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		out.ControlPlane = cp
	}

	census, ok := h.fetchRLSCensus(ctx, page)
	if !ok {
		return out, nil
	}
	out.RLS, out.RLSAvailable = census, true

	// Label the object-census rows. One query over `tenants` (un-RLS'd)
	// covering exactly the ids the worker reported — cheaper and more
	// predictable than a per-row lookup, and it tolerates ids whose tenant
	// row was purged while objects lingered (those stay unlabelled).
	if h.pool != nil && len(census.Objects.Tenants) > 0 {
		ids := make([]string, 0, len(census.Objects.Tenants))
		for _, t := range census.Objects.Tenants {
			ids = append(ids, t.TenantID)
		}
		names, err := h.tenantNames(ctx, ids)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		out.TenantNames = names
	}
	return out, nil
}

// fetchRLSCensus GETs the worker's ops endpoint for one page of the
// per-tenant breakdown. Returns ok=false for every degraded case (no URL
// configured, dial failure, non-200, bad body) — the caller turns that into
// available=false. A worker older than the paging parameters answers with
// its first page and no next token, which reads as the last page.
func (h *Handler) fetchRLSCensus(ctx context.Context, page platformstats.TenantPage) (*platformstats.RLSCensus, bool) {
	url := h.cfg.Worker.OpsURL
	if url == "" {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/system/rls-census.json?"+page.Query().Encode(), nil)
	if err != nil {
		return nil, false
	}
	res, err := h.httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, false
	}
	var census platformstats.RLSCensus
	if err := json.NewDecoder(res.Body).Decode(&census); err != nil {
		return nil, false
	}
	return &census, true
}

// SignalTenantsResult is one page of the tenants behind a flagged census
// count, labelled the way PlatformStatsResult labels the object table.
type SignalTenantsResult struct {
	Tenants     *platformstats.SignalTenants
	TenantNames map[string]TenantName
}

// PlatformStatsTenants answers "whose" for one of the census counts the
// console flags. Unlike PlatformStats it has no degraded answer: it is only
// asked for once the census has shown the count, so a worker that cannot
// answer is Unavailable rather than an empty list that reads as "nobody".
func (h *Handler) PlatformStatsTenants(ctx context.Context, signal platformstats.Signal, page platformstats.TenantPage) (*SignalTenantsResult, error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, rerr.Error()).WithCause(rerr)
	}
	base := h.cfg.Worker.OpsURL
	if base == "" {
		return nil, connect.NewError(connect.CodeUnavailable, "worker ops endpoint is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+platformstats.SignalTenantsPath+"?"+platformstats.SignalQuery(signal, page).Encode(), nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	res, err := h.httpClient.Do(req)
	if err != nil {
		return nil, rpcerr.New(connect.CodeUnavailable, fmt.Errorf("worker census: %w", err))
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, connect.Errorf(connect.CodeUnavailable, "worker census: %s", res.Status)
	}
	var tenants platformstats.SignalTenants
	if err := json.NewDecoder(res.Body).Decode(&tenants); err != nil {
		return nil, rpcerr.New(connect.CodeUnavailable, fmt.Errorf("worker census: %w", err))
	}

	out := &SignalTenantsResult{Tenants: &tenants, TenantNames: map[string]TenantName{}}
	if h.pool != nil && len(tenants.Tenants) > 0 {
		ids := make([]string, 0, len(tenants.Tenants))
		for _, t := range tenants.Tenants {
			ids = append(ids, t.TenantID)
		}
		names, err := h.tenantNames(ctx, ids)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		out.TenantNames = names
	}
	return out, nil
}

// tenantNames resolves slug + display name for the supplied tenant ids.
// Trashed tenants are included: their objects still occupy storage and an
// operator chasing usage needs to see whose they are.
func (h *Handler) tenantNames(ctx context.Context, ids []string) (map[string]TenantName, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id::text, COALESCE(slug, ''), COALESCE(display_name, '')
		FROM tenants
		WHERE id::text = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("tenant names: %w", err)
	}
	defer rows.Close()
	out := make(map[string]TenantName, len(ids))
	for rows.Next() {
		var id string
		var n TenantName
		if err := rows.Scan(&id, &n.Slug, &n.DisplayName); err != nil {
			return nil, fmt.Errorf("tenant names: scan: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant names: rows: %w", err)
	}
	return out, nil
}

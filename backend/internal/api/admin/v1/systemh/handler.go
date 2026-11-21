// Package systemh implements the admin/v1 SystemService — operator
// diagnostics that show the running configuration with secrets
// redacted. Distinct from iam/v1.SystemService (open to any
// authenticated caller) because the YAML may shape internal
// architecture an attacker shouldn't see.
package systemh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
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
	// inv serves the control-plane half of PlatformStats and the tenant
	// labels. Nil-safe: a handler built without one reports no inventory
	// and no labels (the config-only unit tests construct it that way).
	inv Inventory
	// httpClient fetches the dispatcher / worker pods' ops endpoints for
	// DispatcherStats and PlatformStats. Short timeout — dashboard reads.
	httpClient *http.Client
	// log records what PlatformHealth could not read.
	log *zap.Logger
	// healthClient reads the roles' snapshots: httpClient unless
	// admin.health.ca_path asks for one trusting the internal CA.
	healthClient *http.Client
}

// New wires the handler with the live config + the on-disk path it
// was loaded from. Pass an empty path when the process bootstrapped
// from env / flags only — the response renders "—" in that case.
func New(cfg config.Config, sourcePath string) *Handler {
	return &Handler{
		cfg:        cfg,
		sourcePath: sourcePath,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		log:        zap.NewNop(),
	}
}

// WithHealthClient sets the client PlatformHealth reads the roles with.
func (h *Handler) WithHealthClient(c *http.Client) *Handler {
	h.healthClient = c
	return h
}

// WithLogger sets the logger PlatformHealth reports unreachable roles to.
func (h *Handler) WithLogger(l *zap.Logger) *Handler {
	h.log = l
	return h
}

// Inventory is what the admin pod reads itself for the stats page: the
// control-plane census over the un-RLS'd inventory tables, and the slug and
// display name of tenants the worker's census names only by id.
type Inventory interface {
	ControlPlane(ctx context.Context) (*platformstats.ControlPlane, error)
	// TenantNames labels the tenants in ids, trashed ones included: their
	// objects still occupy storage. An id with no tenant row is left out.
	TenantNames(ctx context.Context, ids []string) (map[string]TenantName, error)
}

// WithInventory attaches the admin pod's own reads, enabling the
// control-plane half of PlatformStats and the tenant labels. Chained at
// wiring time so the config-only New keeps working for the paths that never
// touch the DB.
func (h *Handler) WithInventory(inv Inventory) *Handler {
	h.inv = inv
	return h
}

// MarshalRedacted produces the wire payload: the running config as
// YAML with secrets replaced by Config.Obfuscated()'s "***" sentinels,
// alongside the on-disk path the binary loaded it from.
//
// Gated to platform.admin — this method is the only entry-point the
// connectshim uses, so the role check lives here so the connectshim-
// gating coverage test sees it (rather than tucked into the shim).
func (h *Handler) MarshalRedacted(ctx context.Context) (yamlBlob, defaultsYAML, sourcePath string, err error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return "", "", "", connect.NewError(connect.CodePermissionDenied, rerr.Error()).WithCause(rerr)
	}
	running, err := marshalObfuscated(&h.cfg)
	if err != nil {
		return "", "", "", rpcerr.New(connect.CodeInternal, fmt.Errorf("marshal config: %w", err))
	}
	defaults, err := config.Defaults()
	if err != nil {
		return "", "", "", rpcerr.New(connect.CodeInternal, fmt.Errorf("config defaults: %w", err))
	}
	defaultsBlob, err := marshalObfuscated(&defaults)
	if err != nil {
		return "", "", "", rpcerr.New(connect.CodeInternal, fmt.Errorf("marshal config defaults: %w", err))
	}
	return running, defaultsBlob, h.sourcePath, nil
}

// marshalObfuscated is cfg as YAML with every secret replaced, the shape
// both the running configuration and its defaults are served in.
func marshalObfuscated(cfg *config.Config) (string, error) {
	redacted := cfg.Obfuscated()
	out, err := yaml.Marshal(&redacted)
	return string(out), err
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
	var s worker.DeliveryStats
	err = h.getOps(ctx, h.cfg.Dispatcher.OpsURL, worker.DispatcherStatsPath, &s)
	switch {
	case err == nil:
		return &s, true, nil
	case errors.Is(err, errOpsBody):
		// The dispatcher answered, and not with stats: a fault to report,
		// not a view to degrade.
		h.log.Error("dispatcher stats: unreadable answer",
			zap.String("endpoint", worker.DispatcherStatsPath), zap.Error(err))
		return nil, false, rpcerr.New(connect.CodeInternal, fmt.Errorf("decode dispatcher stats: %w", err))
	default:
		// Unconfigured or unreachable dispatcher = degraded view, not an RPC
		// failure.
		h.logDegraded("dispatcher stats unavailable", worker.DispatcherStatsPath, err)
		return nil, false, nil
	}
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

	if h.inv != nil {
		// The census is a cross-tenant aggregate by definition, and its
		// tenants query joins tenant_default_bindings, which is RLS-covered
		// as of 016. Without the flag the subquery would see only the
		// caller's binding and every OTHER tenant would be counted as having
		// none — a wrong number on a page, with no error anywhere.
		cp, err := h.inv.ControlPlane(auth.WithCrossTenantRead(ctx))
		if err != nil {
			h.log.Error("platform stats: control plane census failed", zap.Error(err))
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
	ids := make([]string, 0, len(census.Objects.Tenants))
	for _, t := range census.Objects.Tenants {
		ids = append(ids, t.TenantID)
	}
	names, err := h.labelTenants(ctx, ids)
	if err != nil {
		return nil, err
	}
	out.TenantNames = names
	return out, nil
}

// fetchRLSCensus GETs the worker's ops endpoint for one page of the
// per-tenant breakdown. Returns ok=false for every degraded case (no URL
// configured, dial failure, non-200, bad body) — the caller turns that into
// available=false. A worker older than the paging parameters answers with
// its first page and no next token, which reads as the last page.
func (h *Handler) fetchRLSCensus(ctx context.Context, page platformstats.TenantPage) (*platformstats.RLSCensus, bool) {
	var census platformstats.RLSCensus
	if err := h.getOps(ctx, h.cfg.Worker.OpsURL, platformstats.RLSCensusPath+"?"+page.Query().Encode(), &census); err != nil {
		h.logDegraded("platform stats: worker census unavailable", platformstats.RLSCensusPath, err)
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

// msgCensusUnavailable is what a caller is told when the worker's census
// could not be read. The worker's address and its HTTP client's words stay the
// cause, which the RPC log line carries.
const msgCensusUnavailable = "the worker's census could not be read; retry"

// PlatformStatsTenants answers "whose" for one of the census counts the
// console flags. Unlike PlatformStats it has no degraded answer: it is only
// asked for once the census has shown the count, so a worker that cannot
// answer is Unavailable rather than an empty list that reads as "nobody".
func (h *Handler) PlatformStatsTenants(ctx context.Context, signal platformstats.Signal, page platformstats.TenantPage) (*SignalTenantsResult, error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, rerr.Error()).WithCause(rerr)
	}
	var tenants platformstats.SignalTenants
	if err := h.getOps(ctx, h.cfg.Worker.OpsURL,
		platformstats.SignalTenantsPath+"?"+platformstats.SignalQuery(signal, page).Encode(), &tenants); err != nil {
		h.log.Warn("platform stats: signal tenants unavailable",
			zap.String("endpoint", platformstats.SignalTenantsPath), zap.String("signal", string(signal)), zap.Error(err))
		return nil, connect.NewError(connect.CodeUnavailable, msgCensusUnavailable).WithCause(fmt.Errorf("worker census: %w", err))
	}

	ids := make([]string, 0, len(tenants.Tenants))
	for _, t := range tenants.Tenants {
		ids = append(ids, t.TenantID)
	}
	names, err := h.labelTenants(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &SignalTenantsResult{Tenants: &tenants, TenantNames: names}, nil
}

// labelTenants is the slug and display name of each tenant in ids, read
// from the inventory; none without one, or without ids.
func (h *Handler) labelTenants(ctx context.Context, ids []string) (map[string]TenantName, error) {
	if h.inv == nil || len(ids) == 0 {
		return map[string]TenantName{}, nil
	}
	names, err := h.inv.TenantNames(ctx, ids)
	if err != nil {
		h.log.Error("platform stats: tenant labels failed", zap.Int("tenants", len(ids)), zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return names, nil
}

// errOpsUnconfigured is getOps's answer when the deployment set no ops URL
// for the pod: a choice, not a fault.
var errOpsUnconfigured = errors.New("ops endpoint is not configured")

// errOpsBody wraps getOps's failure to decode an answer the pod gave.
var errOpsBody = errors.New("ops endpoint answer unreadable")

// getOps GETs path (with its query) from the ops listener at base and decodes
// the JSON answer into out. The error says which step failed:
// errOpsUnconfigured with no base, errOpsBody for an answer it cannot read.
func (h *Handler) getOps(ctx context.Context, base, path string, out any) error {
	if base == "" {
		return errOpsUnconfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	res, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reach ops endpoint: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("ops endpoint answered %s", res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: %w", errOpsBody, err)
	}
	return nil
}

// logDegraded records why a stats read is shown as unavailable: at debug for
// an endpoint the deployment left unconfigured, a warning otherwise.
func (h *Handler) logDegraded(msg, endpoint string, err error) {
	if errors.Is(err, errOpsUnconfigured) {
		h.log.Debug(msg, zap.String("endpoint", endpoint), zap.Error(err))
		return
	}
	h.log.Warn(msg, zap.String("endpoint", endpoint), zap.Error(err))
}

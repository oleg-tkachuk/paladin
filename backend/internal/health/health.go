// Package health implements the three K8s probe endpoints (/livez,
// /readyz, /startupz) with the semantics most people get wrong:
//
//	/livez    — process is alive. Never touches dependencies. Flips to
//	            503 only after MarkShuttingDown so the kubelet doesn't
//	            restart a pod that's draining gracefully.
//
//	/readyz   — pod can serve traffic. Runs every Ready check inside a
//	            bounded context. 503 short-circuits while shutting down
//	            so traffic stops routing to this pod immediately even
//	            though /livez stays 200 to let in-flight requests drain.
//
//	/startupz — initial bootstrap done (migrations + first DB ping). Once
//	            kubelet sees a 200 it stops polling startupz; subsequent
//	            liveness / readiness probes take over. Useful when the
//	            bootstrap window (e.g. running goose against an empty
//	            cluster) is longer than the readiness probe budget.
//
// Failed probes always log at warn level. Successful probes are silent
// unless LogSuccesses is true — kubelet hits these endpoints multiple
// times per minute per plane and noisy logs swamp real errors.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// DefaultCheckTimeout bounds how long any single check has to return.
// Kubelet's default probe timeout is 1s, so the chart sets
// `timeoutSeconds: 3` and we cap at 2s — enough for a Postgres ping
// over a healthy network, short enough to surface degraded deps before
// kubelet's own deadline fires.
const DefaultCheckTimeout = 2 * time.Second

// Category groups checks for the SystemService.GetHealth UI render.
// Keep the set small + stable — the frontend renders one section per
// category, so adding a new category is a UI change too.
type Category string

const (
	// CategoryDatabase — Postgres pool, migrations, sqlc-driven repos.
	// Always Critical=true; the runtime can't serve a single RPC
	// without it.
	CategoryDatabase Category = "database"
	// CategoryStorage — per-backend S3 reachability + bucket
	// existence. Critical when the backend is the default backend or
	// hosts in-flight uploads; non-critical for archival-only
	// backends listed in storage.backends but not actively used.
	CategoryStorage Category = "storage"
	// CategorySubsystem — internal subsystems gated by config:
	// capability issuer + verifier (signing key loaded), Cedar policy
	// engine, ingest driver. Critical=true when the subsystem is
	// enabled and required by the request path.
	CategorySubsystem Category = "subsystem"
	// CategoryUpstream — outbound dependencies the role calls
	// directly: MCP bridge upstreams (admin / data / iam URLs from
	// the mcp role), JWKS issuers from the federated-IdP path, etc.
	// Typically Critical=false on the api role (an unreachable MCP
	// upstream doesn't break tenant-data RPCs) and Critical=true on
	// the mcp role.
	CategoryUpstream Category = "upstream"
)

// Check is one named health check. Returning a non-nil error fails the
// probe; the name appears in the JSON failures list so operators can
// pinpoint the degraded dependency without reading process logs.
//
// Category + Critical are optional — zero values default to
// `CategorySubsystem` + `Critical=true` so legacy call sites that
// register `health.Check{Name, Func}` keep their old semantics
// (every check counted toward /readyz).
type Check struct {
	Name     string
	Category Category
	Critical bool
	Func     func(context.Context) error
	// Note is surfaced on the GetHealth RPC response even when the
	// check passes. Use it to label informational components — e.g.
	// "disabled" for an off-by-config subsystem so the /health page
	// shows the row instead of silently omitting it. Empty by default.
	Note string
}

// Handler is the probe registrar. Build one per process; share it across
// all plane muxes so the shutting-down state is unified.
//
// Zero-value usage is intentional: a Handler with no checks behaves like
// "always healthy", useful in tests that don't want to wire a fake DB.
type Handler struct {
	// Logger receives warn-level messages on probe failures and (when
	// LogSuccesses=true) info-level messages on every probe call.
	Logger *zap.Logger

	// Timeout per individual check. Default DefaultCheckTimeout when 0.
	Timeout time.Duration

	// LogSuccesses controls whether successful probes emit log lines.
	// Defaults false — kubelet calls these endpoints constantly and the
	// noise drowns real signal. Set true for verbose diagnostics during
	// pod debugging. Failures always log regardless.
	LogSuccesses bool

	// Ready checks run on /readyz. Examples: DB ping, S3 reachability.
	// Empty list = "always ready".
	Ready []Check

	// role names the role that produced this Handler ("api", "admin",
	// "worker", "mcp"). Surfaced on the JSON snapshot endpoint so a
	// fan-out aggregator can attribute components without having to
	// pass the role label per request.
	role string

	// Startup checks run on /startupz. Typically a superset of Ready
	// during bootstrap (e.g. migrations applied + first DB ping). Once
	// kubelet observes a 200 it stops polling startupz forever.
	Startup []Check

	// shuttingDown flips /readyz to 503 immediately on shutdown signal,
	// stopping new traffic while /livez stays 200 to let in-flight
	// requests finish before SIGKILL. atomic.Bool keeps writes lock-free.
	shuttingDown atomic.Bool
}

// MarkShuttingDown is called from the SIGTERM handler. From this point
// /readyz returns 503 with status="draining" and /livez stays 200 until
// the process exits. Idempotent — repeated calls are no-ops.
func (h *Handler) MarkShuttingDown() {
	h.shuttingDown.Store(true)
}

// IsShuttingDown reports whether MarkShuttingDown has been called. Used
// by app shutdown logic to decide whether to wait for kubelet to observe
// the 503 readyz before closing listeners.
func (h *Handler) IsShuttingDown() bool {
	return h.shuttingDown.Load()
}

// Register attaches /livez, /readyz, /startupz to mux. Safe to call once
// per plane mux — handlers themselves close over the same Handler so the
// shutting-down state is shared across planes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /livez", h.serveLive)
	mux.HandleFunc("GET /readyz", h.serveReady)
	mux.HandleFunc("GET /startupz", h.serveStartup)
	mux.HandleFunc("GET /system/health.json", h.serveSnapshot)
}

// ─── handler impls ──────────────────────────────────────────────────────────

// serveLive: 200 unless shutting down. Touches no dependencies.
func (h *Handler) serveLive(w http.ResponseWriter, r *http.Request) {
	if h.shuttingDown.Load() {
		h.respond(w, r, http.StatusServiceUnavailable, "draining", nil)
		return
	}
	h.respond(w, r, http.StatusOK, "ok", nil)
}

// serveReady: 503 if shutting down OR any *critical* Ready check fails.
//
// Non-critical failures still appear in the response body and in
// SystemService.GetHealth, but they do NOT pull the pod out of the
// kubelet endpoint set — they're informational ("degraded" rather
// than "unhealthy"). Use Critical=true for the small set of deps the
// runtime can't serve a single RPC without (Postgres) and
// Critical=false for everything else (storage backends, MCP upstreams)
// so a flapping S3 endpoint doesn't take the whole api Deployment
// off line when reads still work from the cache.
func (h *Handler) serveReady(w http.ResponseWriter, r *http.Request) {
	if h.shuttingDown.Load() {
		h.respond(w, r, http.StatusServiceUnavailable, "draining", nil)
		return
	}
	all := h.runChecks(r.Context(), h.Ready)
	var critical []failure
	for _, f := range all {
		if f.Critical {
			critical = append(critical, f)
		}
	}
	switch {
	case len(critical) > 0:
		h.respond(w, r, http.StatusServiceUnavailable, "unhealthy", all)
	case len(all) > 0:
		// Non-critical failures: 200 with status="degraded" so the
		// kubelet keeps routing traffic but operators see the
		// per-component state in the body.
		h.respond(w, r, http.StatusOK, "degraded", all)
	default:
		h.respond(w, r, http.StatusOK, "ok", nil)
	}
}

// serveStartup: 503 if any Startup check fails. Does NOT honour
// shutting-down — startup is a one-shot bootstrap concern; once it
// returns 200 once, kubelet stops calling it.
func (h *Handler) serveStartup(w http.ResponseWriter, r *http.Request) {
	failures := h.runChecks(r.Context(), h.Startup)
	if len(failures) > 0 {
		h.respond(w, r, http.StatusServiceUnavailable, "starting", failures)
		return
	}
	h.respond(w, r, http.StatusOK, "ok", nil)
}

// runChecks invokes each check in sequence with an independent timeout
// and returns the failures only. Sequential rather than parallel
// because (a) the dep set is small enough that ~5 sequential pings
// finish within the kubelet's 3s probe budget, (b) sequential
// failures preserve cause-first ordering for log readability, and
// (c) parallelising would need to duplicate the Critical-marker so
// the caller can still tell which failures gate /readyz.
func (h *Handler) runChecks(ctx context.Context, checks []Check) []failure {
	if len(checks) == 0 {
		return nil
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	var out []failure
	for _, c := range checks {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		err := c.Func(cctx)
		cancel()
		if err != nil {
			msg := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				// Replace the generic stdlib message with one that
				// names the check + budget so a flapping probe is
				// debuggable from the response body alone.
				msg = "check timed out after " + timeout.String()
			}
			out = append(out, failure{
				Name:     c.Name,
				Error:    msg,
				Critical: c.Critical,
			})
		}
	}
	return out
}

// ─── response shape ─────────────────────────────────────────────────────────

// response is the JSON wire format. `status` is one of
// {"ok", "draining", "starting", "unhealthy"} — single token so log
// scrapers can match it as a literal. failures is omitted (not empty
// array) on success to keep the happy-path payload minimal.
type response struct {
	Status   string    `json:"status"`
	Failures []failure `json:"failures,omitempty"`
}

type failure struct {
	Name     string `json:"name"`
	Error    string `json:"error"`
	Critical bool   `json:"critical,omitempty"`
}

// ─── snapshot ───────────────────────────────────────────────────────────────
//
// Snapshot is the canonical structured form of a /readyz run, used by:
//
//   - the unauthenticated `GET /system/health.json` endpoint exposed on
//     every plane mux (consumed by the BFF aggregator at /api/health/all)
//   - SystemService.GetHealth (admin/iam Connect RPCs, authenticated)
//
// Both surfaces share one runChecks invocation per request — the proto
// shim in connectshim/iam converts Snapshot to its proto twin so the
// authenticated path doesn't drift from the JSON path.

// ComponentStatus enumerates the per-check rollup. Mirrors the proto
// enum paladin.iam.v1.ComponentStatus 1:1 (HEALTHY/DEGRADED/UNHEALTHY).
type ComponentStatus string

const (
	StatusHealthy   ComponentStatus = "healthy"
	StatusDegraded  ComponentStatus = "degraded"
	StatusUnhealthy ComponentStatus = "unhealthy"
)

// Component is one row in a Snapshot.
type Component struct {
	Name      string          `json:"name"`
	Status    ComponentStatus `json:"status"`
	Message   string          `json:"message,omitempty"`
	LatencyMs int64           `json:"latency_ms"`
	Category  string          `json:"category"`
	Critical  bool            `json:"critical"`
}

// Snapshot is the aggregated health view for one role.
type Snapshot struct {
	// Role names the role that produced the snapshot ("api", "admin",
	// "worker", "mcp"). Required so the UI can attribute degradation
	// to the right binary.
	Role       string          `json:"role"`
	Status     ComponentStatus `json:"status"`
	Components []Component     `json:"components"`
	// CheckedAt is the wall clock at the moment runChecks finished.
	// Useful for the UI to show staleness when a probe is slow.
	CheckedAt time.Time `json:"checked_at"`
}

// Snapshot runs every Ready check with its own timeout and returns the
// aggregated result. Aggregate semantics match SystemService.GetHealth:
// any *critical* UNHEALTHY → UNHEALTHY; any non-critical UNHEALTHY (or
// any DEGRADED) → DEGRADED; else HEALTHY. Same asymmetry as /readyz so
// the kubelet endpoint set and the UI agree.
func (h *Handler) Snapshot(ctx context.Context, role string) Snapshot {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	components := make([]Component, 0, len(h.Ready))
	worst := StatusHealthy
	for _, c := range h.Ready {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		err := c.Func(cctx)
		latency := time.Since(start)
		cancel()

		comp := Component{
			Name:      c.Name,
			LatencyMs: latency.Milliseconds(),
			Category:  string(c.Category),
			Critical:  c.Critical,
		}
		switch {
		case err == nil:
			comp.Status = StatusHealthy
			comp.Message = c.Note
		case errors.Is(err, context.DeadlineExceeded):
			comp.Status = StatusUnhealthy
			comp.Message = "check timed out after " + timeout.String()
		default:
			comp.Status = StatusUnhealthy
			comp.Message = err.Error()
		}

		switch {
		case comp.Status == StatusUnhealthy && c.Critical:
			worst = StatusUnhealthy
		case comp.Status == StatusUnhealthy:
			if worst == StatusHealthy {
				worst = StatusDegraded
			}
		}
		components = append(components, comp)
	}
	return Snapshot{
		Role:       role,
		Status:     worst,
		Components: components,
		CheckedAt:  time.Now().UTC(),
	}
}

// Role is the label embedded in the Snapshot the JSON endpoint emits.
// Set on the Handler at construction time so each pod self-identifies
// without callers having to pass it on every probe.
func (h *Handler) WithRole(role string) *Handler {
	h.role = role
	return h
}

// serveSnapshot is the unauthenticated JSON endpoint at
// `/system/health.json`. Same security posture as /readyz: no tenant
// data leaks, every field is operator-visible info already exposed via
// kubelet probes. The BFF's /api/health/all fans out to this endpoint
// across the four roles and returns the merged result to the UI.
func (h *Handler) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	role := h.role
	if role == "" {
		role = "unknown"
	}
	snap := h.Snapshot(r.Context(), role)
	w.Header().Set("Content-Type", "application/json")
	if h.shuttingDown.Load() {
		// Respect drain state — same rationale as /readyz returning 503
		// while draining. The body still carries the per-component
		// detail so the UI shows what's degrading even mid-shutdown.
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(snap)
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, code int, status string, failures []failure) {
	body := response{Status: status, Failures: failures}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)

	if h.Logger == nil {
		return
	}
	if code != http.StatusOK {
		// Failures always log at warn — operators want to see why the
		// pod is failing readiness even if probe success logging is off.
		h.Logger.Warn("probe failed",
			zap.String("path", r.URL.Path),
			zap.Int("status", code),
			zap.String("result", status),
			zap.Any("failures", failures),
		)
		return
	}
	if h.LogSuccesses {
		// Verbose mode — used during pod debugging to confirm probes
		// are firing. Default off because kubelet's probe cadence
		// produces ~6 success log lines per minute per plane.
		h.Logger.Info("probe ok",
			zap.String("path", r.URL.Path),
			zap.String("result", status),
		)
	}
}

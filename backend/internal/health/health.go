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

// Check is one named health check. Returning a non-nil error fails the
// probe; the name appears in the JSON failures list so operators can
// pinpoint the degraded dependency without reading process logs.
type Check struct {
	Name string
	Func func(context.Context) error
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

// serveReady: 503 if shutting down OR any Ready check fails.
func (h *Handler) serveReady(w http.ResponseWriter, r *http.Request) {
	if h.shuttingDown.Load() {
		h.respond(w, r, http.StatusServiceUnavailable, "draining", nil)
		return
	}
	failures := h.runChecks(r.Context(), h.Ready)
	if len(failures) > 0 {
		h.respond(w, r, http.StatusServiceUnavailable, "unhealthy", failures)
		return
	}
	h.respond(w, r, http.StatusOK, "ok", nil)
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

// runChecks invokes each check in sequence with an independent timeout.
// Sequential rather than parallel because (a) the v1 dep set is small —
// one DB ping, maybe an S3 ping later — and (b) sequential failures
// preserve cause-first ordering in the failures slice for log readability.
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
			out = append(out, failure{Name: c.Name, Error: msg})
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
	Name  string `json:"name"`
	Error string `json:"error"`
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

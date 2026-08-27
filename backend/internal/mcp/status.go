package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// UpstreamStatus is one plane's reachability as the bridge sees it.
type UpstreamStatus struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	// Error carries why, when Reachable is false. Empty otherwise.
	Error string `json:"error,omitempty"`
	// LatencyMs is the probe's round trip. Present either way — a slow
	// reachable upstream is worth seeing before it becomes an unreachable one.
	LatencyMs int64 `json:"latency_ms"`
}

// BridgeStatus is what the bridge knows about itself: which planes it can
// reach right now, and how many sessions it is holding.
//
// This exists because the bridge's reachability used to be invisible. The
// admin plane proxies /sessions and treats an unreachable bridge as an empty
// list, so "the bridge is down" and "nobody is using MCP" rendered
// identically — an operator opening the MCP page during an incident saw a
// tidy empty table. Whoever knows the answer should be the one asked, and
// only the bridge knows whether it can reach the planes it proxies to.
type BridgeStatus struct {
	Upstreams []UpstreamStatus `json:"upstreams"`
	Sessions  int              `json:"sessions"`
	CheckedAt time.Time        `json:"checked_at"`
}

// UpstreamTarget names a plane to probe.
type UpstreamTarget struct {
	Name string
	URL  string
}

// StatusHandler serves BridgeStatus as JSON.
//
// Probes run per request rather than on a background timer: the answer is
// worth having fresh, the caller is an operator looking at a page (not a hot
// path), and a cached verdict is exactly the kind of thing that goes stale
// during the incident it was meant to explain. The client bounds the whole
// thing; each probe additionally gets its own short deadline so one hanging
// plane cannot hold the response.
//
// Auth: the same admin-audience JWT + platform-admin role the /sessions
// endpoint requires. No new shared secret.
func StatusHandler(
	reg *SessionRegistry,
	verifier auth.TokenVerifier,
	targets []UpstreamTarget,
	probe func(ctx context.Context, url string) error,
) http.Handler {
	if probe == nil {
		probe = probeHealth
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := authorizePlatformAdmin(r, verifier); code != 0 {
			http.Error(w, http.StatusText(code), code)
			return
		}

		out := BridgeStatus{
			Upstreams: make([]UpstreamStatus, len(targets)),
			CheckedAt: time.Now().UTC(),
		}
		if reg != nil {
			out.Sessions = len(reg.Snapshot())
		}

		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				defer cancel()
				start := time.Now()
				err := probe(ctx, t.URL)
				st := UpstreamStatus{
					Name:      t.Name,
					URL:       t.URL,
					Reachable: err == nil,
					LatencyMs: time.Since(start).Milliseconds(),
				}
				if err != nil {
					st.Error = err.Error()
				}
				out.Upstreams[i] = st
			}()
		}
		wg.Wait()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// HealthProbe asks a plane's /livez over the supplied client, which every role
// registers and which answers without touching the database — the question is
// "can I reach this process", not "is its database well". A non-2xx is as much
// a failure as a dial error: both mean the bridge cannot use it.
//
// The client matters: pass the one the bridge uses for its real upstream
// calls. The planes' certificates chain to the internal mTLS CA, which the
// system roots do not carry, so probing with http.DefaultClient reports
// "x509: certificate signed by unknown authority" for every plane while the
// bridge itself is talking to all three quite happily. A status endpoint that
// invents an outage is worse than none — it is read during the incident it
// misdescribes.
func HealthProbe(c *http.Client) func(context.Context, string) error {
	if c == nil {
		c = http.DefaultClient
	}
	return func(ctx context.Context, base string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/livez", nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return &statusError{code: resp.StatusCode}
		}
		return nil
	}
}

// probeHealth is the zero-configuration default: plaintext upstreams only.
var probeHealth = HealthProbe(nil)

type statusError struct{ code int }

func (e *statusError) Error() string {
	return "upstream answered HTTP " + http.StatusText(e.code)
}

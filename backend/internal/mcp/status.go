package mcp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logfield"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// UpstreamStatus is one plane's reachability as the bridge sees it.
type UpstreamStatus struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	// Error carries why, when Reachable is false: one of a fixed set of
	// verdicts (upstreamVerdict), the probe's own error going to the log.
	// Empty otherwise.
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
		if !authorizePlatformAdmin(w, r, verifier) {
			return
		}

		out := BridgeStatus{CheckedAt: time.Now().UTC()}
		if reg != nil {
			out.Sessions = len(reg.Snapshot())
		}

		out.Upstreams = ProbeUpstreams(r.Context(), targets, probe)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// upstreamProbeTimeout bounds one plane's probe, so one hanging plane cannot
// hold the answer about the others.
const upstreamProbeTimeout = 2 * time.Second

// ProbeUpstreams probes every target at once, each under its own deadline,
// and reports each in the targets' order: reachable or why not, and how long
// the probe took either way.
func ProbeUpstreams(ctx context.Context, targets []UpstreamTarget, probe func(ctx context.Context, url string) error) []UpstreamStatus {
	if probe == nil {
		probe = probeHealth
	}
	out := make([]UpstreamStatus, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, upstreamProbeTimeout)
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
				st.Error = upstreamVerdict(err)
				logUpstreamFailure(ctx, t, st.Error, err)
			}
			out[i] = st
		}()
	}
	wg.Wait()
	return out
}

// The verdicts an upstream's Error is one of. The probe's own error names
// the plane's URL, which UpstreamStatus carries in its own field, and the
// TLS stack's detail; it goes to the log line instead.
const (
	msgUpstreamUnreachable = "the plane is unreachable"
	msgUpstreamTimedOut    = "the plane timed out"
	msgUpstreamCertificate = "the plane's certificate was refused"
	msgUpstreamHandshake   = "the TLS handshake with the plane failed"
	msgUpstreamBadStatus   = "the plane answered HTTP %d"
)

// msgUpstreamProbeFailed is the line a failed probe leaves, with its error.
const msgUpstreamProbeFailed = "mcp upstream probe failed"

// The log keys a failed probe's line carries.
const (
	fieldUpstream = "upstream"
	fieldURL      = "url"
	fieldVerdict  = "verdict"
)

// upstreamVerdict is why a probe failed, as the admin is told it, read from
// the error's types: the status a plane answered, a certificate the bridge
// refused (an unknown authority, an expired or misnamed certificate), a
// handshake the plane ended (its alert, as when it refuses the bridge's
// client certificate), a deadline, else a plane that could not be reached.
// HealthProbe reads no body, so no answer is ever unreadable.
func upstreamVerdict(err error) string {
	if se, ok := errors.AsType[*statusError](err); ok {
		return fmt.Sprintf(msgUpstreamBadStatus, se.code)
	}
	if certificateRefused(err) {
		return msgUpstreamCertificate
	}
	if handshakeFailed(err) {
		return msgUpstreamHandshake
	}
	if te, ok := errors.AsType[timeoutError](err); (ok && te.Timeout()) || errors.Is(err, context.DeadlineExceeded) {
		return msgUpstreamTimedOut
	}
	return msgUpstreamUnreachable
}

// certificateRefused reports whether err is the bridge refusing the plane's
// certificate.
func certificateRefused(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}
	_, ok := errors.AsType[x509.HostnameError](err)
	return ok
}

// tlsRemoteAlertOp is the net.OpError Op crypto/tls reports an alert the
// peer sent with (crypto/tls conn.go, readRecordOrCCS): the alert itself is
// of an unexported type, and tls.AlertError wraps it only over QUIC.
const tlsRemoteAlertOp = "remote error"

// handshakeFailed reports whether err is a TLS handshake the plane ended
// with an alert, as when it refuses the bridge's client certificate. A plane
// answering plaintext on an https URL is not told apart: net/http replaces
// the tls.RecordHeaderError with an untyped error, so it is unreachable.
func handshakeFailed(err error) bool {
	oe, ok := errors.AsType[*net.OpError](err)
	return ok && oe.Op == tlsRemoteAlertOp
}

// timeoutError is a transport error that can say it timed out, as net.Error
// and *url.Error do.
type timeoutError interface {
	error
	Timeout() bool
}

// logUpstreamFailure logs a failed probe at Warn with the error the verdict
// was read from — the TLS stack's detail is what an mTLS failure is debugged
// by. The URL is logged with its credentials redacted, and the error without
// the *url.Error wrapper that repeats it.
func logUpstreamFailure(ctx context.Context, t UpstreamTarget, verdict string, err error) {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		err = ue.Err
	}
	logger.FromContext(ctx).Warn(msgUpstreamProbeFailed,
		zap.String(fieldUpstream, t.Name),
		logfield.URL(fieldURL, t.URL),
		zap.String(fieldVerdict, verdict),
		zap.Error(err),
	)
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

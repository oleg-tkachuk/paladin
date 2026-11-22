package mcp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// probeVerdictTimeout bounds the probe of a plane that never answers, well
// under upstreamProbeTimeout so the test does not wait that out.
const probeVerdictTimeout = 50 * time.Millisecond

// probeFailure is the error HealthProbe gives for a plane set up to fail one
// way, over real connections: the verdict is read from the types the
// transport and the TLS stack really return, not from stand-ins.
type probeFailure func(t *testing.T) (ctx context.Context, client *http.Client, base string)

// tlsPlane is a plane serving TLS, requiring a client certificate when
// requireClientCert is set.
func tlsPlane(t *testing.T, requireClientCert bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if requireClientCert {
		srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestUpstreamVerdictReadsTheErrorsTypes(t *testing.T) {
	cases := map[string]struct {
		setup probeFailure
		want  string
	}{
		"bad status": {func(t *testing.T) (context.Context, *http.Client, string) {
			t.Helper()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)
			return t.Context(), srv.Client(), srv.URL
		}, fmt.Sprintf(msgUpstreamBadStatus, http.StatusServiceUnavailable)},
		"unknown authority": {func(t *testing.T) (context.Context, *http.Client, string) {
			t.Helper()
			srv := tlsPlane(t, false)
			return t.Context(), &http.Client{}, srv.URL
		}, msgUpstreamCertificate},
		"client certificate refused": {func(t *testing.T) (context.Context, *http.Client, string) {
			t.Helper()
			srv := tlsPlane(t, true)
			return t.Context(), srv.Client(), srv.URL
		}, msgUpstreamHandshake},
		// net/http answers it with an untyped error: no type says TLS.
		"not tls": {func(t *testing.T) (context.Context, *http.Client, string) {
			t.Helper()
			srv := httptest.NewServer(http.NotFoundHandler())
			t.Cleanup(srv.Close)
			return t.Context(), &http.Client{}, "https://" + srv.Listener.Addr().String()
		}, msgUpstreamUnreachable},
		"timed out": {func(t *testing.T) (context.Context, *http.Client, string) {
			t.Helper()
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
			t.Cleanup(func() { close(release); srv.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), probeVerdictTimeout)
			t.Cleanup(cancel)
			return ctx, srv.Client(), srv.URL
		}, msgUpstreamTimedOut},
		"refused": {func(t *testing.T) (context.Context, *http.Client, string) {
			t.Helper()
			var lc net.ListenConfig
			l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := l.Addr().String()
			_ = l.Close()
			return t.Context(), &http.Client{}, "http://" + addr
		}, msgUpstreamUnreachable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, client, base := c.setup(t)
			err := HealthProbe(client)(ctx, base)
			if err == nil {
				t.Fatal("the probe succeeded")
			}
			if got := upstreamVerdict(err); got != c.want {
				t.Errorf("verdict = %q, want %q (err %v)", got, c.want, err)
			}
		})
	}
}

// The admin is told the verdict alone; the log line keeps what the TLS
// stack said, which is what an mTLS failure is debugged by, and names the
// plane's URL with its password redacted.
func TestProbeUpstreamsLogsTheErrorTheVerdictHides(t *testing.T) {
	const password = "s3cret"
	srv := tlsPlane(t, false)
	target := UpstreamTarget{Name: "admin", URL: strings.Replace(srv.URL, "https://", "https://probe:"+password+"@", 1)}
	core, logs := observer.New(zapcore.DebugLevel)

	got := ProbeUpstreams(logger.WithContext(t.Context(), zap.New(core)), []UpstreamTarget{target}, HealthProbe(&http.Client{}))

	if len(got) != 1 || got[0].Reachable || got[0].Error != msgUpstreamCertificate {
		t.Fatalf("upstreams = %+v, want admin refused for its certificate", got)
	}
	lines := logs.FilterMessage(msgUpstreamProbeFailed).All()
	if len(lines) != 1 || lines[0].Level != zapcore.WarnLevel {
		t.Fatalf("logged %v, want one Warn line", logs.All())
	}
	f := lines[0].ContextMap()
	if cause, _ := f["error"].(string); !strings.Contains(cause, "x509") {
		t.Errorf("error = %q, want the TLS stack's x509 detail", cause)
	}
	if f[fieldUpstream] != target.Name || f[fieldVerdict] != msgUpstreamCertificate {
		t.Errorf("fields = %v, want the upstream and its verdict", f)
	}
	for k, v := range f {
		if s, ok := v.(string); ok && strings.Contains(s, password) {
			t.Errorf("field %s carries the URL's password: %q", k, s)
		}
	}
}

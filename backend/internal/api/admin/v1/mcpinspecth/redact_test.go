package mcpinspecth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

func TestLoggedURL(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "http://mcp:8095/sessions", "http://mcp:8095/sessions"},
		{"credentials and token", "http://user:pass@mcp:8095/sessions?token=abc#frag", "http://user:xxxxx@mcp:8095/sessions"},
		{"unparsable", "http://[::1", unloggableURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := loggedURL(tc.in); got != tc.want {
				t.Errorf("loggedURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBoundedError(t *testing.T) {
	if got := boundedError(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	if got := boundedError(fmt.Errorf("short")); got != "short" {
		t.Errorf("short = %q", got)
	}
	long := fmt.Errorf("%s", strings.Repeat("x", maxLoggedErrorLen+10))
	if got := boundedError(long); len(got) > maxLoggedErrorLen+len(truncatedMark) || !strings.HasSuffix(got, truncatedMark) {
		t.Errorf("long error logged as %d bytes %q", len(got), got[len(got)-10:])
	}
}

// Neither the configured URL's credentials and query nor an unbounded error
// text the remote chose reaches a log line.
func TestBridgeLinesCarryNoSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	h, logs := observedHandler(config.MCP{})
	h.sessionsURL = strings.Replace(srv.URL, "http://", "http://user:s3cret@", 1) + "/sessions?token=t0ken"
	h.httpClient = srv.Client()

	if _, err := h.ListSessions(ctxAs("admin"), &adminv1.ListSessionsRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.GetBridgeStatus(ctxAs("admin"), &adminv1.GetBridgeStatusRequest{}); err != nil {
		t.Fatal(err)
	}
	lines := logs.FilterLevelExact(zapcore.WarnLevel).All()
	if len(lines) != 2 {
		t.Fatalf("want two warn lines, got %v", logs.All())
	}
	for _, l := range lines {
		for k, v := range l.ContextMap() {
			s := fmt.Sprint(v)
			if strings.Contains(s, "s3cret") || strings.Contains(s, "t0ken") {
				t.Errorf("%q: field %s carries a secret: %q", l.Message, k, s)
			}
		}
	}
}

func TestBridgeFailure(t *testing.T) {
	if got := bridgeFailure(nil); got != msgNoReplicaAnswered {
		t.Errorf("no error = %q", got)
	}
	ue := &url.Error{Op: "Get", URL: "http://h/sessions?token=t0ken", Err: fmt.Errorf("connection refused")}
	if got := bridgeFailure(ue); got != "connection refused" {
		t.Errorf("transport error = %q, want its cause without the URL", got)
	}
}

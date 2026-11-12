package app

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

func sessionConfig() config.Config {
	var cfg config.Config
	cfg.Auth.Issuer = "paladin-core"
	cfg.Auth.JWKSURL = "http://127.0.0.1:1/.well-known/jwks.json"
	cfg.Session.ConsoleOrigins = []string{"https://console.test"}
	cfg.Session.RotateAfter = 5 * time.Minute
	cfg.Session.Server.Addr = "127.0.0.1:0"
	cfg.Session.Upstreams = config.MCPUpstreams{
		IAMURL: "http://iam:8085", DataURL: "http://api:8080", AdminURL: "http://admin:8090",
	}
	return cfg
}

func TestBuildSessionHandlerRefusesWhatCannotRun(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*config.Config)
		names  string
	}{
		"no jwks":         {func(c *config.Config) { c.Auth.JWKSURL = "" }, "auth.jwks_url"},
		"no origin":       {func(c *config.Config) { c.Session.ConsoleOrigins = nil }, "console_origins"},
		"unparseable url": {func(c *config.Config) { c.Session.Upstreams.DataURL = "api:8080" }, "data plane"},
		"missing ca":      {func(c *config.Config) { c.Session.Upstreams.TLS.CaPath = "/nonexistent/ca.crt" }, "session.upstreams.tls"},
	} {
		cfg := sessionConfig()
		tc.mutate(&cfg)
		_, err := BuildSessionHandler(t.Context(), cfg, BuildMeta{}, zap.NewNop())
		if err == nil || !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: %v, want an error naming %s", name, err, tc.names)
		}
	}
}

func TestBuildSessionListenersServesTheRoleAndItsHealth(t *testing.T) {
	set, err := BuildSessionListeners(t.Context(), sessionConfig(), BuildMeta{Version: "test"}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("BuildSessionListeners: %v", err)
	}
	if len(set.Listeners) != 1 || set.Listeners[0].Plane != sessionPlane || set.Health == nil {
		t.Fatalf("listeners = %+v, health %v; want the session listener and its health", set.Listeners, set.Health)
	}
}

// Behind internal TLS the iam plane answers https from a CA the system roots
// lack; the health probe dials it as the forwarded calls do.
func TestSessionHealthReachesATLSIAMPlane(t *testing.T) {
	iam := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != health.LivePath {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(iam.Close)
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: iam.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := sessionConfig()
	cfg.Session.Upstreams.IAMURL = iam.URL
	cfg.Session.Upstreams.TLS.CaPath = ca

	set, err := BuildSessionListeners(t.Context(), cfg, BuildMeta{}, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("BuildSessionListeners: %v", err)
	}
	snap := set.Health.Snapshot(t.Context(), sessionPlane)
	if snap.Status != health.StatusHealthy {
		t.Errorf("health = %+v, want the iam plane reached", snap)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	set.Health.Register(mux)
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, health.SnapshotPath, nil))
	if !strings.Contains(rec.Body.String(), `"role":"session"`) {
		t.Errorf("snapshot = %s, want it to name the session role", rec.Body)
	}
}

package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
)

// An ephemeral loopback port, so the tests never collide with a real one.
const anyLoopbackPort = "127.0.0.1:0"

// scrapeBody is what the stub exporter answers, to tell its response from a
// mux's own.
const scrapeBody = "paladin_test_metric 1\n"

func pulledDeps(addr string) *app.SharedDeps {
	return &app.SharedDeps{
		Cfg: config.Config{OTel: config.OTel{MetricsAddr: addr}},
		Metrics: observability.MetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, scrapeBody)
		})),
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107 -- loopback URL built by the test
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, string(body)
}

func TestStartMetricsListener_NotPulled(t *testing.T) {
	cases := map[string]*app.SharedDeps{
		"no deps":    nil,
		"no handler": {Cfg: config.Config{OTel: config.OTel{MetricsAddr: anyLoopbackPort}}},
	}
	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			ml, err := app.StartMetricsListener(context.Background(), deps, zap.NewNop())
			if err != nil {
				t.Fatalf("StartMetricsListener: %v", err)
			}
			if ml != nil {
				t.Fatalf("got a listener at %v; want none when metrics are not pulled", ml.Addr())
			}
			// The roles call these unconditionally on a nil listener.
			if ml.Addr() != nil {
				t.Errorf("nil listener Addr = %v; want nil", ml.Addr())
			}
			if err := ml.Shutdown(context.Background()); err != nil {
				t.Errorf("nil listener Shutdown: %v", err)
			}
		})
	}
}

func TestStartMetricsListener_ServesOnlyMetrics(t *testing.T) {
	ml, err := app.StartMetricsListener(context.Background(), pulledDeps(anyLoopbackPort), zap.NewNop())
	if err != nil {
		t.Fatalf("StartMetricsListener: %v", err)
	}
	t.Cleanup(func() { _ = ml.Shutdown(context.Background()) })
	base := "http://" + ml.Addr().String()

	if code, body := get(t, base+middleware.PathMetrics); code != http.StatusOK || body != scrapeBody {
		t.Errorf("GET %s = %d %q; want %d %q", middleware.PathMetrics, code, body, http.StatusOK, scrapeBody)
	}
	// A scraper pointed at a health path is on the wrong port and must be
	// told so, not handed a 200.
	for _, path := range []string{"/livez", "/readyz", "/"} {
		if code, _ := get(t, base+path); code != http.StatusNotFound {
			t.Errorf("GET %s = %d; want %d", path, code, http.StatusNotFound)
		}
	}
}

// shutdownRaceRounds is how many start/stop pairs the Shutdown test runs. A
// Shutdown issued before the serving goroutine starts left the socket open
// in a few runs out of a hundred; enough rounds make that window show up in a
// single test run instead of as an occasional CI failure.
const shutdownRaceRounds = 200

func TestStartMetricsListener_Shutdown(t *testing.T) {
	var d net.Dialer
	for range shutdownRaceRounds {
		ml, err := app.StartMetricsListener(context.Background(), pulledDeps(anyLoopbackPort), zap.NewNop())
		if err != nil {
			t.Fatalf("StartMetricsListener: %v", err)
		}
		addr := ml.Addr().String()
		// Immediately, as a role stopping during start-up would.
		if err := ml.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if conn, err := d.DialContext(context.Background(), "tcp", addr); err == nil {
			_ = conn.Close()
			t.Fatalf("%s still accepts connections after Shutdown", addr)
		}
	}
}

func TestStartMetricsListener_PortInUse(t *testing.T) {
	var lc net.ListenConfig
	taken, err := lc.Listen(context.Background(), "tcp", anyLoopbackPort)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = taken.Close() })

	ml, err := app.StartMetricsListener(context.Background(), pulledDeps(taken.Addr().String()), zap.NewNop())
	if err == nil {
		_ = ml.Shutdown(context.Background())
		t.Fatal("StartMetricsListener on a taken port succeeded; want the bind error at start")
	}
}

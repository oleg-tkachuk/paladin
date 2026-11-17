package systemh

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
)

// opsTargets are the ops endpoints the handler reads: one that answers 503,
// one that answers garbage, and one nobody listens on.
func opsTargets(t *testing.T) map[string]string {
	t.Helper()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no bypassrls pool", http.StatusServiceUnavailable)
	}))
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	t.Cleanup(func() { failing.Close(); garbled.Close() })
	return map[string]string{"non-200": failing.URL, "bad body": garbled.URL, "unreachable": closedURL}
}

// Every way the dispatcher and the worker can fail to answer turns into
// "unavailable" on the page — and a warning saying which, and why.
func TestDegradedStatsSayWhy(t *testing.T) {
	for name, url := range opsTargets(t) {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			cfg := config.Config{}
			cfg.Dispatcher.OpsURL, cfg.Worker.OpsURL = url, url
			h := New(cfg, "").WithLogger(zap.New(core))
			ctx := ctxAs(apiutil.RolePlatformAdmin)

			_, _, _ = h.DispatcherStats(ctx)
			res, err := h.PlatformStats(ctx, platformstats.TenantPage{})
			if err != nil || res.RLSAvailable {
				t.Fatalf("PlatformStats = %+v, %v; want unavailable, no error", res, err)
			}
			_, _ = h.PlatformStatsTenants(ctx, platformstats.Signal(""), platformstats.TenantPage{})

			// A dispatcher answering garbage is an error (the RPC fails
			// internal); every other case degrades with a warning.
			warns := logs.Filter(func(e observer.LoggedEntry) bool { return e.Level >= zapcore.WarnLevel }).All()
			if len(warns) != 3 {
				t.Fatalf("want a line from each of the three reads, got %v", logs.All())
			}
			for _, w := range warns {
				if w.ContextMap()["error"] == nil || w.ContextMap()["endpoint"] == nil {
					t.Errorf("warning %q names no endpoint or reason: %v", w.Message, w.ContextMap())
				}
			}
		})
	}
}

// An endpoint left unconfigured is a deployment's choice, not a fault: it is
// noted at debug, not warned about on every page load.
func TestUnconfiguredStatsAreNotWarnings(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := New(config.Config{}, "").WithLogger(zap.New(core))
	ctx := ctxAs(apiutil.RolePlatformAdmin)
	_, _, _ = h.DispatcherStats(ctx)
	_, _ = h.PlatformStats(ctx, platformstats.TenantPage{})
	if n := logs.FilterLevelExact(zapcore.WarnLevel).Len(); n != 0 {
		t.Fatalf("%d warnings for unconfigured endpoints: %v", n, logs.All())
	}
	if logs.FilterLevelExact(zapcore.DebugLevel).Len() != 2 {
		t.Fatalf("want a debug line for each unconfigured read, got %v", logs.All())
	}
}

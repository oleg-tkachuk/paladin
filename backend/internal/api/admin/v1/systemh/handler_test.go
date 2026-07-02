package systemh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

func ctxAs(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: uuid.New(),
		Subject:  "op-1",
		Roles:    roles,
	})
}

func TestDispatcherStats_RequiresPlatformAdmin(t *testing.T) {
	h := New(config.Config{}, "")
	_, _, err := h.DispatcherStats(ctxAs("tenant.admin"))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestDispatcherStats_UnconfiguredIsUnavailableNotError(t *testing.T) {
	h := New(config.Config{}, "") // no dispatcher.ops_url
	stats, available, err := h.DispatcherStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("err = %v, want nil (graceful degrade)", err)
	}
	if available || stats != nil {
		t.Errorf("available=%v stats=%v, want false/nil", available, stats)
	}
}

func TestDispatcherStats_UnreachableIsUnavailableNotError(t *testing.T) {
	cfg := config.Config{}
	cfg.Dispatcher.OpsURL = "http://127.0.0.1:1" // nothing listens there
	h := New(cfg, "")
	stats, available, err := h.DispatcherStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("err = %v, want nil (graceful degrade)", err)
	}
	if available || stats != nil {
		t.Errorf("available=%v stats=%v, want false/nil", available, stats)
	}
}

func TestDispatcherStats_ProxiesTheOpsEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/dispatcher-stats.json" {
			t.Errorf("proxied path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"pending": 7, "failed": 2, "oldest_pending_seconds": 41,
			"subscriptions": [{
				"subscription_id": "s-1", "tenant_id": "t-1",
				"pending": 7, "failed": 2,
				"last_error": "kafka write: dial tcp: no such host",
				"last_status_code": 0,
				"last_attempt_at": "2026-07-02T00:00:00Z"
			}]
		}`))
	}))
	defer srv.Close()

	cfg := config.Config{}
	cfg.Dispatcher.OpsURL = srv.URL
	h := New(cfg, "")
	stats, available, err := h.DispatcherStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("DispatcherStats: %v", err)
	}
	if !available || stats == nil {
		t.Fatalf("available=%v stats=%v, want true/non-nil", available, stats)
	}
	if stats.Pending != 7 || stats.Failed != 2 || stats.OldestPendingSeconds != 41 {
		t.Errorf("totals = %+v", stats)
	}
	if len(stats.Subscriptions) != 1 || stats.Subscriptions[0].LastError == "" {
		t.Errorf("subscriptions = %+v", stats.Subscriptions)
	}
}

func TestDispatcherStats_Non200IsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := config.Config{}
	cfg.Dispatcher.OpsURL = srv.URL
	h := New(cfg, "")
	_, available, err := h.DispatcherStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil || available {
		t.Errorf("err=%v available=%v, want nil/false", err, available)
	}
}

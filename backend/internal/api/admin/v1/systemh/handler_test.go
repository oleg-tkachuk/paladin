package systemh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
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

// ─── PlatformStats ──────────────────────────────────────────────────────────
//
// These cover the leg that needs no DB: the role gate and the worker-proxy
// degrade path. The control-plane census (which needs a pool) is exercised
// by the platformstats package's own tests against a real Postgres.

func TestPlatformStats_RequiresPlatformAdmin(t *testing.T) {
	h := New(config.Config{}, "")
	_, err := h.PlatformStats(ctxAs("tenant.admin"))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

func TestPlatformStats_UnconfiguredWorkerIsUnavailableNotError(t *testing.T) {
	h := New(config.Config{}, "") // no worker.ops_url, no pool
	res, err := h.PlatformStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("err = %v, want nil (graceful degrade)", err)
	}
	if res.RLSAvailable || res.RLS != nil {
		t.Errorf("available=%v rls=%v, want false/nil", res.RLSAvailable, res.RLS)
	}
}

func TestPlatformStats_UnreachableWorkerIsUnavailableNotError(t *testing.T) {
	cfg := config.Config{}
	cfg.Worker.OpsURL = "http://127.0.0.1:1" // nothing listens there
	res, err := New(cfg, "").PlatformStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("err = %v, want nil (graceful degrade)", err)
	}
	if res.RLSAvailable {
		t.Error("available = true, want false")
	}
}

func TestPlatformStats_ProxiesTheWorkerOpsEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/rls-census.json" {
			t.Errorf("proxied path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"objects":{
			  "states":[{"state":"AVAILABLE","count":7,"bytes":2048}],
			  "total_count":7,"total_bytes":2048,
			  "tenants":[{"tenant_id":"11111111-1111-1111-1111-111111111111",
			              "states":[{"state":"AVAILABLE","count":7,"bytes":2048}],
			              "total_count":7,"total_bytes":2048}],
			  "tenants_truncated":0},
			"quotas":{"total":2,"tenant_scoped":2,"with_limits":1,"at_limit":1,
			          "usage_object_count":9,"usage_total_bytes":4096},
			"capabilities":{"total":5,"active":3,"expired":1,"revoked":1,
			                "by_principal_kind":{"service_account":3}},
			"api_tokens":{"total":4,"active":2,"expired":1,"revoked":1,"never_used":1},
			"subscriptions":{"total":3,"enabled":2,"disabled":1,
			                 "by_sink_kind":{"http":2}}}`))
	}))
	defer srv.Close()

	cfg := config.Config{}
	cfg.Worker.OpsURL = srv.URL
	res, err := New(cfg, "").PlatformStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("PlatformStats: %v", err)
	}
	if !res.RLSAvailable {
		t.Fatal("available = false, want true")
	}
	objects := res.RLS.Objects
	if objects.TotalCount != 7 || objects.TotalBytes != 2048 {
		t.Errorf("totals = %d/%d, want 7/2048", objects.TotalCount, objects.TotalBytes)
	}
	if len(objects.Tenants) != 1 || objects.Tenants[0].TotalCount != 7 {
		t.Errorf("tenants = %+v, want one row with 7 objects", objects.Tenants)
	}
	// Every sibling census rides the same payload — one round-trip, not five.
	if res.RLS.Quotas.AtLimit != 1 || res.RLS.Quotas.UsageTotalBytes != 4096 {
		t.Errorf("quotas = %+v, want at_limit 1 / usage 4096", res.RLS.Quotas)
	}
	if res.RLS.Capabilities.Active != 3 ||
		res.RLS.Capabilities.ByPrincipalKind["service_account"] != 3 {
		t.Errorf("capabilities = %+v, want 3 active service_account", res.RLS.Capabilities)
	}
	if res.RLS.APITokens.NeverUsed != 1 {
		t.Errorf("api tokens = %+v, want never_used 1", res.RLS.APITokens)
	}
	if res.RLS.Subscriptions.Enabled != 2 ||
		res.RLS.Subscriptions.BySinkKind["http"] != 2 {
		t.Errorf("subscriptions = %+v, want 2 enabled http", res.RLS.Subscriptions)
	}
	// No pool wired → no labels, but the census still comes through.
	if len(res.TenantNames) != 0 {
		t.Errorf("tenant names = %v, want empty without a pool", res.TenantNames)
	}
}

func TestPlatformStats_Non200WorkerIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no bypassrls pool", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := config.Config{}
	cfg.Worker.OpsURL = srv.URL
	res, err := New(cfg, "").PlatformStats(ctxAs(apiutil.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("err = %v, want nil (graceful degrade)", err)
	}
	if res.RLSAvailable {
		t.Error("available = true, want false")
	}
}

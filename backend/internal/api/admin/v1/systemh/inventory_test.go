package systemh

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/platformstats"
)

const labelledTenant = "11111111-1111-1111-1111-111111111111"

// fakeInventory stands in for the admin pod's own reads.
type fakeInventory struct {
	cp        *platformstats.ControlPlane
	cpErr     error
	names     map[string]TenantName
	namesErr  error
	asked     []string
	crossRead bool
}

func (f *fakeInventory) ControlPlane(ctx context.Context) (*platformstats.ControlPlane, error) {
	f.crossRead = auth.CrossTenantRead(ctx)
	return f.cp, f.cpErr
}

func (f *fakeInventory) TenantNames(_ context.Context, ids []string) (map[string]TenantName, error) {
	f.asked = ids
	return f.names, f.namesErr
}

// censusServer answers the worker's census with one tenant in the object
// table, and the signal drill-down with the same tenant.
func censusServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == platformstats.SignalTenantsPath {
			_, _ = w.Write([]byte(`{"tenants":[{"tenant_id":"` + labelledTenant + `","count":1}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"objects":{"tenants":[{"tenant_id":"` + labelledTenant + `","total_count":7}]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func statsHandler(t *testing.T, inv Inventory, workerURL string) (*Handler, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	cfg := config.Config{}
	cfg.Worker.OpsURL = workerURL
	return New(cfg, "").WithInventory(inv).WithLogger(zap.New(core)), logs
}

// The control-plane census is read across tenants, and the object table's
// tenants are labelled from the inventory.
func TestPlatformStatsReadsTheInventory(t *testing.T) {
	inv := &fakeInventory{
		cp:    &platformstats.ControlPlane{},
		names: map[string]TenantName{labelledTenant: {Slug: "acme", DisplayName: "Acme"}},
	}
	h, _ := statsHandler(t, inv, censusServer(t).URL)
	res, err := h.PlatformStats(ctxAs(apiutil.RolePlatformAdmin), platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("PlatformStats: %v", err)
	}
	if res.ControlPlane != inv.cp {
		t.Error("control plane census not passed through")
	}
	if !inv.crossRead {
		t.Error("control plane census read without the cross-tenant flag: every other tenant's binding would be counted as none")
	}
	if len(inv.asked) != 1 || inv.asked[0] != labelledTenant {
		t.Errorf("labels asked for %v, want the census tenant", inv.asked)
	}
	if res.TenantNames[labelledTenant].Slug != "acme" {
		t.Errorf("labels = %v", res.TenantNames)
	}
}

// A failing inventory read fails the RPC as internal, and says why in the log.
func TestPlatformStatsInventoryFailures(t *testing.T) {
	boom := errors.New("connection reset")
	for _, tc := range []struct {
		name string
		inv  *fakeInventory
		want string
	}{
		{"control plane", &fakeInventory{cpErr: boom}, "platform stats: control plane census failed"},
		{"tenant labels", &fakeInventory{cp: &platformstats.ControlPlane{}, namesErr: boom}, "platform stats: tenant labels failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := statsHandler(t, tc.inv, censusServer(t).URL)
			_, err := h.PlatformStats(ctxAs(apiutil.RolePlatformAdmin), platformstats.TenantPage{})
			if connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("err = %v, want internal", err)
			}
			if logs.FilterMessage(tc.want).FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
				t.Fatalf("want one %q error line, got %v", tc.want, logs.All())
			}
		})
	}
}

// The drill-down labels its tenants the same way, and fails the same way.
func TestPlatformStatsTenantsLabelsFromTheInventory(t *testing.T) {
	inv := &fakeInventory{names: map[string]TenantName{labelledTenant: {Slug: "acme"}}}
	h, _ := statsHandler(t, inv, censusServer(t).URL)
	res, err := h.PlatformStatsTenants(ctxAs(apiutil.RolePlatformAdmin), platformstats.Signal(""), platformstats.TenantPage{})
	if err != nil {
		t.Fatalf("PlatformStatsTenants: %v", err)
	}
	if res.TenantNames[labelledTenant].Slug != "acme" {
		t.Errorf("labels = %v", res.TenantNames)
	}

	inv.namesErr = errors.New("connection reset")
	if _, err := h.PlatformStatsTenants(ctxAs(apiutil.RolePlatformAdmin), platformstats.Signal(""), platformstats.TenantPage{}); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("err = %v, want internal", err)
	}
}

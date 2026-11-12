package systemh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

const testSnapshotToken = "platform-health-test-token"

// role serves a snapshot of name with status code, refusing any request
// without the snapshot token.
func role(t *testing.T, name string, code int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != health.SnapshotPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(health.SnapshotTokenHeader) != testSnapshotToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(health.Snapshot{
			Role: name, Status: health.StatusHealthy,
			Components: []health.Component{{Name: "postgres", Status: health.StatusHealthy}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func platformHealthHandler(timeout time.Duration, roles ...config.AdminHealthRole) *Handler {
	var cfg config.Config
	cfg.Runtime.HealthSnapshotToken = testSnapshotToken
	cfg.Admin.Health = config.AdminHealth{Roles: roles, Timeout: timeout}
	return New(cfg, "")
}

// unreachable reports whether s is the answer for a role that did not
// answer, and that its message says why.
func unreachable(s health.Snapshot, why string) bool {
	return s.Status == health.StatusUnhealthy && len(s.Components) == 1 &&
		s.Components[0].Name == reachabilityComponent && s.Components[0].Critical &&
		strings.Contains(s.Components[0].Message, why)
}

func TestPlatformHealthReadsEveryRoleInOrder(t *testing.T) {
	h := platformHealthHandler(time.Second,
		config.AdminHealthRole{Name: "iam", URL: role(t, "iam", http.StatusOK)},
		config.AdminHealthRole{Name: "api", URL: role(t, "api", http.StatusOK)},
	)
	got := h.PlatformHealth(t.Context())
	if len(got) != 2 || got[0].Role != "iam" || got[1].Role != "api" {
		t.Fatalf("roles = %+v, want iam then api", got)
	}
	if got[0].Status != health.StatusHealthy || len(got[0].Components) != 1 {
		t.Errorf("iam = %+v, want its own snapshot", got[0])
	}
}

// A draining role answers 503 with its snapshot: it is shown, not hidden
// behind a reachability failure.
func TestPlatformHealthKeepsADrainingRolesSnapshot(t *testing.T) {
	h := platformHealthHandler(time.Second, config.AdminHealthRole{Name: "worker", URL: role(t, "worker", http.StatusServiceUnavailable)})
	got := h.PlatformHealth(t.Context())
	if got[0].Role != "worker" || len(got[0].Components) != 1 || got[0].Components[0].Name != "postgres" {
		t.Errorf("worker = %+v, want its own snapshot", got[0])
	}
}

func TestPlatformHealthListsARoleThatDoesNotAnswer(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(slow.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	wrongToken := role(t, "admin", http.StatusOK)

	h := platformHealthHandler(100*time.Millisecond,
		config.AdminHealthRole{Name: "mcp", URL: slow.URL},
		config.AdminHealthRole{Name: "dispatcher", URL: closedURL},
		config.AdminHealthRole{Name: "admin", URL: wrongToken},
	)
	h.cfg.Runtime.HealthSnapshotToken = "not-the-token"
	got := h.PlatformHealth(t.Context())
	if len(got) != 3 {
		t.Fatalf("got %d roles, want every configured one", len(got))
	}
	for i, name := range []string{"mcp", "dispatcher", "admin"} {
		if got[i].Role != name {
			t.Errorf("role %d = %q, want %q", i, got[i].Role, name)
		}
	}
	if !unreachable(got[0], "deadline") && !unreachable(got[0], "Timeout") {
		t.Errorf("slow role = %+v, want unreachable naming the timeout", got[0])
	}
	if !unreachable(got[1], "refused") {
		t.Errorf("closed role = %+v, want unreachable naming the refusal", got[1])
	}
	if !unreachable(got[2], "401") {
		t.Errorf("refused role = %+v, want unreachable naming the status", got[2])
	}
}

func TestPlatformHealthWithNoRolesIsEmpty(t *testing.T) {
	if got := platformHealthHandler(time.Second).PlatformHealth(t.Context()); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

// A role behind internal TLS is read with the client trusting its CA, and is
// unreachable to one that does not.
func TestPlatformHealthReadsATLSRoleWithTheTrustingClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(health.Snapshot{Role: "admin", Status: health.StatusHealthy})
	}))
	t.Cleanup(srv.Close)
	roleTLS := config.AdminHealthRole{Name: "admin", URL: srv.URL}

	if got := platformHealthHandler(time.Second, roleTLS).PlatformHealth(t.Context()); !unreachable(got[0], "certificate") {
		t.Errorf("without the CA: %+v, want unreachable naming the certificate", got[0])
	}
	got := platformHealthHandler(time.Second, roleTLS).WithHealthClient(srv.Client()).PlatformHealth(t.Context())
	if got[0].Status != health.StatusHealthy {
		t.Errorf("with the CA: %+v, want its snapshot", got[0])
	}
}

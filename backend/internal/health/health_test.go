package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// helper: register a Handler on a fresh mux and run one GET request.
func probe(t *testing.T, h *Handler, path string) (int, response) {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var body response
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return rec.Code, body
}

func TestLivez_Healthy(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	code, body := probe(t, h, "/livez")
	if code != http.StatusOK {
		t.Errorf("status: got %d want 200", code)
	}
	if body.Status != "ok" {
		t.Errorf("status: got %q want %q", body.Status, "ok")
	}
}

func TestLivez_Draining(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	h.MarkShuttingDown()
	code, body := probe(t, h, "/livez")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", code)
	}
	if body.Status != "draining" {
		t.Errorf("status: got %q want %q", body.Status, "draining")
	}
}

func TestReadyz_NoChecks_OK(t *testing.T) {
	t.Parallel()
	// Empty Ready slice is a valid configuration — pod is "always ready"
	// at the dependency layer. Useful for read-only services.
	h := &Handler{}
	code, body := probe(t, h, "/readyz")
	if code != http.StatusOK {
		t.Errorf("status: got %d want 200", code)
	}
	if body.Status != "ok" {
		t.Errorf("status: got %q want %q", body.Status, "ok")
	}
}

func TestReadyz_FailingCheck(t *testing.T) {
	t.Parallel()
	h := &Handler{
		Ready: []Check{
			{Name: "db", Critical: true, Func: func(context.Context) error { return errors.New("connection refused") }},
		},
	}
	code, body := probe(t, h, "/readyz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", code)
	}
	if body.Status != "unhealthy" {
		t.Errorf("status: got %q want %q", body.Status, "unhealthy")
	}
	if len(body.Failures) != 1 || body.Failures[0].Name != "db" {
		t.Errorf("failures: got %+v want one entry named 'db'", body.Failures)
	}
	if body.Failures[0].Error != "connection refused" {
		t.Errorf("failure error: got %q", body.Failures[0].Error)
	}
}

func TestReadyz_ManyChecks_AllFailuresReported(t *testing.T) {
	t.Parallel()
	// Sequential cause-first ordering — db before s3 in the failures
	// slice exactly matches the registration order, so log scrapers can
	// pick the "first failure" off the top reliably.
	h := &Handler{
		Ready: []Check{
			{Name: "db", Critical: true, Func: func(context.Context) error { return errors.New("db down") }},
			{Name: "s3", Critical: true, Func: func(context.Context) error { return errors.New("s3 down") }},
		},
	}
	_, body := probe(t, h, "/readyz")
	if len(body.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(body.Failures))
	}
	if body.Failures[0].Name != "db" || body.Failures[1].Name != "s3" {
		t.Errorf("ordering broken: %+v", body.Failures)
	}
}

func TestReadyz_DrainingShortCircuits(t *testing.T) {
	t.Parallel()
	// During shutdown, readyz must flip to 503 BEFORE the checks run.
	// If we let the checks run first, a slow DB ping would block the
	// shutdown response and kubelet would keep routing traffic to a
	// pod that's about to disappear.
	checkRan := false
	h := &Handler{
		Ready: []Check{
			{Name: "expensive", Critical: true, Func: func(context.Context) error {
				checkRan = true
				return nil
			}},
		},
	}
	h.MarkShuttingDown()
	code, body := probe(t, h, "/readyz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", code)
	}
	if body.Status != "draining" {
		t.Errorf("status: got %q want %q", body.Status, "draining")
	}
	if checkRan {
		t.Error("ready check ran during shutdown — should short-circuit before checks")
	}
}

func TestStartupz_FailureSetsStarting(t *testing.T) {
	t.Parallel()
	h := &Handler{
		Startup: []Check{
			{Name: "migrations", Critical: true, Func: func(context.Context) error { return errors.New("not yet applied") }},
		},
	}
	code, body := probe(t, h, "/startupz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", code)
	}
	// "starting" rather than "unhealthy" — startup is a bootstrap
	// concern, conceptually different from a degraded ready state.
	if body.Status != "starting" {
		t.Errorf("status: got %q want %q", body.Status, "starting")
	}
}

func TestStartupz_IgnoresShuttingDown(t *testing.T) {
	t.Parallel()
	// Once kubelet sees startupz=200 once, it stops polling. Honouring
	// shuttingDown here would be meaningless — by definition that pod
	// has already passed startup. So shuttingDown is a no-op for this
	// endpoint; the probe still exercises its checks.
	h := &Handler{
		Startup: []Check{
			{Name: "always-ok", Critical: true, Func: func(context.Context) error { return nil }},
		},
	}
	h.MarkShuttingDown()
	code, body := probe(t, h, "/startupz")
	if code != http.StatusOK {
		t.Errorf("status: got %d want 200 (shutdown should not affect startupz)", code)
	}
	if body.Status != "ok" {
		t.Errorf("status: got %q want %q", body.Status, "ok")
	}
}

func TestCheck_RespectsTimeout(t *testing.T) {
	t.Parallel()
	// A check that blocks longer than Timeout must surface a deadline
	// failure rather than holding the probe handler open. Use a short
	// budget so the test runs fast.
	h := &Handler{
		Timeout: 30 * time.Millisecond,
		Ready: []Check{
			{Name: "slow", Critical: true, Func: func(ctx context.Context) error {
				select {
				case <-time.After(500 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}},
		},
	}
	code, body := probe(t, h, "/readyz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d want 503", code)
	}
	if len(body.Failures) != 1 {
		t.Fatalf("failures: got %+v", body.Failures)
	}
	if got := body.Failures[0].Error; got == "" || got[:len("check timed out")] != "check timed out" {
		t.Errorf("expected timeout-named error, got %q", got)
	}
}

func TestIsShuttingDown(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	if h.IsShuttingDown() {
		t.Error("zero-value Handler should not be shutting down")
	}
	h.MarkShuttingDown()
	if !h.IsShuttingDown() {
		t.Error("MarkShuttingDown should flip the flag")
	}
	// Idempotent — second call is fine.
	h.MarkShuttingDown()
	if !h.IsShuttingDown() {
		t.Error("MarkShuttingDown should remain set after repeated call")
	}
}

//go:build integration

// Package integration exercises the docker-compose smoke stack end-to-end.
//
// Bring the stack up first:
//
//	docker compose -f deploy/docker-compose.yaml up --build -d
//
// Then run:
//
//	go test -tags=integration ./tests/integration/...
//
// The test only asserts that every plane is reachable on its expected
// port — the heavier behavioural coverage lives in tests/api/e2e (Hurl).
// This is intentionally narrow: its job is to fail fast when a refactor
// breaks the "stack composes" claim, not to substitute for the e2e suite.
package integration

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

type probe struct {
	name string
	url  string
}

// The four planes the e2e compose stack actually runs. `mcp` is absent
// deliberately — that file brings up postgres, migrate, bootstrap, api,
// admin and ui, and leaves the worker / mcp / ingest / dispatcher planes
// out because the Playwright suite never exercises background jobs. Probing
// a plane the stack does not start made this test fail the moment a stack
// was up, which is the one situation it exists for.
var probes = []probe{
	{"data", "http://127.0.0.1:8080/readyz"},
	{"iam", "http://127.0.0.1:8085/readyz"},
	{"admin", "http://127.0.0.1:8090/readyz"},
}

// TestSmokeStackReady waits up to 60s for every plane to report ready.
// Compose's `service_completed_successfully` chains the migrate/bootstrap
// one-shots before the long-running services boot, but the planes still
// take a few seconds to bind their listeners after that — hence the
// poll loop instead of a single shot.
func TestSmokeStackReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Second}

	// Precondition: a live compose stack on localhost (see the file header).
	// Unlike the rest of this directory, this test is NOT self-contained — it
	// probes a running deployment, not a testcontainers Postgres. When the
	// stack is not up (every CI run of the integration gate, and any local
	// run without `docker compose up`), skip rather than fail: an unmet
	// environment precondition is a skip, not a red. Set PALADIN_SMOKE=1 to force
	// it to run and fail loudly, e.g. in a job that brought the stack up.
	if os.Getenv("PALADIN_SMOKE") != "1" {
		probe := &http.Client{Timeout: 1 * time.Second}
		resp, err := probe.Get(probes[0].url)
		if err != nil {
			t.Skipf("compose stack not reachable at %s (%v); "+
				"bring it up and set PALADIN_SMOKE=1 to run this smoke test", probes[0].url, err)
		}
		_ = resp.Body.Close()
	}

	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			if err := waitReady(ctx, client, p.url); err != nil {
				t.Fatalf("%s plane never became ready at %s: %v", p.name, p.url, err)
			}
		})
	}
}

func waitReady(ctx context.Context, c *http.Client, url string) error {
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	var lastErr error
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = &statusErr{code: resp.StatusCode}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

type statusErr struct{ code int }

func (e *statusErr) Error() string { return http.StatusText(e.code) }

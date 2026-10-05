//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// A delivery outcome the runner fails to write leaves the row to its lease, so
// a log line is all an operator gets. These tests make the write fail — the
// sink closes the runner's own pool while it answers — and pin that each
// failure says so, and that a write that succeeds says nothing.

const (
	deliveredWriteFailed = "recording a delivery failed; the row is redelivered after its lease"
	failedWriteFailed    = "failed to mark delivery row"
)

// runnerPool is a pool of the runner's own on the test's database, so closing
// it leaves the fixture's pool free for the assertions.
func (f *dispatcherFixture) runnerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), f.h.MigrateDSN)
	if err != nil {
		t.Fatalf("runner pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// closingSink answers every request with status, closing pool first when it
// is set.
func closingSink(t *testing.T, pool *pgxpool.Pool, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if pool != nil {
			pool.Close()
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOutboxRunner_LogsOnlyTheOutcomeWritesThatFail(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		closePool bool
		wantLog   string // "" = no warning at all
	}{
		{"delivered and recorded", http.StatusOK, false, ""},
		{"retry scheduled", http.StatusInternalServerError, false, ""},
		{"delivered, record lost", http.StatusOK, true, deliveredWriteFailed},
		{"failed, record lost", http.StatusInternalServerError, true, failedWriteFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := setupDispatcher(t)
			pool := f.runnerPool(t)
			var closing *pgxpool.Pool
			if tc.closePool {
				closing = pool
			}
			sink := closingSink(t, closing, tc.status)

			tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-logs")
			f.seedSubscription(t, tenant, subOpts{URL: sink.URL})
			d := f.dispatcher()
			if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			core, logs := observer.New(zap.WarnLevel)
			r := f.outboxRunner(d)
			r.Pool = pool
			r.Logger = zap.New(core)
			f.tickOnce(t, r)

			if tc.wantLog == "" {
				if logs.Len() != 0 {
					t.Errorf("warnings = %v, want none", logs.All())
				}
				return
			}
			if logs.FilterMessage(tc.wantLog).Len() != 1 {
				t.Errorf("warnings = %v, want one %q", logs.All(), tc.wantLog)
			}
		})
	}
}

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The ops listeners stopped serving /metrics: every role serves it on
// otel.metrics_addr only (ADR-0023), so a scraper has one port to find. Each
// mux is built with a metrics handler present, the case where they used to
// mount it.
func TestOpsMuxesDoNotServeMetrics(t *testing.T) {
	deps := &app.SharedDeps{
		Metrics: observability.MetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "paladin_test_metric 1\n")
		})),
	}
	l := zap.NewNop()
	workerMux, _ := workerOpsMux(config.Runtime{}, deps, l)
	dispatcherMux, _ := dispatcherOpsMux(deps, nil, nil, worker.NewNatsConnPool(nil), worker.NewRabbitMQConnPool(nil), l)
	muxes := map[string]http.Handler{
		"worker":     workerMux,
		"dispatcher": dispatcherMux,
		"ingest":     ingestOpsMux(deps, nil, l),
	}
	for role, mux := range muxes {
		t.Run(role, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, middleware.PathMetrics, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s ops mux GET %s = %d; want %d", role, middleware.PathMetrics, rec.Code, http.StatusNotFound)
			}
		})
	}
}

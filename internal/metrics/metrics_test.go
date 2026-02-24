package metrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func TestMetricsPrometheusHelpers(t *testing.T) {
	// Simple assertions to ensure calling the helpers doesn't panic
	assert.NotPanics(t, func() {
		metrics.RecordObjectOperation("create", "success", 0.5)
		metrics.RecordS3Operation("put", "success", 0.2)
		metrics.RecordDatabaseQuery("select", "success", 0.1)
		metrics.UpdateConnectionPoolMetrics(5, 10, 50, 15)
		metrics.RecordCacheOperation("get", "hit")
		metrics.UpdateRateLimiterMetrics(10, 100)
		metrics.RecordHTTPRequest("GET", "/v1/objects", "200", 0.05)
	})
}

func TestOtelHelpers(t *testing.T) {
	ctx := context.Background()
	start := time.Now()

	assert.NotPanics(t, func() {
		metrics.RecordObjectOp(ctx, "create", "success", start)
		metrics.RecordS3Op(ctx, "put", "success", start)
		metrics.RecordDbQuery(ctx, "select", "success", start)
		metrics.RecordCacheOp(ctx, "get", "hit")
	})
}

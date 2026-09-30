package metrics_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

func TestRecordResourceNameShape(t *testing.T) {
	// Smoke test: recording each shape must not panic (no-op meter when
	// OTel is disabled, real counter otherwise).
	assert.NotPanics(t, func() {
		metrics.RecordResourceNameShape(context.Background(), "canonical")
		metrics.RecordResourceNameShape(context.Background(), "tenant")
		metrics.RecordResourceNameShape(context.Background(), "bare")
	})
}

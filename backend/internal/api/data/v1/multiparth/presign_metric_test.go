package multiparth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

// presignMetricName is the counter the dashboards chart presign traffic from.
const presignMetricName = "paladin_presign_total"

// ONE provider for the package, installed before any test runs: OTel's global
// meter binds its delegate once, so a provider swapped in per test would leave
// the instruments recording into whichever came first.
var presignMetricReader = func() sdkmetric.Reader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

func presignCount(t *testing.T, op, outcome string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := presignMetricReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := attribute.NewSet(attribute.String("op", op), attribute.String("outcome", outcome))
	var n int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != presignMetricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is %T, want an int64 sum", m.Name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				if dp.Attributes.Equals(&want) {
					n += dp.Value
				}
			}
		}
	}
	return n
}

// The op="part" series was recorded only by PresignService's part method,
// which no RPC reached; the part URLs clients actually receive come from this
// handler and were never counted, so the dashboard showed no multipart
// traffic at all. Counted as deltas so other tests cannot move the result.
func TestPresignPartIsCounted(t *testing.T) {
	tid := uuid.New()
	repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
		return sessionForTenant(tid), nil
	}}
	h := newHandler(repo, &fakeStorage{}, allow())

	okBefore := presignCount(t, metrics.PresignOpPart, metrics.PresignOutcomeOK)
	if _, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, 0, SessionRef{}); err != nil {
		t.Fatal(err)
	}
	if got := presignCount(t, metrics.PresignOpPart, metrics.PresignOutcomeOK) - okBefore; got != 1 {
		t.Errorf("a part presign added %d ok samples, want 1", got)
	}

	// A refusal is counted too, under the outcome its error maps to.
	_, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, -time.Second, SessionRef{})
	outcome := metrics.PresignOutcome(err)
	if err == nil || outcome == metrics.PresignOutcomeOK {
		t.Fatalf("negative ttl: err=%v outcome=%q, want a refusal", err, outcome)
	}
	before := presignCount(t, metrics.PresignOpPart, outcome)
	_, _, _, _ = h.PresignPart(authedCtx(tid), "up-1", 1, -time.Second, SessionRef{})
	if got := presignCount(t, metrics.PresignOpPart, outcome) - before; got != 1 {
		t.Errorf("a refused part presign added %d %q samples, want 1", got, outcome)
	}
}

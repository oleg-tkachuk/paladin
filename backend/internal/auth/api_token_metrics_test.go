package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The verify histogram is emitted from one place and consumed by nobody in
// this repo, so nothing but a collector can tell whether it fires. It once did
// not fire for streaming RPCs at all: the streaming wrapper was a hand-copy of
// the unary one that had never carried the metric line, and every behavioural
// test passed either way. One interceptor function now serves both call
// shapes through apiTokenInterceptor.authenticate, so there is no second copy
// to drift; this holds that a verify, accepted or refused, is metered.
//
// ONE provider for the package, installed at init. OTel's global meter binds
// its delegate once — instruments created against the no-op default before a
// real provider is installed keep delegating to the no-op — and
// initAPITokenMetrics builds them under a sync.Once on first record. A
// package-level var wins that race: Go initialises it before any test runs.
var metricsReader = func() sdkmetric.Reader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

// verifyCountFor returns how many samples the verify histogram holds for one
// tenant. Keyed on tenant rather than on a total, so a parallel test recording
// its own verify cannot make this one pass or fail: the fixtures mint a fresh
// tenant UUID per test, and only the call under test can produce that series.
func verifyCountFor(t *testing.T, tenantID string, ok bool) uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := metricsReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	// recordVerifyDuration omits tenant_id rather than sending it empty, so a
	// failed verify carries `ok` alone. Matching on a set with an empty
	// tenant_id would find nothing and read as "the metric never fired".
	attrs := []attribute.KeyValue{attribute.Bool("ok", ok)}
	if tenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", tenantID))
	}
	want := attribute.NewSet(attrs...)
	var n uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "paladin.api_token.verify.duration_ms" {
				continue
			}
			h, isHist := m.Data.(metricdata.Histogram[float64])
			if !isHist {
				t.Fatalf("%s is %T, want a float64 histogram", m.Name, m.Data)
			}
			for _, dp := range h.DataPoints {
				if dp.Attributes.Equals(&want) {
					n += dp.Count
				}
			}
		}
	}
	return n
}

func TestVerifyIsMetered(t *testing.T) {
	f := newTokenFixture(t)

	t.Run("a verified call", func(t *testing.T) {
		tenant := uuid.New()
		tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
		i := &apiTokenInterceptor{verifier: f.verifier, audience: planeData}

		if got := verifyCountFor(t, tenant.String(), true); got != 0 {
			t.Fatalf("fresh tenant already has %d samples", got)
		}

		c := callProbe(context.Background(), []connect.ServerInterceptor{i.intercept},
			paladin.HeaderAuthorization, bearerPrefix+tok.Plaintext)
		if c.err != nil {
			t.Fatalf("call should be admitted, got %v", c.err)
		}

		if got := verifyCountFor(t, tenant.String(), true); got != 1 {
			t.Errorf("verify samples after one call: got %d, want 1", got)
		}
	})

	// A refused token is metered too, with ok=false and no tenant — the
	// failure rate is the half of this histogram an operator actually alerts
	// on, and it is recorded before the error mapping.
	t.Run("a refused verify", func(t *testing.T) {
		i := &apiTokenInterceptor{verifier: f.verifier, audience: planeData}
		before := verifyCountFor(t, "", false)

		c := callProbe(context.Background(), []connect.ServerInterceptor{i.intercept},
			paladin.HeaderAuthorization, bearerPrefix+unknownPAT)
		if c.err == nil {
			t.Fatal("expected the unknown token to be refused")
		}

		if got := verifyCountFor(t, "", false); got != before+1 {
			t.Errorf("failed-verify samples: got %d, want %d", got, before+1)
		}
	})
}

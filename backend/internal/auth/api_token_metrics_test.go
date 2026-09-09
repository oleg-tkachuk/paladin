package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
)

// The verify histogram is emitted from one place and consumed by nobody in
// this repo, so nothing but a collector can tell whether it fires. It did not
// fire for streaming RPCs at all: the streaming wrapper was a hand-copy of the
// unary one that had never carried the metric line, and every behavioural test
// passed either way. Both wrappers now go through apiTokenInterceptor.
// authenticate, and this is what holds that they do.
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

// Both call kinds in one test, because they are one claim: a verify is
// metered, whatever wrapper reached it. Split across two tests, a regression
// on one path reads as an unrelated failure rather than as the asymmetry it
// would be.
func TestVerifyIsMeteredOnBothCallKinds(t *testing.T) {
	f := newStreamFixture(t)

	t.Run("streaming handler", func(t *testing.T) {
		tenant := uuid.New()
		tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
		i := &apiTokenInterceptor{verifier: f.verifier, audience: "data"}

		if got := verifyCountFor(t, tenant.String(), true); got != 0 {
			t.Fatalf("fresh tenant already has %d samples", got)
		}

		conn := newStreamConn()
		conn.header.Set("Authorization", "Bearer "+tok.Plaintext)
		var called bool
		var seen context.Context
		if err := i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn); err != nil {
			t.Fatalf("stream should be admitted, got %v", err)
		}

		if got := verifyCountFor(t, tenant.String(), true); got != 1 {
			t.Errorf("verify samples after one stream: got %d, want 1", got)
		}
	})

	t.Run("unary handler", func(t *testing.T) {
		tenant := uuid.New()
		tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
		i := &apiTokenInterceptor{verifier: f.verifier, audience: "data"}

		next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			return connect.NewResponse(&emptypb.Empty{}), nil
		}
		if _, err := i.WrapUnary(next)(context.Background(), newReq("Bearer "+tok.Plaintext)); err != nil {
			t.Fatalf("call should be admitted, got %v", err)
		}

		if got := verifyCountFor(t, tenant.String(), true); got != 1 {
			t.Errorf("verify samples after one call: got %d, want 1", got)
		}
	})

	// A refused token is metered too, with ok=false and no tenant — the
	// failure rate is the half of this histogram an operator actually alerts
	// on, and it is recorded before the error mapping on both paths.
	t.Run("a refused verify is metered on the streaming path", func(t *testing.T) {
		i := &apiTokenInterceptor{verifier: f.verifier, audience: "data"}
		before := verifyCountFor(t, "", false)

		conn := newStreamConn()
		conn.header.Set("Authorization", "Bearer "+api_token.TokenPrefix+
			"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		var called bool
		var seen context.Context
		if err := i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn); err == nil {
			t.Fatal("expected the unknown token to be refused")
		}

		if got := verifyCountFor(t, "", false); got != before+1 {
			t.Errorf("failed-verify samples: got %d, want %d", got, before+1)
		}
	})
}

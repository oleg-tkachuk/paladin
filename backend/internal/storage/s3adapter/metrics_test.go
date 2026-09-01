package s3adapter

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The storage-call metric is an SDK middleware, which buys complete coverage
// of every operation — and costs the ability to see, by reading the code, that
// it fires at all. Put it on the wrong step, or hand the SDK a stack mutator
// it silently drops, and the result compiles, the S3 calls work, and the
// counter stays at zero forever.
//
// So this drives a real client against the fake S3 server and collects. It is
// the same argument internal/metrics/domain_test.go makes for the instruments
// themselves: a metric that never reaches a collector is what the dashboards
// would discover instead.

func collectInto(t *testing.T, reader sdkmetric.Reader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func sumFor(rm metricdata.ResourceMetrics, name string) (total int64, attrs []metricdata.DataPoint[int64]) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			if s, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range s.DataPoints {
					total += dp.Value
					attrs = append(attrs, dp)
				}
			}
		}
	}
	return total, attrs
}

// ONE provider for the whole file, installed once.
//
// OTel's global meter delegates to the provider installed after the
// instruments were created — but it binds that delegate ONCE. A second
// SetMeterProvider does not re-point instruments that already delegate, so a
// per-test reader silently collects nothing after the first test. That is not
// a quirk worth working around with a rebind hook in production code; it is
// simpler and more honest to share the reader and assert on what accumulated.
var metricsReader = func() sdkmetric.Reader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

// Both halves in one test, for the reason above and because they are one
// claim: every call this client makes is counted, whether it worked or not. A
// metric that records only successes reports a dead backend as silence, which
// is indistinguishable from an idle one.
func TestStorageCallsAreCounted(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)
	c.SetBackendID("primary")

	if err := c.Probe(testCtx); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	total, points := sumFor(collectInto(t, metricsReader), "paladin_storage_calls_total")
	if total == 0 {
		t.Fatal("a real S3 call recorded nothing — the middleware is not on the stack")
	}
	var sawBackend, sawOp, sawOK bool
	for _, dp := range points {
		if v, ok := dp.Attributes.Value("backend_id"); ok && v.AsString() == "primary" {
			sawBackend = true
		}
		if v, ok := dp.Attributes.Value("op"); ok && v.AsString() != "" {
			sawOp = true
		}
		if v, ok := dp.Attributes.Value("outcome"); ok && v.AsString() == "ok" {
			sawOK = true
		}
	}
	if !sawBackend {
		t.Error("backend_id label missing — the metric cannot say which store")
	}
	if !sawOp {
		t.Error("op label missing — the SDK operation name did not reach the metric")
	}
	if !sawOK {
		t.Error("a successful call was not counted with outcome=ok")
	}

	// Now a failing one, against the same reader.
	fail := newFakeS3(t)
	fail.route = func(w http.ResponseWriter, _ *http.Request, _, _ string) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	fc := newTestClient(t, fail.srv.URL)
	fc.SetBackendID("primary")
	if err := fc.DeleteObject(testCtx, "", testTenant, "coll", "key"); err == nil {
		t.Fatal("expected the 500 to surface as an error")
	}

	_, points = sumFor(collectInto(t, metricsReader), "paladin_storage_calls_total")
	var sawError bool
	for _, dp := range points {
		if v, ok := dp.Attributes.Value("outcome"); ok && v.AsString() == "error" {
			sawError = true
		}
	}
	if !sawError {
		t.Error("a failing S3 call was not counted with outcome=error")
	}
}

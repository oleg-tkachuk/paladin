package objecth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
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

// presignCount sums paladin_presign_total for one op and outcome.
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

// UploadObject and DownloadObject hand out presigned URLs exactly as
// PresignService does, and were the only presigns not counted — so the
// counter missed most uploads and downloads, and a dashboard reading it showed
// an idle store. Counted as deltas, so other tests' presigns cannot make this
// pass or fail.
func TestObjectTransfersAreCountedAsPresigns(t *testing.T) {
	ok := metrics.PresignOutcomeOK

	t.Run("download", func(t *testing.T) {
		h, _, _, ctx := downloadHandler(t, statemachine.StateAvailable)
		before := presignCount(t, metrics.PresignOpGet, ok)
		if _, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "attachment"); err != nil {
			t.Fatalf("DownloadObject: %v", err)
		}
		if got := presignCount(t, metrics.PresignOpGet, ok) - before; got != 1 {
			t.Errorf("a download added %d %s/%s presigns, want 1", got, metrics.PresignOpGet, ok)
		}
	})

	for name, tc := range map[string]struct {
		post bool
		op   string
	}{
		"upload by PUT":  {false, metrics.PresignOpPut},
		"upload by POST": {true, metrics.PresignOpPost},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
			h := &Handler{
				repo:    &uploadRepo{fakeObjectRepo: fakeObjectRepo{meta: BucketMeta{BackendID: "backend-7", BucketName: "bucket-7"}}},
				storage: noopStorage{},
				policy:  &recordingAuthorizer{},
				presign: testPresignConfig(),
			}
			before := presignCount(t, tc.op, ok)
			if _, err := h.UploadObject(ctx, UploadObjectInput{
				Collection: "docs", Key: "a.txt", ContentType: "text/plain", TransportPOST: tc.post,
			}); err != nil {
				t.Fatalf("UploadObject: %v", err)
			}
			if got := presignCount(t, tc.op, ok) - before; got != 1 {
				t.Errorf("an upload added %d %s/%s presigns, want 1", got, tc.op, ok)
			}
		})
	}

	t.Run("a refused download is counted with its code", func(t *testing.T) {
		h, _, _, ctx := downloadHandler(t, statemachine.StateAvailable)
		const refused = "invalid_argument"
		before := presignCount(t, metrics.PresignOpGet, refused)
		if _, err := h.DownloadObject(ctx, "", "", 0, ""); err == nil {
			t.Fatal("a download with no collection was accepted")
		}
		if got := presignCount(t, metrics.PresignOpGet, refused) - before; got != 1 {
			t.Errorf("a refused download added %d %s/%s presigns, want 1", got, metrics.PresignOpGet, refused)
		}
	})
}

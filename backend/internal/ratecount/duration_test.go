package ratecount

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
)

// steppingClock advances by step on every read, so each call to the shared
// store takes exactly step.
type steppingClock struct {
	now  time.Time
	step time.Duration
}

func (c *steppingClock) Now() time.Time {
	t := c.now
	c.now = c.now.Add(c.step)
	return t
}

// storeDurations collects the duration histogram's points, by outcome.
func storeDurations(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	points := map[string]metricdata.HistogramDataPoint[float64]{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != storeDurationMetric {
				continue
			}
			if m.Unit != storeDurationUnit {
				t.Fatalf("unit %q, want %q", m.Unit, storeDurationUnit)
			}
			for _, p := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				backend, _ := p.Attributes.Value(attrBackend)
				if backend.AsString() != "valkey" {
					t.Fatalf("backend attribute %v", backend)
				}
				outcome, _ := p.Attributes.Value(attrOutcome)
				points[outcome.AsString()] = p
			}
		}
	}
	return points
}

// testStoreTimeout is the shared store's timeout the tests give Degrading.
const testStoreTimeout = 100 * time.Millisecond

// bucketOf is the index of the bucket the histogram puts seconds in.
func bucketOf(bounds []float64, seconds float64) int {
	for i, bound := range bounds {
		if seconds <= bound {
			return i
		}
	}
	return len(bounds)
}

// Every call to the shared store is timed, the ones it answered and the ones
// it did not apart: what a call takes against the store's timeout is what
// says whether the timeout or the store is wrong when calls fall back.
func TestDegradingTimesEveryCallToTheSharedStore(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	const step = 40 * time.Millisecond
	ctx := context.Background()
	s := &switchable{inner: NewLocal()}
	d := NewDegrading(s, "valkey", testStoreTimeout, zap.NewNop())
	d.now = (&steppingClock{now: time.Unix(0, 0), step: step}).Now

	_, _ = d.Bump(ctx, testKey)
	_, _ = d.BumpIfFits(ctx, testKey, 10)
	_, _ = d.Read(ctx, testKey)
	s.down = true
	_, _ = d.Bump(ctx, testKey)

	points := storeDurations(t, reader)
	for outcome, want := range map[string]uint64{outcomeAnswered: 3, outcomeFailed: 1} {
		p, ok := points[outcome]
		if !ok {
			t.Fatalf("no %s point; have %v", outcome, points)
		}
		if p.Count != want {
			t.Errorf("%s: %d calls timed, want %d", outcome, p.Count, want)
		}
		if got, wantSum := p.Sum, float64(want)*step.Seconds(); got < wantSum-1e-9 || got > wantSum+1e-9 {
			t.Errorf("%s: %v s timed, want %v", outcome, got, wantSum)
		}
		if p.BucketCounts[bucketOf(storeDurationBuckets(testStoreTimeout), step.Seconds())] != want {
			t.Errorf("%s: buckets %v, want all %d in the one holding %v", outcome, p.BucketCounts, want, step)
		}
	}
}

// The buckets bracket the timeout Degrading is given, with one bound at it:
// how close calls run to the timeout, and how many run past it, read off the
// histogram whatever the timeout is configured to.
func TestStoreDurationBucketsBracketTheTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{50 * time.Millisecond, testStoreTimeout, time.Second} {
		bounds := storeDurationBuckets(timeout)
		if timeout == testStoreTimeout {
			want := []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.25, 0.5, 1}
			if len(bounds) != len(want) {
				t.Fatalf("%v: bounds %v, want %v", timeout, bounds, want)
			}
			for i := range want {
				if bounds[i] != want[i] {
					t.Fatalf("%v: bounds %v, want %v exactly, as their labels read", timeout, bounds, want)
				}
			}
		}
		for i := 1; i < len(bounds); i++ {
			if bounds[i] <= bounds[i-1] {
				t.Fatalf("%v: bounds not ascending at %d: %v", timeout, i, bounds)
			}
		}
		below, at, above := false, false, false
		for _, b := range bounds {
			switch {
			case b < timeout.Seconds():
				below = true
			case b == timeout.Seconds():
				at = true
			default:
				above = true
			}
		}
		if !below || !at || !above {
			t.Errorf("%v: bounds %v do not bracket it", timeout, bounds)
		}
	}
}

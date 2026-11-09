package ratecount

import (
	"context"
	"errors"
	"testing"
	"time"

	"uuid"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
)

// clock is a settable time source for Local.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// bucketAt is the start of a bucket far from the epoch, so tests never sit
// on index zero.
var bucketAt = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

const testKey = "tenant:test"

func TestLocalBumpCountsEveryCall(t *testing.T) {
	c := &clock{t: bucketAt.Add(10 * time.Second)}
	l := newLocal(c.now)
	for i := int64(1); i <= 3; i++ {
		got, _ := l.Bump(context.Background(), testKey)
		if got.Current != i || !got.Admitted {
			t.Fatalf("bump %d: %+v", i, got)
		}
	}
	got, _ := l.Read(context.Background(), testKey)
	if got.Current != 3 || got.Admitted {
		t.Fatalf("read counted or lost: %+v", got)
	}
	if got.Elapsed != 10*time.Second || !got.BucketStart.Equal(bucketAt) {
		t.Fatalf("elapsed %v start %v", got.Elapsed, got.BucketStart)
	}
	if !got.ResetsAt().Equal(bucketAt.Add(Window)) {
		t.Fatalf("resets at %v", got.ResetsAt())
	}
}

func TestLocalRollsBuckets(t *testing.T) {
	tests := []struct {
		name     string
		advance  time.Duration
		wantCur  int64
		wantPrev int64
	}{
		{"same bucket", 30 * time.Second, 3, 0},
		{"next bucket", Window, 0, 2},
		{"two buckets later", 2 * Window, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &clock{t: bucketAt}
			l := newLocal(c.now)
			ctx := context.Background()
			_, _ = l.Bump(ctx, testKey)
			_, _ = l.Bump(ctx, testKey)
			c.t = c.t.Add(tc.advance)
			got, _ := l.Read(ctx, testKey)
			if tc.name == "same bucket" {
				got, _ = l.Bump(ctx, testKey)
			}
			if got.Current != tc.wantCur || got.Previous != tc.wantPrev {
				t.Fatalf("got cur=%d prev=%d, want %d/%d", got.Current, got.Previous, tc.wantCur, tc.wantPrev)
			}
		})
	}
}

func TestLocalBumpIfFitsStopsAtCapacity(t *testing.T) {
	const capacity = 3
	c := &clock{t: bucketAt}
	l := newLocal(c.now)
	ctx := context.Background()
	for i := 0; i < capacity; i++ {
		if got, _ := l.BumpIfFits(ctx, testKey, capacity); !got.Admitted {
			t.Fatalf("call %d refused under capacity", i)
		}
	}
	got, _ := l.BumpIfFits(ctx, testKey, capacity)
	if got.Admitted || got.Current != capacity {
		t.Fatalf("over capacity: %+v", got)
	}
}

// The previous bucket's share fades across the current bucket: four
// requests last minute leave room for one at a quarter in (4 × 0.75 = 3),
// and for three at three quarters in (4 × 0.25 = 1).
func TestLocalBumpIfFitsWeighsPreviousBucket(t *testing.T) {
	const capacity, last = 4, 4
	tests := []struct {
		name  string
		at    time.Duration
		admit int
	}{
		{"a quarter in", Window / 4, 1},
		{"three quarters in", 3 * Window / 4, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &clock{t: bucketAt}
			l := newLocal(c.now)
			ctx := context.Background()
			for i := 0; i < last; i++ {
				_, _ = l.Bump(ctx, testKey)
			}
			c.t = bucketAt.Add(Window + tc.at)
			admitted := 0
			for i := 0; i < capacity; i++ {
				if got, _ := l.BumpIfFits(ctx, testKey, capacity); got.Admitted {
					admitted++
				}
			}
			if admitted != tc.admit {
				t.Fatalf("admitted %d, want %d", admitted, tc.admit)
			}
		})
	}
}

func TestLocalPrunesIdleKeys(t *testing.T) {
	c := &clock{t: bucketAt}
	l := newLocal(c.now)
	ctx := context.Background()
	_, _ = l.Bump(ctx, "idle")
	c.t = c.t.Add(2 * Window)
	for i := 0; i < pruneEvery; i++ {
		_, _ = l.Read(ctx, "busy")
	}
	if _, ok := l.keys["idle"]; ok {
		t.Fatal("idle key survived a prune")
	}
	if _, ok := l.keys["busy"]; !ok {
		t.Fatal("busy key pruned")
	}
}

func TestKeysKeepTenantsAndTokensApart(t *testing.T) {
	id := uuid.New()
	if TenantKey(id) == TokenKey(id) {
		t.Fatal("tenant and token share a key")
	}
}

// failing answers every call with err.
type failing struct{ err error }

func (f failing) Bump(context.Context, string) (Counts, error)            { return Counts{}, f.err }
func (f failing) BumpIfFits(context.Context, string, int) (Counts, error) { return Counts{}, f.err }
func (f failing) Read(context.Context, string) (Counts, error)            { return Counts{}, f.err }

// switchable fails while down is set and otherwise counts locally.
type switchable struct {
	down  bool
	inner *Local
}

func (s *switchable) Bump(ctx context.Context, k string) (Counts, error) {
	if s.down {
		return Counts{}, errors.New("down")
	}
	return s.inner.Bump(ctx, k)
}
func (s *switchable) BumpIfFits(ctx context.Context, k string, c int) (Counts, error) {
	if s.down {
		return Counts{}, errors.New("down")
	}
	return s.inner.BumpIfFits(ctx, k, c)
}
func (s *switchable) Read(ctx context.Context, k string) (Counts, error) {
	if s.down {
		return Counts{}, errors.New("down")
	}
	return s.inner.Read(ctx, k)
}

func TestDegradingKeepsTheLimitWhenTheStoreFails(t *testing.T) {
	const capacity = 2
	d := NewDegrading(failing{errors.New("boom")}, "valkey", zap.NewNop())
	ctx := context.Background()
	admitted := 0
	for i := 0; i < capacity+1; i++ {
		c, err := d.BumpIfFits(ctx, testKey, capacity)
		if err != nil {
			t.Fatalf("degraded call returned %v", err)
		}
		if c.Admitted {
			admitted++
		}
	}
	if admitted != capacity {
		t.Fatalf("admitted %d with the store down, want %d", admitted, capacity)
	}
	if c, _ := d.Bump(ctx, testKey); c.Current != capacity+1 {
		t.Fatalf("bump fell back to %+v", c)
	}
	if c, _ := d.Read(ctx, testKey); c.Current != capacity+1 {
		t.Fatalf("read fell back to %+v", c)
	}
}

func TestDegradingLogsEachSwitchOnce(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	s := &switchable{inner: NewLocal()}
	d := NewDegrading(s, "valkey", zap.New(core))
	ctx := context.Background()

	_, _ = d.Bump(ctx, testKey)
	if logs.Len() != 0 {
		t.Fatalf("healthy store logged: %v", logs.All())
	}
	s.down = true
	_, _ = d.Bump(ctx, testKey)
	_, _ = d.Bump(ctx, testKey)
	if got := logs.FilterLevelExact(zapcore.WarnLevel).Len(); got != 1 {
		t.Fatalf("warned %d times for one outage", got)
	}
	s.down = false
	_, _ = d.Bump(ctx, testKey)
	_, _ = d.Bump(ctx, testKey)
	if got := logs.FilterLevelExact(zapcore.InfoLevel).Len(); got != 1 {
		t.Fatalf("logged recovery %d times", got)
	}
}

func TestTenantStoreReportsWeightedCountAndRetry(t *testing.T) {
	c := &clock{t: bucketAt.Add(15 * time.Second)}
	s := NewTenantStore(newLocal(c.now))
	id := uuid.New()
	_, _, _ = s.BumpTenantRate(context.Background(), id)
	weighted, retry, err := s.BumpTenantRate(context.Background(), id)
	if err != nil || weighted != 2 || retry != 45 {
		t.Fatalf("weighted=%v retry=%v err=%v", weighted, retry, err)
	}
	if _, _, err := NewTenantStore(failing{errors.New("x")}).BumpTenantRate(context.Background(), id); err == nil {
		t.Fatal("store error swallowed")
	}
}

func TestTokenLimiterMatchesTheRetryContract(t *testing.T) {
	const capacity = 2
	c := &clock{t: bucketAt.Add(20 * time.Second)}
	l := NewTokenLimiter(newLocal(c.now))
	ctx := context.Background()
	id := uuid.New()

	if d, _ := l.Allow(ctx, id, 0); !d.Allowed {
		t.Fatal("an uncapped token was refused")
	}
	for i := 0; i < capacity; i++ {
		if d, _ := l.Allow(ctx, id, capacity); !d.Allowed {
			t.Fatalf("call %d refused", i)
		}
	}
	d, err := l.Allow(ctx, id, capacity)
	if err != nil || d.Allowed {
		t.Fatalf("over capacity admitted: %+v %v", d, err)
	}
	if want := ratelimit.RetryAfter(capacity, 0, 20*time.Second, capacity); d.RetryAfter != want {
		t.Fatalf("retry %v, want %v", d.RetryAfter, want)
	}
	if d.WeightedCount != capacity+1 {
		t.Fatalf("denied weighted %v", d.WeightedCount)
	}
	snap, err := l.Usage(ctx, id)
	if err != nil || snap.CurrentBucketCount != capacity || !snap.WindowResetsAt.Equal(bucketAt.Add(Window)) {
		t.Fatalf("usage %+v %v", snap, err)
	}
	if _, err := NewTokenLimiter(failing{errors.New("x")}).Allow(ctx, id, capacity); err == nil {
		t.Fatal("allow swallowed a store error")
	}
	if _, err := NewTokenLimiter(failing{errors.New("x")}).Usage(ctx, id); err == nil {
		t.Fatal("usage swallowed a store error")
	}
}

// A key whose previous bucket still counts survives a prune: dropping it
// would forget the requests the window still weighs.
func TestLocalPruneKeepsThePreviousBucket(t *testing.T) {
	c := &clock{t: bucketAt}
	l := newLocal(c.now)
	ctx := context.Background()
	_, _ = l.Bump(ctx, "recent")
	_, _ = l.Bump(ctx, "recent")
	c.t = c.t.Add(Window)
	for i := 0; i < pruneEvery; i++ {
		_, _ = l.Read(ctx, "busy")
	}
	if got, _ := l.Read(ctx, "recent"); got.Previous != 2 {
		t.Fatalf("pruned a key still in the window: %+v", got)
	}
}

func TestTokenLimiterCapacityOfOne(t *testing.T) {
	l := NewTokenLimiter(NewLocal())
	ctx := context.Background()
	id := uuid.New()
	if d, _ := l.Allow(ctx, id, 1); !d.Allowed {
		t.Fatal("first call refused")
	}
	if d, _ := l.Allow(ctx, id, 1); d.Allowed {
		t.Fatal("a capacity of one admitted two calls")
	}
}

func TestDegradingCountsEachLocalDecision(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	d := NewDegrading(failing{errors.New("down")}, "valkey", zap.NewNop())
	ctx := context.Background()
	const calls = 3
	for i := 0; i < calls; i++ {
		_, _ = d.Bump(ctx, testKey)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != degradedMetric {
				continue
			}
			sum := m.Data.(metricdata.Sum[int64])
			if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != calls {
				t.Fatalf("degraded points %+v", sum.DataPoints)
			}
			if v, _ := sum.DataPoints[0].Attributes.Value("backend"); v.AsString() != "valkey" {
				t.Fatalf("backend attribute %v", v)
			}
			return
		}
	}
	t.Fatal("no degraded metric recorded")
}

// pingable is a shared store that answers Ping with err.
type pingable struct {
	failing
	pingErr error
}

func (p pingable) Ping(context.Context) error { return p.pingErr }

func TestDegradingStatsFollowTheStore(t *testing.T) {
	s := &switchable{inner: NewLocal()}
	d := NewDegrading(s, "valkey", zap.NewNop())
	ctx := context.Background()

	_, _ = d.Bump(ctx, testKey)
	if st := d.Stats(); st.PerProcess || st.DecidedPerProcess != 0 {
		t.Fatalf("answering store: stats %+v", st)
	}
	s.down = true
	const outage = 3
	for i := 0; i < outage; i++ {
		_, _ = d.Bump(ctx, testKey)
	}
	if st := d.Stats(); !st.PerProcess || st.DecidedPerProcess != outage {
		t.Fatalf("store down: stats %+v, want per process with %d decided", st, outage)
	}
	s.down = false
	_, _ = d.Bump(ctx, testKey)
	if st := d.Stats(); st.PerProcess || st.DecidedPerProcess != outage {
		t.Fatalf("store back: stats %+v, want shared and %d decided so far", st, outage)
	}
}

func TestDegradingPingAsksTheStore(t *testing.T) {
	down := errors.New("refused")
	cases := []struct {
		name   string
		shared Counter
		want   error
	}{
		{"store answers", pingable{}, nil},
		{"store refuses", pingable{pingErr: down}, down},
		{"store cannot be pinged", failing{}, ErrNoPing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewDegrading(tc.shared, "valkey", zap.NewNop()).Ping(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Ping = %v, want %v", err, tc.want)
			}
		})
	}
}

// fixed answers every call with counts.
type fixed struct{ counts Counts }

func (f fixed) Bump(context.Context, string) (Counts, error)            { return f.counts, nil }
func (f fixed) BumpIfFits(context.Context, string, int) (Counts, error) { return f.counts, nil }
func (f fixed) Read(context.Context, string) (Counts, error)            { return f.counts, nil }

// While the store answers, its counts decide: the process's own are not
// consulted.
func TestDegradingReturnsTheStoresAnswer(t *testing.T) {
	const shared = 42
	d := NewDegrading(fixed{Counts{Current: shared}}, "valkey", zap.NewNop())
	ctx := context.Background()
	calls := map[string]func() (Counts, error){
		"bump":         func() (Counts, error) { return d.Bump(ctx, testKey) },
		"bump if fits": func() (Counts, error) { return d.BumpIfFits(ctx, testKey, 1) },
		"read":         func() (Counts, error) { return d.Read(ctx, testKey) },
	}
	for name, call := range calls {
		if c, err := call(); err != nil || c.Current != shared {
			t.Errorf("%s = %+v, %v; want the store's count %d", name, c, err, shared)
		}
	}
}

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type fakeSlugExpirer struct {
	expired []ExpiredSlug
	err     error
	calls   int
	before  time.Time
}

func (f *fakeSlugExpirer) ExpireRetiredSlugs(_ context.Context, retiredBefore time.Time) ([]ExpiredSlug, error) {
	f.calls++
	f.before = retiredBefore
	return f.expired, f.err
}

func observed() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// No period: every retired slug stays until an operator releases it. The job
// says so once and never touches the store.
func TestSlugReservationExpirerWithoutPeriodKeepsEverything(t *testing.T) {
	store := &fakeSlugExpirer{}
	l, logs := observed()
	e := &SlugReservationExpirer{Store: store, Interval: time.Millisecond, Logger: l}
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("store called %d times without a period", store.calls)
	}
	if logs.FilterMessageSnippet("stay reserved until released").Len() != 1 {
		t.Fatalf("no line saying the slugs are kept: %v", logs.All())
	}
}

// A tick releases what was retired a full period ago, and names each slug and
// its former tenant in the log.
func TestSlugReservationExpirerReleasesWhatThePeriodCovers(t *testing.T) {
	const period = 90 * 24 * time.Hour
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	retired := now.Add(-period - time.Hour)
	store := &fakeSlugExpirer{expired: []ExpiredSlug{
		{Slug: "acme", FormerTenantID: "01a1e6cd-2efe-7a61-a78e-5ecad091c778", RetiredAt: retired},
		{Slug: "globex", FormerTenantID: "01a1e6cd-2efe-7a61-a78e-5ecad091c779", RetiredAt: retired},
	}}
	l, logs := observed()
	p := period
	e := &SlugReservationExpirer{Store: store, Period: &p, Logger: l, Now: func() time.Time { return now }}

	if err := e.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if want := now.Add(-period); !store.before.Equal(want) {
		t.Fatalf("released slugs retired before %v, want %v", store.before, want)
	}
	lines := logs.FilterMessage("tenant slug reservation expired, slug released").All()
	if len(lines) != len(store.expired) {
		t.Fatalf("%d release lines, want %d", len(lines), len(store.expired))
	}
	for i, line := range lines {
		f := line.ContextMap()
		if f["slug"] != store.expired[i].Slug || f["former_tenant_id"] != store.expired[i].FormerTenantID {
			t.Errorf("line %d = %v, want slug %s of %s", i, f, store.expired[i].Slug, store.expired[i].FormerTenantID)
		}
	}
}

// A failed release is logged at error and returned, for the tick metrics.
func TestSlugReservationExpirerReportsAStoreFailure(t *testing.T) {
	boom := errors.New("connection refused")
	l, logs := observed()
	p := time.Hour
	e := &SlugReservationExpirer{Store: &fakeSlugExpirer{err: boom}, Period: &p, Logger: l}
	if err := e.tick(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("tick = %v, want %v", err, boom)
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Fatalf("want one error line, got %v", logs.All())
	}
}

// With a period, Run says what it runs with and ticks until cancelled.
func TestSlugReservationExpirerRunsUntilCancelled(t *testing.T) {
	store := &fakeSlugExpirer{}
	l, logs := observed()
	p := time.Hour
	e := &SlugReservationExpirer{Store: store, Period: &p, Interval: time.Millisecond, Logger: l}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	deadline := time.After(5 * time.Second)
	for logs.FilterMessage("slug reservation expirer started").Len() == 0 {
		select {
		case <-deadline:
			t.Fatal("never started")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
}

// Without an interval the job cannot tick, and says so rather than going
// quiet.
func TestSlugReservationExpirerWithoutIntervalSaysSo(t *testing.T) {
	l, logs := observed()
	p := time.Hour
	e := &SlugReservationExpirer{Store: &fakeSlugExpirer{}, Period: &p, Logger: l}
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
		t.Fatalf("want one warning, got %v", logs.All())
	}
}

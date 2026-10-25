package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// fullBatch is a housekeeping batch that filled the limit, which tells the
// purger more rows may be waiting.
const fullBatch = int64(limes.DefaultHousekeepingLimit)

// shortBatch is a housekeeping batch short of the limit: the last one.
const shortBatch int64 = 2

// batches hands out the sizes in turn, then 0; err fails every call.
type batches struct {
	sizes []int64
	err   error
	calls int
}

func (b *batches) next() (int64, error) {
	b.calls++
	if b.err != nil {
		return 0, b.err
	}
	if len(b.sizes) == 0 {
		return 0, nil
	}
	n := b.sizes[0]
	b.sizes = b.sizes[1:]
	return n, nil
}

// purgingStore answers Purge from purged and records each request; any other
// Store method panics through the nil embedded interface, which the purger
// must never call.
type purgingStore struct {
	limes.Store
	purged   batches
	requests []limes.PurgeRequest
	ctx      context.Context
}

func (s *purgingStore) Purge(ctx context.Context, req limes.PurgeRequest) (limes.PurgeResult, error) {
	s.ctx = ctx
	s.requests = append(s.requests, req)
	n, err := s.purged.next()
	return limes.PurgeResult{Purged: n}, err
}

// fakeReplayPurger hands out the batch sizes in batches, then 0.
type fakeReplayPurger struct {
	batches []int64
	err     error
	calls   int
}

func (f *fakeReplayPurger) PurgeExpired(context.Context) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	if len(f.batches) == 0 {
		return 0, nil
	}
	n := f.batches[0]
	f.batches = f.batches[1:]
	return n, nil
}

func TestCapabilityPurgerDrainsExpiredDPoPProofIDs(t *testing.T) {
	replay := &fakeReplayPurger{batches: []int64{10, 3}}
	p := &CapabilityPurger{Store: &purgingStore{}, Replay: replay}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if replay.calls != 3 {
		t.Fatalf("replay purge called %d times, want 3 (two batches, then the empty one)", replay.calls)
	}
}

func TestCapabilityPurgerReportsAFailedDPoPPurge(t *testing.T) {
	failure := errors.New("database unreachable")
	replay := &fakeReplayPurger{err: failure}
	p := &CapabilityPurger{Store: &purgingStore{}, Replay: replay}
	if err := p.tick(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("tick: err = %v, want %v", err, failure)
	}
	if replay.calls != 1 {
		t.Fatalf("replay purge called %d times after a failure, want 1", replay.calls)
	}
}

func TestCapabilityPurgerWithoutReplayCache(t *testing.T) {
	p := &CapabilityPurger{Store: &purgingStore{}}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// The record purge carries the configured retention and loops while a batch
// fills the limit; the first short batch is the last.
func TestCapabilityPurgerPurgesRecordsInBatches(t *testing.T) {
	const retention = 48 * time.Hour
	cases := map[string]struct {
		sizes     []int64
		wantCalls int
	}{
		"nothing to purge":            {nil, 1},
		"one short batch":             {[]int64{shortBatch}, 1},
		"full then short":             {[]int64{fullBatch, shortBatch}, 2},
		"two full then the empty one": {[]int64{fullBatch, fullBatch}, 3},
		"one under the limit is last": {[]int64{fullBatch - 1, fullBatch}, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &purgingStore{purged: batches{sizes: tc.sizes}}
			p := &CapabilityPurger{Store: store, ExpiredFor: retention}
			if err := p.tick(context.Background()); err != nil {
				t.Fatalf("tick: %v", err)
			}
			if store.purged.calls != tc.wantCalls {
				t.Fatalf("Purge called %d times, want %d", store.purged.calls, tc.wantCalls)
			}
			for i, req := range store.requests {
				if req.ExpiredFor != retention {
					t.Errorf("request %d: ExpiredFor = %v, want %v", i, req.ExpiredFor, retention)
				}
			}
		})
	}
}

// A failed record purge stops the tick before the usage sweeps: a store that
// cannot purge is unlikely to sweep, and the error is reported.
func TestCapabilityPurgerReportsAFailedRecordPurge(t *testing.T) {
	failure := errors.New("database unreachable")
	store := &purgingStore{purged: batches{err: failure}}
	usage := &sweepingUsage{}
	p := &CapabilityPurger{Store: store, Usage: usage}
	if err := p.tick(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("tick: err = %v, want %v", err, failure)
	}
	if store.purged.calls != 1 {
		t.Errorf("Purge called %d times after a failure, want 1", store.purged.calls)
	}
	if usage.release.calls != 0 || usage.orphans.calls != 0 {
		t.Errorf("usage swept after a failed purge: %d / %d calls", usage.release.calls, usage.orphans.calls)
	}
}

// sweepingUsage answers the two sweeps the purger makes; any other method
// panics through the nil embedded interface.
type sweepingUsage struct {
	meter.UsageStore
	release, orphans batches
}

func (u *sweepingUsage) ReleaseExpired(context.Context, meter.ReleaseExpiredRequest) (meter.ReleaseExpiredResult, error) {
	n, err := u.release.next()
	return meter.ReleaseExpiredResult{Released: n}, err
}

func (u *sweepingUsage) PurgeOrphans(context.Context, meter.PurgeOrphansRequest) (meter.PurgeOrphansResult, error) {
	n, err := u.orphans.next()
	return meter.PurgeOrphansResult{Purged: n}, err
}

// Both usage sweeps loop while a batch fills the limit and stop on the first
// short one, without asking again for the empty batch.
func TestCapabilityPurgerDrainsBothUsageSweeps(t *testing.T) {
	usage := &sweepingUsage{
		release: batches{sizes: []int64{fullBatch, shortBatch}},
		orphans: batches{sizes: []int64{shortBatch}},
	}
	p := &CapabilityPurger{Store: &purgingStore{}, Usage: usage}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if usage.release.calls != 2 || usage.orphans.calls != 1 {
		t.Errorf("release calls = %d (want 2), orphan calls = %d (want 1)", usage.release.calls, usage.orphans.calls)
	}
}

// A failed sweep stops its own loop and is reported, and the other sweep
// still runs.
func TestCapabilityPurgerReportsAFailedUsageSweep(t *testing.T) {
	failure := errors.New("database unreachable")
	for name, usage := range map[string]*sweepingUsage{
		"release": {release: batches{err: failure}, orphans: batches{sizes: []int64{fullBatch}}},
		"orphans": {release: batches{sizes: []int64{fullBatch}}, orphans: batches{err: failure}},
	} {
		p := &CapabilityPurger{Store: &purgingStore{}, Usage: usage}
		if err := p.tick(context.Background()); !errors.Is(err, failure) {
			t.Errorf("%s: err = %v, want the sweep's", name, err)
		}
		if usage.release.calls == 0 || usage.orphans.calls == 0 {
			t.Errorf("%s: a failed sweep stopped the other one", name)
		}
		if usage.release.calls > 2 || usage.orphans.calls > 2 {
			t.Errorf("%s: a failed sweep kept looping: %d / %d calls", name, usage.release.calls, usage.orphans.calls)
		}
	}
}

// A pass that purged nothing logs nothing; one that purged rows says how many.
func TestCapabilityPurgerLogsOnlyWhatItPurged(t *testing.T) {
	const purgedLog = "purged expired capabilities"
	for n, want := range map[int64]int{0: 0, shortBatch: 1} {
		core, logs := observer.New(zap.InfoLevel)
		store := &purgingStore{purged: batches{sizes: []int64{n}}}
		p := &CapabilityPurger{Store: store, Logger: zap.New(core)}
		if err := p.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := logs.FilterMessage(purgedLog).Len(); got != want {
			t.Errorf("%d purged: %d log lines, want %d", n, got, want)
		}
	}
}

// Unset, the retention defaults before the loop starts. (Interval's own
// `<= 0` guard is equivalent to `< 0`: RunTicker refuses a zero interval the
// same way.)
func TestCapabilityPurgerDefaultsItsRetention(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &CapabilityPurger{Store: &purgingStore{}, Interval: time.Hour}
	_ = p.Run(ctx)
	if p.ExpiredFor != DefaultCapabilityExpiredFor {
		t.Errorf("ExpiredFor = %v, want %v", p.ExpiredFor, DefaultCapabilityExpiredFor)
	}
}

// allTenantsSetting is pgstore's setting for a transaction scoped to every
// tenant, and allTenantsWrite its value for reading and writing them.
const (
	allTenantsSetting = "limes.all_tenants"
	allTenantsWrite   = "on"
)

// settingsTx records the settings a scope sets; any other Tx method panics
// through the nil embedded interface.
type settingsTx struct {
	pgx.Tx
	set map[string]string
}

// Exec records each name and value pair of a set_config statement.
func (s *settingsTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	for i := 0; i+1 < len(args); i += 2 {
		s.set[args[i].(string)] = args[i+1].(string)
	}
	return pgconn.CommandTag{}, nil
}

// Purging removes rows of every tenant, so the purger's calls are scoped to
// write every tenant: a platform-wide read would remove nothing and report
// success.
func TestCapabilityPurgerWritesEveryTenant(t *testing.T) {
	store := &purgingStore{}
	p := &CapabilityPurger{Store: store}
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	tx := &settingsTx{set: map[string]string{}}
	if err := capabilitypg.Scope(store.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if got := tx.set[allTenantsSetting]; got != allTenantsWrite {
		t.Errorf("%s = %q, want %q", allTenantsSetting, got, allTenantsWrite)
	}
}

// purgingLedger answers PurgeCharges from purged and records each request
// and its context.
type purgingLedger struct {
	purged   batches
	requests []meter.PurgeChargesRequest
	ctx      context.Context
}

func (l *purgingLedger) PurgeCharges(ctx context.Context, req meter.PurgeChargesRequest) (meter.PurgeChargesResult, error) {
	l.ctx = ctx
	l.requests = append(l.requests, req)
	n, err := l.purged.next()
	return meter.PurgeChargesResult{Purged: n}, err
}

// Wired, the ledger's old charges go in batches, with the configured
// retention, across every tenant; a failure is the tick's.
func TestCapabilityPurgerPurgesOldCharges(t *testing.T) {
	retention := meter.MinChargeRetention + 24*time.Hour
	ledger := &purgingLedger{purged: batches{sizes: []int64{fullBatch, shortBatch}}}
	p := &CapabilityPurger{Store: &purgingStore{}, Ledger: ledger, ChargesOlderThan: retention}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if ledger.purged.calls != 2 {
		t.Fatalf("PurgeCharges called %d times, want 2 (a full batch, then a short one)", ledger.purged.calls)
	}
	for i, req := range ledger.requests {
		if req.OlderThan != retention {
			t.Errorf("request %d: OlderThan = %v, want %v", i, req.OlderThan, retention)
		}
	}
	tx := &settingsTx{set: map[string]string{}}
	if err := capabilitypg.Scope(ledger.ctx, tx); err != nil {
		t.Fatal(err)
	}
	if got := tx.set[allTenantsSetting]; got != allTenantsWrite {
		t.Errorf("charges purged under %s = %q, want %q: under no tenant pgstore refuses", allTenantsSetting, got, allTenantsWrite)
	}

	failure := errors.New("database unreachable")
	failing := &purgingLedger{purged: batches{err: failure}}
	p = &CapabilityPurger{Store: &purgingStore{}, Ledger: failing}
	if err := p.tick(context.Background()); !errors.Is(err, failure) || failing.purged.calls != 1 {
		t.Errorf("a failed purge: err = %v after %d calls, want %v once", err, failing.purged.calls, failure)
	}
}

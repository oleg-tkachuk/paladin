package worker

import (
	"context"
	"sync"
	"testing"
	"time"
	"uuid"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// ─── RefreshTokenPurger ─────────────────────────────────────────────────────

type fakeRefreshRepo struct {
	mu      sync.Mutex
	purged  []time.Time
	purgeN  int64
	purgErr error
}

func (f *fakeRefreshRepo) Insert(context.Context, authstore.RefreshToken) error { return nil }
func (f *fakeRefreshRepo) Get(context.Context, uuid.UUID) (authstore.RefreshToken, error) {
	return authstore.RefreshToken{}, authstore.ErrNotFound
}
func (f *fakeRefreshRepo) Revoke(context.Context, uuid.UUID) error  { return nil }
func (*fakeRefreshRepo) Supersede(context.Context, uuid.UUID) error { return nil }
func (*fakeRefreshRepo) GetAny(context.Context, uuid.UUID) (authstore.RefreshToken, error) {
	return authstore.RefreshToken{}, nil
}
func (f *fakeRefreshRepo) RevokeForUser(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (f *fakeRefreshRepo) RevokeFamilyOf(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeRefreshRepo) MarkUsed(context.Context, uuid.UUID) error { return nil }
func (f *fakeRefreshRepo) UnusedSuccessor(context.Context, uuid.UUID) (authstore.RefreshToken, error) {
	return authstore.RefreshToken{}, authstore.ErrNotFound
}
func (f *fakeRefreshRepo) PurgeExpired(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purged = append(f.purged, before)
	return f.purgeN, f.purgErr
}

func TestRefreshTokenPurgerTicks(t *testing.T) {
	repo := &fakeRefreshRepo{purgeN: 5}
	p := &RefreshTokenPurger{Repo: repo, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = p.Run(ctx)
		close(done)
	}()
	// Wait for at least 2 ticks.
	time.Sleep(15 * time.Millisecond)
	cancel()
	<-done

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.purged) < 2 {
		t.Errorf("expected ≥2 purges, got %d", len(repo.purged))
	}
}

// ─── AuditLogPurger ─────────────────────────────────────────────────────────

type fakeAuditPurger struct {
	mu      sync.Mutex
	cutoffs []time.Time
	// purged, when set, receives each cutoff as it is purged, so a test
	// waits for the ticks themselves rather than for a stretch of time.
	purged chan time.Time
}

func (f *fakeAuditPurger) PurgeOlderThan(ctx context.Context, c time.Time) (int64, error) {
	f.mu.Lock()
	f.cutoffs = append(f.cutoffs, c)
	f.mu.Unlock()
	if f.purged != nil {
		// A tick after the test stopped reading must not hold Run open.
		select {
		case f.purged <- c:
		case <-ctx.Done():
		}
	}
	return 7, nil
}

func TestAuditLogPurgerDisabledWhenTTLZero(t *testing.T) {
	p := &AuditLogPurger{Purger: &fakeAuditPurger{}, TTL: 0, Interval: 5 * time.Millisecond}
	if err := p.Run(context.Background()); err != nil {
		t.Errorf("expected nil for disabled, got %v", err)
	}
}

// auditPurgerTicksWanted is how many purges show the purger ticks, not
// merely runs once; auditPurgerTickTimeout bounds the wait for them, far
// above the interval so a loaded machine cannot fail it.
const (
	auditPurgerTicksWanted = 2
	auditPurgerTickTimeout = 10 * time.Second
	auditPurgerTTL         = 24 * time.Hour
	auditPurgerCutoffSlack = time.Hour
)

func TestAuditLogPurgerTicks(t *testing.T) {
	purger := &fakeAuditPurger{purged: make(chan time.Time)}
	p := &AuditLogPurger{Purger: purger, TTL: auditPurgerTTL, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = p.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	timeout := time.After(auditPurgerTickTimeout)
	for i := range auditPurgerTicksWanted {
		select {
		case c := <-purger.purged:
			// The cutoff is now less the TTL.
			if age := time.Since(c); age < auditPurgerTTL-auditPurgerCutoffSlack || age > auditPurgerTTL+auditPurgerCutoffSlack {
				t.Errorf("purge %d: cutoff age %v, want about %v", i, age, auditPurgerTTL)
			}
		case <-timeout:
			t.Fatalf("purges = %d within %v, want %d", i, auditPurgerTickTimeout, auditPurgerTicksWanted)
		}
	}
}

package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
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
func (f *fakeRefreshRepo) Revoke(context.Context, uuid.UUID) error                 { return nil }
func (f *fakeRefreshRepo) RevokeForUser(context.Context, uuid.UUID) (int64, error) { return 0, nil }
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

// ─── ApiKeyExpirer ──────────────────────────────────────────────────────────

type fakeApiKeyExpirerRepo struct {
	mu      sync.Mutex
	keys    []authstore.ApiKey
	revoked []uuid.UUID
	listErr error
}

func (f *fakeApiKeyExpirerRepo) ListExpired(_ context.Context, _ time.Time, _ int32) ([]authstore.ApiKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keys, f.listErr
}
func (f *fakeApiKeyExpirerRepo) Revoke(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, id)
	return nil
}

func TestApiKeyExpirerRevokesExpired(t *testing.T) {
	id1 := uuid.Must(uuid.NewV7())
	id2 := uuid.Must(uuid.NewV7())
	repo := &fakeApiKeyExpirerRepo{
		keys: []authstore.ApiKey{{ApiKeyID: id1}, {ApiKeyID: id2}},
	}
	e := &ApiKeyExpirer{Repo: repo, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.Run(ctx); close(done) }()
	time.Sleep(15 * time.Millisecond)
	cancel()
	<-done

	repo.mu.Lock()
	defer repo.mu.Unlock()
	// Each tick revokes both keys; we expect ≥2 revocations.
	if len(repo.revoked) < 2 {
		t.Errorf("expected ≥2 revocations, got %d", len(repo.revoked))
	}
}

func TestApiKeyExpirerListErrorContinues(t *testing.T) {
	repo := &fakeApiKeyExpirerRepo{listErr: errors.New("transient")}
	e := &ApiKeyExpirer{Repo: repo, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.Run(ctx); close(done) }()
	time.Sleep(15 * time.Millisecond)
	cancel()
	<-done
	// No panic, no exit — survival of transient errors is the contract.
}

// ─── AuditLogPurger ─────────────────────────────────────────────────────────

type fakeAuditPurger struct {
	mu      sync.Mutex
	cutoffs []time.Time
}

func (f *fakeAuditPurger) PurgeOlderThan(_ context.Context, c time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, c)
	return 7, nil
}

func TestAuditLogPurgerDisabledWhenTTLZero(t *testing.T) {
	p := &AuditLogPurger{Purger: &fakeAuditPurger{}, TTL: 0, Interval: 5 * time.Millisecond}
	if err := p.Run(context.Background()); err != nil {
		t.Errorf("expected nil for disabled, got %v", err)
	}
}

func TestAuditLogPurgerTicks(t *testing.T) {
	purger := &fakeAuditPurger{}
	p := &AuditLogPurger{Purger: purger, TTL: 24 * time.Hour, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = p.Run(ctx); close(done) }()
	time.Sleep(15 * time.Millisecond)
	cancel()
	<-done
	purger.mu.Lock()
	defer purger.mu.Unlock()
	if len(purger.cutoffs) < 2 {
		t.Errorf("expected ≥2 ticks, got %d", len(purger.cutoffs))
	}
	// Cutoff should be approximately now-TTL.
	for _, c := range purger.cutoffs {
		age := time.Since(c)
		if age < 23*time.Hour || age > 25*time.Hour {
			t.Errorf("cutoff age out of band: %v", age)
		}
	}
}

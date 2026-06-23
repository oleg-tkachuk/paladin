package lease

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// newRenewerLease builds a Lease with a nil pool — the renewer no longer
// touches the pool directly (claim is injected), so unit tests run
// without Postgres.
func newRenewerLease(renew time.Duration) *Lease {
	return &Lease{cfg: Config{
		Name:          "t",
		HolderID:      uuid.New(),
		Logger:        zap.NewNop(),
		TTL:           10 * time.Second,
		RenewInterval: renew,
	}}
}

// Two consecutive failed renewals must cancel the work context (free the
// leader slot) rather than keep pretending we hold the lease.
func TestRenewerCancelsAfterTwoConsecutiveFailures(t *testing.T) {
	l := newRenewerLease(5 * time.Millisecond)
	workCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	var calls int
	var mu sync.Mutex
	claim := func(context.Context) (int64, time.Time, bool, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return 0, time.Time{}, false, errors.New("db down")
	}

	// initialExp far in the future so the EXPIRY timer doesn't fire first —
	// we want the consecutive-failure path, not the deadline path.
	go l.renewer(workCtx, cancel, time.Now().Add(time.Hour), done, claim)

	select {
	case <-workCtx.Done(): // cancelWork() was called by the renewer
	case <-time.After(2 * time.Second):
		t.Fatal("renewer did not cancel work after consecutive failures")
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Errorf("expected >=2 claim attempts before cancel, got %d", calls)
	}
}

// If renewals never land, the in-process expiry deadline must fire and
// stop the work even without two ticks — the deadline path.
func TestRenewerCancelsOnExpiryWithoutRenewal(t *testing.T) {
	l := newRenewerLease(time.Hour) // ticker won't fire during the test
	workCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	claim := func(context.Context) (int64, time.Time, bool, error) {
		return 0, time.Time{}, true, nil // never actually called
	}
	// initialExp already passed → expiry timer fires immediately.
	go l.renewer(workCtx, cancel, time.Now().Add(20*time.Millisecond), done, claim)

	select {
	case <-workCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("renewer did not cancel work when the deadline lapsed")
	}
	<-done
}

// A successful renewal pushes the deadline forward — the renewer keeps
// running (does not cancel) across several ticks.
func TestRenewerSurvivesHealthyRenewals(t *testing.T) {
	l := newRenewerLease(5 * time.Millisecond)
	workCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	claim := func(context.Context) (int64, time.Time, bool, error) {
		return 1, time.Now().Add(time.Hour), true, nil // always renews
	}
	go l.renewer(workCtx, cancel, time.Now().Add(50*time.Millisecond), done, claim)

	// Should still be alive well past the initial deadline.
	select {
	case <-workCtx.Done():
		t.Fatal("renewer cancelled despite healthy renewals")
	case <-time.After(150 * time.Millisecond):
	}
	cancel() // stop it
	<-done
}

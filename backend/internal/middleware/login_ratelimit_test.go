package middleware

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

func newTestLimiter(perSubject, perIP int, window time.Duration, now time.Time) *LoginRateLimiter {
	return &LoginRateLimiter{
		PerSubjectMax: perSubject,
		PerIPMax:      perIP,
		Window:        window,
		buckets:       map[string][]time.Time{},
		now:           func() time.Time { return now },
	}
}

func TestLoginRateLimiterAdmitsUnderCap(t *testing.T) {
	l := newTestLimiter(3, 100, 1*time.Minute, time.Now())
	for i := 0; i < 3; i++ {
		if !l.admit("alice", "1.1.1.1") {
			t.Fatalf("attempt %d denied unexpectedly", i+1)
		}
	}
}

func TestLoginRateLimiterRejectsAtPerSubjectCap(t *testing.T) {
	l := newTestLimiter(2, 100, 1*time.Minute, time.Now())
	_ = l.admit("alice", "1.1.1.1")
	_ = l.admit("alice", "1.1.1.1")
	if l.admit("alice", "1.1.1.1") {
		t.Error("third attempt for same subject should be rejected")
	}
}

func TestLoginRateLimiterRejectsAtPerIPCap(t *testing.T) {
	// Per-IP cap = 5, per-subject cap = 100 — varying subjects shouldn't
	// dodge the per-IP layer.
	l := newTestLimiter(100, 5, 1*time.Minute, time.Now())
	for i := 0; i < 5; i++ {
		s := "fabricated-" + string(rune('a'+i))
		if !l.admit(s, "1.1.1.1") {
			t.Fatalf("attempt %d denied under per-IP cap", i+1)
		}
	}
	if l.admit("another-subject", "1.1.1.1") {
		t.Error("6th attempt should hit per-IP cap regardless of subject")
	}
}

func TestLoginRateLimiterIPCapDoesNotConsumeSubjectQuota(t *testing.T) {
	// A request rejected at the per-IP layer must NOT count toward the
	// per-subject bucket — otherwise an IP-flood would lock specific users
	// out by exhausting their per-subject window.
	l := newTestLimiter(2, 1, 1*time.Minute, time.Now())
	if !l.admit("alice", "1.1.1.1") {
		t.Fatal("alice's first attempt unexpectedly denied")
	}
	// Per-IP is now full. Bob trying from the same IP gets rejected — but
	// his per-subject bucket should remain at 0.
	if l.admit("bob", "1.1.1.1") {
		t.Fatal("per-IP cap should reject bob")
	}
	// From a different IP, bob still has full quota.
	if !l.admit("bob", "2.2.2.2") {
		t.Error("bob's first attempt from a different IP should succeed")
	}
}

func TestLoginRateLimiterReleasesOnWindowRoll(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := t0
	l := &LoginRateLimiter{
		PerSubjectMax: 1,
		PerIPMax:      100,
		Window:        30 * time.Second,
		buckets:       map[string][]time.Time{},
		now:           func() time.Time { return cur },
	}
	if !l.admit("alice", "1.1.1.1") {
		t.Fatal("first attempt rejected")
	}
	if l.admit("alice", "1.1.1.1") {
		t.Fatal("second attempt should still be rejected")
	}
	cur = t0.Add(31 * time.Second) // window has rolled
	if !l.admit("alice", "1.1.1.1") {
		t.Error("attempt after window-roll should be admitted")
	}
}

func TestLoginRateLimiterScopesPerTuple(t *testing.T) {
	l := newTestLimiter(1, 100, 1*time.Minute, time.Now())
	if !l.admit("alice", "1.1.1.1") {
		t.Fatal("alice@1 first attempt denied")
	}
	if l.admit("alice", "1.1.1.1") {
		t.Error("alice@1 should be capped")
	}
	if !l.admit("bob", "1.1.1.1") {
		t.Error("bob should not be impacted by alice's bucket")
	}
}

// The per-IP key is the address the listener resolved, never a forwarding
// header: its leftmost entry is the caller's choice, so a client could pick a
// fresh bucket for every attempt.
func TestLoginRateLimiterKeysOnTheResolvedAddress(t *testing.T) {
	l := NewLoginRateLimiter(0, 0)
	req := &iamv1.LoginRequest{Subject: "alice"}

	ctx := clientip.WithAddr(context.Background(), netip.MustParseAddr(resolvedAddr))
	if subject, ip := l.coords(ctx, req); subject != "alice" || ip != resolvedAddr {
		t.Errorf("coords = (%q, %q), want alice and the resolved address", subject, ip)
	}
	if _, ip := l.coords(context.Background(), req); ip != "" {
		t.Errorf("ip = %q with no resolved address, want empty — the header is not consulted", ip)
	}
}

// The same, through the interceptor with the header actually sent: an
// attempt naming a fresh forwarded address, from a resolved address whose
// bucket is full, is refused.
func TestLoginRateLimiterIgnoresTheForwardingHeader(t *testing.T) {
	const perIPMax = 1
	l := NewLoginRateLimiter(0, perIPMax)
	// Login is left unimplemented: an attempt the limiter admits is answered
	// Unimplemented by the handler, one it refuses ResourceExhausted.
	client := paladiniamv1connect.NewAuthServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterAuthServiceHandler(s, paladiniamv1connect.UnimplementedAuthServiceHandler{})
	}, l.Interceptor()))
	ctx := clientip.WithAddr(context.Background(), netip.MustParseAddr(resolvedAddr))

	for i, attempt := range []struct {
		subject, forwardedFor string
		want                  connect.Code
	}{
		{"alice", "192.0.2.66", connect.CodeUnimplemented},
		{"bob", "192.0.2.67", connect.CodeResourceExhausted},
	} {
		_, err := client.Login(unarytest.WithHeader(ctx, forwardedForHeader, attempt.forwardedFor),
			&iamv1.LoginRequest{Subject: attempt.subject})
		if got := connect.CodeOf(err); got != attempt.want {
			t.Errorf("attempt %d: code = %v, want %v — the per-IP bucket must be the resolved address's", i+1, got, attempt.want)
		}
	}
}

// resolvedAddr is the client address the listener resolved.
const resolvedAddr = "198.51.100.7"

// forwardedForHeader is the forwarding header a caller writes as it likes.
const forwardedForHeader = "X-Forwarded-For"

func TestLoginRateLimiterProcedureSelection(t *testing.T) {
	l := NewLoginRateLimiter(0, 0)
	for _, p := range []string{
		"/paladin.iam.v1.AuthService/Login",
		"/paladin.iam.v1.AuthService/RefreshToken",
	} {
		if _, ok := l.procedures[p]; !ok {
			t.Errorf("default procedures should include %s", p)
		}
	}
	if _, ok := l.procedures["/paladin.iam.v1.AuthService/ExchangeAudience"]; ok {
		t.Error("ExchangeAudience should not be throttled by default")
	}
}

func TestLoginRateLimiterMaxKeysBackstop(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := &LoginRateLimiter{
		PerSubjectMax: 1000,
		PerIPMax:      1000,
		Window:        time.Minute,
		maxKeys:       4, // tiny cap: fills after 2 distinct IPs (ip + subject key each)
		buckets:       map[string][]time.Time{},
		now:           func() time.Time { return t0 },
	}
	// Two distinct IPs → 4 keys, hitting the cap.
	_ = l.admit("a", "1.1.1.1")
	_ = l.admit("b", "2.2.2.2")
	// A brand-new IP must be rejected by the backstop, not grow the map.
	if l.admit("c", "3.3.3.3") {
		t.Error("new key past maxKeys should be rejected")
	}
	// An existing IP+subject pair still admits (no new bucket created).
	if !l.admit("a", "1.1.1.1") {
		t.Error("existing key should still admit under its own cap")
	}
	// A known IP with a NEW subject must be rejected at the cap — admitting
	// would mint a new subjectKey bucket and grow the map past maxKeys
	// (the credential-stuffing memory-exhaustion vector).
	if l.admit("z", "1.1.1.1") {
		t.Error("new subject on an existing IP must be rejected at the cap")
	}
}

func TestLoginRateLimiterSweepEvictsIdle(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := t0
	l := &LoginRateLimiter{
		PerSubjectMax: 100,
		PerIPMax:      100,
		Window:        30 * time.Second,
		maxKeys:       100_000,
		buckets:       map[string][]time.Time{},
		now:           func() time.Time { return cur },
	}
	_ = l.admit("alice", "1.1.1.1") // 2 keys
	if len(l.buckets) == 0 {
		t.Fatal("expected buckets after admit")
	}
	// Roll past the window + sweep interval, then a fresh admit triggers
	// the sweep which drops alice's now-stale buckets.
	cur = t0.Add(61 * time.Second)
	_ = l.admit("bob", "2.2.2.2")
	if _, ok := l.buckets["i:1.1.1.1"]; ok {
		t.Error("stale alice IP bucket should have been swept")
	}
}

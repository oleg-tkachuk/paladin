package middleware

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// newTestLimiter counts in one process's window, as a replica falling back
// to its own count does; the shared stores are pinned in ratecount.
func newTestLimiter(perSubject, perIP int) *LoginRateLimiter {
	return NewLoginRateLimiter(ratecount.NewKeyLimiter(ratecount.NewLocal()), zap.NewNop(), perSubject, perIP)
}

// admitted reports whether an attempt by subject from ip is let through. An
// admitted one is told no wait.
func admitted(l *LoginRateLimiter, subject, ip string) bool {
	ok, retry := l.admit(context.Background(), procedureLogin, subject, ip)
	if ok && retry != 0 {
		panic("an admitted attempt was told to wait")
	}
	return ok
}

// A cap of zero or below keeps the built-in default, never none.
func TestLoginRateLimiterDefaultsItsCaps(t *testing.T) {
	for _, configured := range []int{0, -1} {
		l := newTestLimiter(configured, configured)
		if l.perSubjectMax != defaultLoginPerSubjectMax || l.perIPMax != defaultLoginPerIPMax {
			t.Errorf("caps %d: got subject %d ip %d, want the defaults", configured, l.perSubjectMax, l.perIPMax)
		}
	}
	if l := newTestLimiter(1, 1); l.perSubjectMax != 1 || l.perIPMax != 1 {
		t.Errorf("explicit caps of 1 were not kept: %d %d", l.perSubjectMax, l.perIPMax)
	}
}

func TestLoginRateLimiterAdmitsUnderCap(t *testing.T) {
	l := newTestLimiter(3, 100)
	for i := 0; i < 3; i++ {
		if !admitted(l, "alice", "1.1.1.1") {
			t.Fatalf("attempt %d denied unexpectedly", i+1)
		}
	}
}

func TestLoginRateLimiterRejectsAtPerSubjectCap(t *testing.T) {
	l := newTestLimiter(2, 100)
	_ = admitted(l, "alice", "1.1.1.1")
	_ = admitted(l, "alice", "1.1.1.1")
	if admitted(l, "alice", "1.1.1.1") {
		t.Error("third attempt for same subject should be rejected")
	}
}

func TestLoginRateLimiterRejectsAtPerIPCap(t *testing.T) {
	// Per-IP cap = 5, per-subject cap = 100 — varying subjects shouldn't
	// dodge the per-IP layer.
	l := newTestLimiter(100, 5)
	for i := 0; i < 5; i++ {
		s := "fabricated-" + string(rune('a'+i))
		if !admitted(l, s, "1.1.1.1") {
			t.Fatalf("attempt %d denied under per-IP cap", i+1)
		}
	}
	if admitted(l, "another-subject", "1.1.1.1") {
		t.Error("6th attempt should hit per-IP cap regardless of subject")
	}
}

func TestLoginRateLimiterIPCapDoesNotConsumeSubjectQuota(t *testing.T) {
	// A request rejected at the per-IP layer must NOT count toward the
	// per-subject bucket — otherwise an IP-flood would lock specific users
	// out by exhausting their per-subject window.
	l := newTestLimiter(1, 1)
	if !admitted(l, "alice", "1.1.1.1") {
		t.Fatal("alice's first attempt unexpectedly denied")
	}
	if admitted(l, "bob", "1.1.1.1") {
		t.Fatal("per-IP cap should reject bob")
	}
	// bob's subject key is per address, so prove the refusal above did not
	// count against it: from the same address, once its IP cap allows, his
	// one attempt still fits. Here a second limiter shares the window and
	// raises only the IP cap.
	wider := &LoginRateLimiter{limiter: l.limiter, perSubjectMax: 1, perIPMax: 100, procedures: l.procedures, log: l.log}
	if !admitted(wider, "bob", "1.1.1.1") {
		t.Error("an attempt refused at the IP layer was counted against bob")
	}
}

func TestLoginRateLimiterScopesPerTuple(t *testing.T) {
	l := newTestLimiter(1, 100)
	if !admitted(l, "alice", "1.1.1.1") {
		t.Fatal("alice@1 first attempt denied")
	}
	if admitted(l, "alice", "1.1.1.1") {
		t.Error("alice@1 should be capped")
	}
	if !admitted(l, "bob", "1.1.1.1") {
		t.Error("bob should not be impacted by alice's bucket")
	}
	if !admitted(l, "alice", "2.2.2.2") {
		t.Error("alice from another address should have her own bucket")
	}
}

// Two replicas on one store share one ceiling: the point of counting there.
func TestLoginRateLimiterReplicasShareTheCeiling(t *testing.T) {
	shared := ratecount.NewKeyLimiter(ratecount.NewLocal())
	a := NewLoginRateLimiter(shared, zap.NewNop(), 2, 100)
	b := NewLoginRateLimiter(shared, zap.NewNop(), 2, 100)
	if !admitted(a, "alice", "1.1.1.1") || !admitted(b, "alice", "1.1.1.1") {
		t.Fatal("attempts under the ceiling refused")
	}
	if admitted(a, "alice", "1.1.1.1") || admitted(b, "alice", "1.1.1.1") {
		t.Error("a replica granted past the shared ceiling")
	}
}

// failingLimiter decides nothing.
type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, 0, errors.New("store down")
}

// A limit that cannot be decided refuses the attempt: these calls carry
// credentials.
func TestLoginRateLimiterRefusesWhatItCannotDecide(t *testing.T) {
	// No logger given: the refusal is still logged, to a no-op.
	l := NewLoginRateLimiter(failingLimiter{}, nil, 0, 0)
	ok, retry := l.admit(context.Background(), procedureLogin, "alice", "1.1.1.1")
	if ok || retry != 0 {
		t.Errorf("an undecided attempt: admitted=%v retry=%v, want refused with no wait", ok, retry)
	}
}

// The per-IP key is the address the listener resolved, never a forwarding
// header: its leftmost entry is the caller's choice, so a client could pick a
// fresh bucket for every attempt.
func TestLoginRateLimiterKeysOnTheResolvedAddress(t *testing.T) {
	l := newTestLimiter(0, 0)
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
	l := newTestLimiter(0, perIPMax)
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
	l := newTestLimiter(0, 0)
	for _, p := range []string{procedureLogin, procedureRefreshToken} {
		if _, ok := l.procedures[p]; !ok {
			t.Errorf("default procedures should include %s", p)
		}
	}
	if _, ok := l.procedures["/paladin.iam.v1.AuthService/ExchangeAudience"]; ok {
		t.Error("ExchangeAudience should not be throttled by default")
	}
}

// A refused attempt is told when to retry, so a client backs off rather than
// hammering.
func TestLoginRateLimiterSaysWhenToRetry(t *testing.T) {
	l := NewLoginRateLimiter(ratecount.NewKeyLimiter(ratecount.NewLocal()), zap.NewNop(), 0, 1, unarytest.ProbeProcedure)
	ctx := clientip.WithAddr(context.Background(), netip.MustParseAddr(resolvedAddr))
	probe := &unarytest.Probe{}
	if _, err := unarytest.CallProbe(ctx, probe, []connect.ServerInterceptor{l.Interceptor()}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	header, err := unarytest.CallProbe(ctx, probe, []connect.ServerInterceptor{l.Interceptor()})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("second call: %v, want ResourceExhausted", err)
	}
	if header.Get(paladin.HeaderRetryAfter) == "" {
		t.Errorf("refused with no %s", paladin.HeaderRetryAfter)
	}
}

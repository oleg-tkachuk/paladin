package auth

// enforceCaveats is what turns a capability's restrictions into refusals. Its
// leaf helper ipInAnyCIDR is tested; the function that decides whether to
// consult it is not. That is the shape of the risk:
// the check works, and nothing proves it is reached.
//
// Both caveats fail silently in the permissive direction. A SourceIPCIDR
// allow-list that stops being consulted leaves a capability minted for one
// network usable from anywhere; a MaxRequests cap that stops being bumped
// leaves a token minted for ten calls good for unlimited ones. In both cases
// the request succeeds and nothing is logged, because from the interceptor's
// point of view the caveat simply passed.

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/capability"
)

// erroringUsage returns a chosen error from BumpRequest so the fail-closed
// branch is reachable — fakeUsage only ever produces the limit sentinel.
type erroringUsage struct {
	*fakeUsage
	err error
}

func (e erroringUsage) BumpRequest(ctx context.Context, id uuid.UUID, max int64) (int64, error) {
	if e.err != nil {
		return 0, e.err
	}
	return e.fakeUsage.BumpRequest(ctx, id, max)
}

func capWithCaveats(c capability.Caveats) *capability.Capability {
	return &capability.Capability{ID: uuid.New(), Caveats: c}
}

// from is a request context carrying addr as the resolved client address,
// as clientip.Middleware leaves it.
func from(addr string) context.Context {
	return clientip.WithAddr(context.Background(), netip.MustParseAddr(addr))
}

func TestEnforceCaveats_SourceIPAllowList(t *testing.T) {
	i := &capabilityInterceptor{}

	t.Run("no allow-list means no IP check", func(t *testing.T) {
		// Nothing configured: the caveat must not run at all, so a request
		// with no IP header whatsoever still passes.
		if err := i.enforceCaveats(context.Background(), capWithCaveats(capability.Caveats{})); err != nil {
			t.Fatalf("unrestricted capability refused: %v", err)
		}
	})

	restricted := capWithCaveats(capability.Caveats{SourceIPCIDR: []string{"10.0.0.0/8", "192.168.1.0/24"}})

	t.Run("an address inside the list passes", func(t *testing.T) {
		for _, ip := range []string{"10.1.2.3", "192.168.1.7"} {
			if err := i.enforceCaveats(from(ip), restricted); err != nil {
				t.Errorf("%s is inside the allow-list but was refused: %v", ip, err)
			}
		}
	})

	t.Run("an address outside the list is refused", func(t *testing.T) {
		for _, ip := range []string{"11.0.0.1", "192.168.2.7", "203.0.113.9"} {
			err := i.enforceCaveats(from(ip), restricted)
			if err == nil {
				t.Errorf("%s is outside the allow-list and was allowed", ip)
				continue
			}
			if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
				t.Errorf("%s: code = %v, want PermissionDenied", ip, got)
			}
		}
	})

	t.Run("an unknown client address is refused, not waved through", func(t *testing.T) {
		// The comment is explicit: SourceIPCIDR set but the IP unknown must
		// reject. Failing open here would make the caveat depend on whether a
		// proxy happened to set a header.
		err := i.enforceCaveats(context.Background(), restricted)
		if err == nil {
			t.Fatal("an IP-restricted capability was allowed with no client address")
		}
		if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", got)
		}
	})
}

func TestEnforceCaveats_MaxRequests(t *testing.T) {
	t.Run("no usage store makes the cap a documented no-op", func(t *testing.T) {
		// The operator opted out of usage accounting; the caveat cannot be
		// enforced atomically, and the code says so rather than pretending.
		i := &capabilityInterceptor{}
		c := capWithCaveats(capability.Caveats{MaxRequests: 1})
		for n := range 3 {
			if err := i.enforceCaveats(context.Background(), c); err != nil {
				t.Fatalf("call %d refused with no usage store: %v", n, err)
			}
		}
	})

	t.Run("a cap of zero is unlimited", func(t *testing.T) {
		usage := newFakeUsage()
		i := &capabilityInterceptor{usage: usage}
		c := capWithCaveats(capability.Caveats{MaxRequests: 0})
		for n := range 3 {
			if err := i.enforceCaveats(context.Background(), c); err != nil {
				t.Fatalf("call %d refused under an unlimited cap: %v", n, err)
			}
		}
		if got := usage.requests[c.ID]; got != 0 {
			t.Errorf("an unlimited capability was counted %d times; the bump must not run", got)
		}
	})

	t.Run("the cap is counted and then enforced", func(t *testing.T) {
		usage := newFakeUsage()
		i := &capabilityInterceptor{usage: usage}
		c := capWithCaveats(capability.Caveats{MaxRequests: 2})

		for n := range 2 {
			if err := i.enforceCaveats(context.Background(), c); err != nil {
				t.Fatalf("call %d within the cap was refused: %v", n+1, err)
			}
		}
		err := i.enforceCaveats(context.Background(), c)
		if err == nil {
			t.Fatal("the third call passed a cap of two")
		}
		if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
			t.Errorf("code = %v, want ResourceExhausted", got)
		}
	})

	t.Run("a store failure fails closed", func(t *testing.T) {
		// A capped capability whose counter cannot be incremented atomically
		// is safer refused than allowed unbounded — and the code distinguishes
		// this from the limit itself, because one is the caller's fault and
		// the other is ours.
		boom := errors.New("connection reset")
		i := &capabilityInterceptor{
			usage: erroringUsage{fakeUsage: newFakeUsage(), err: boom},
		}
		err := i.enforceCaveats(context.Background(), capWithCaveats(capability.Caveats{MaxRequests: 5}))
		if err == nil {
			t.Fatal("a capped capability was allowed when its counter could not be bumped")
		}
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Errorf("code = %v, want Unavailable — a store failure is not the caller's limit", got)
		}
	})
}

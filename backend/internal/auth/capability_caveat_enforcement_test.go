package auth

// enforceCaveats is what turns a capability's restrictions into refusals. The
// match itself (limes.Caveats.CheckSource) is tested in the module; these
// tests prove the interceptor reaches it. That is the shape of the risk: the
// check works, and nothing proves it is reached.
//
// Both caveats fail silently in the permissive direction. A SourceIPCIDR
// allow-list that stops being consulted leaves a capability minted for one
// network usable from anywhere; a request ceiling that stops being bumped
// leaves a token minted for ten calls good for unlimited ones. In both cases
// the request succeeds and nothing is logged, because from the interceptor's
// point of view the caveat simply passed.

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
)

// erroringUsage returns a chosen error from Bump so the fail-closed branch is
// reachable — the reference store only ever produces the limit sentinel.
type erroringUsage struct {
	*usageStore
	err error
}

func (e erroringUsage) Bump(ctx context.Context, req meter.BumpRequest) (meter.BumpReceipt, error) {
	if e.err != nil {
		return meter.BumpReceipt{}, e.err
	}
	return e.usageStore.Bump(ctx, req)
}

// capWithCaveats is a capability verified with caveats c; a ceiling c leaves
// at its zero value is unlimited.
func capWithCaveats(t *testing.T, c limes.Caveats) limes.VerifiedCapability {
	t.Helper()
	return captest.Verified(t, limes.Capability{Caveats: c})
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
		if err := i.enforceCaveats(context.Background(), capWithCaveats(t, limes.Caveats{})); err != nil {
			t.Fatalf("unrestricted capability refused: %v", err)
		}
	})

	restricted := capWithCaveats(t, limes.Caveats{SourceIPCIDR: []string{"10.0.0.0/8", "192.168.1.0/24"}})

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

func TestEnforceCaveats_RequestCeiling(t *testing.T) {
	const ceiling = 2
	t.Run("no usage store makes the cap a documented no-op", func(t *testing.T) {
		// The operator opted out of usage accounting; the caveat cannot be
		// enforced atomically, and the code says so rather than pretending.
		i := &capabilityInterceptor{}
		c := capWithCaveats(t, limes.Caveats{Requests: limes.RequestCeiling{Max: 1}})
		for n := range ceiling + 1 {
			if err := i.enforceCaveats(context.Background(), c); err != nil {
				t.Fatalf("call %d refused with no usage store: %v", n, err)
			}
		}
	})

	t.Run("an unlimited ceiling is not counted", func(t *testing.T) {
		m, usage := newMeteredMinter(t)
		i := &capabilityInterceptor{usage: usage}
		c, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Requests: limes.RequestCeiling{Unlimited: true}}})
		for n := range ceiling + 1 {
			if err := i.enforceCaveats(context.Background(), c); err != nil {
				t.Fatalf("call %d refused under an unlimited ceiling: %v", n, err)
			}
		}
		if _, err := usage.GetUsage(context.Background(), c.Capability().ID); !errors.Is(err, meter.ErrUsageNotFound) {
			t.Errorf("an unlimited capability was counted (err = %v); the bump must not run", err)
		}
	})

	t.Run("a ceiling of zero is never issued", func(t *testing.T) {
		// Zero allows no request, so a capability with it could do nothing:
		// limes refuses to issue one, and no verifier ever meets it.
		m, _ := newMeteredMinter(t)
		_, err := m.Issuer.Issue(context.Background(), limes.IssueRequest{
			Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: uuid.New(), Subject: captest.Subject},
			IssuedBy: limes.Principal{Type: limes.PrincipalTypeUser, TenantID: uuid.New(), Subject: captest.Subject},
			Audience: []string{captest.Audience},
			Caveats: limes.Caveats{
				Ops:    []limes.Op{limes.OpGet},
				Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: captest.Unit},
			},
			ClientID: captest.ClientID,
		})
		if !errors.Is(err, limes.ErrInvalidRequest) {
			t.Errorf("err = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("the cap is counted and then enforced", func(t *testing.T) {
		m, usage := newMeteredMinter(t)
		i := &capabilityInterceptor{usage: usage}
		c, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Requests: limes.RequestCeiling{Max: ceiling}}})

		for n := range ceiling {
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
		const ceiling = 5
		boom := errors.New("connection reset")
		m, usage := newMeteredMinter(t)
		i := &capabilityInterceptor{usage: erroringUsage{usageStore: usage, err: boom}}
		c, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Requests: limes.RequestCeiling{Max: ceiling}}})
		err := i.enforceCaveats(context.Background(), c)
		if err == nil {
			t.Fatal("a capped capability was allowed when its counter could not be bumped")
		}
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Errorf("code = %v, want Unavailable — a store failure is not the caller's limit", got)
		}
	})
}

package auth

// enforceCaveats is what turns a capability's restrictions into refusals. Its
// leaf helper ipInAnyCIDR is tested; the function that decides whether to
// consult it is not, and neither is clientIP. That is the shape of the risk:
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
	"net"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

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

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func TestEnforceCaveats_SourceIPAllowList(t *testing.T) {
	i := &capabilityInterceptor{realIPHeader: "X-Forwarded-For"}

	t.Run("no allow-list means no IP check", func(t *testing.T) {
		// Nothing configured: the caveat must not run at all, so a request
		// with no IP header whatsoever still passes.
		if err := i.enforceCaveats(context.Background(), capWithCaveats(capability.Caveats{}), hdr()); err != nil {
			t.Fatalf("unrestricted capability refused: %v", err)
		}
	})

	restricted := capWithCaveats(capability.Caveats{SourceIPCIDR: []string{"10.0.0.0/8", "192.168.1.0/24"}})

	t.Run("an address inside the list passes", func(t *testing.T) {
		for _, ip := range []string{"10.1.2.3", "192.168.1.7"} {
			if err := i.enforceCaveats(context.Background(), restricted, hdr("X-Forwarded-For", ip)); err != nil {
				t.Errorf("%s is inside the allow-list but was refused: %v", ip, err)
			}
		}
	})

	t.Run("an address outside the list is refused", func(t *testing.T) {
		for _, ip := range []string{"11.0.0.1", "192.168.2.7", "203.0.113.9"} {
			err := i.enforceCaveats(context.Background(), restricted, hdr("X-Forwarded-For", ip))
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
		for name, h := range map[string]http.Header{
			"no headers":          hdr(),
			"empty header":        hdr("X-Forwarded-For", ""),
			"unparseable":         hdr("X-Forwarded-For", "not-an-ip"),
			"unparseable real-ip": hdr("X-Real-Ip", "also-not-an-ip"),
		} {
			err := i.enforceCaveats(context.Background(), restricted, h)
			if err == nil {
				t.Errorf("%s: an IP-restricted capability was allowed with no usable client address", name)
				continue
			}
			if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
				t.Errorf("%s: code = %v, want PermissionDenied", name, got)
			}
		}
	})
}

func TestEnforceCaveats_MaxRequests(t *testing.T) {
	t.Run("no usage store makes the cap a documented no-op", func(t *testing.T) {
		// The operator opted out of usage accounting; the caveat cannot be
		// enforced atomically, and the code says so rather than pretending.
		i := &capabilityInterceptor{realIPHeader: "X-Forwarded-For"}
		c := capWithCaveats(capability.Caveats{MaxRequests: 1})
		for n := range 3 {
			if err := i.enforceCaveats(context.Background(), c, hdr()); err != nil {
				t.Fatalf("call %d refused with no usage store: %v", n, err)
			}
		}
	})

	t.Run("a cap of zero is unlimited", func(t *testing.T) {
		usage := newFakeUsage()
		i := &capabilityInterceptor{realIPHeader: "X-Forwarded-For", usage: usage}
		c := capWithCaveats(capability.Caveats{MaxRequests: 0})
		for n := range 3 {
			if err := i.enforceCaveats(context.Background(), c, hdr()); err != nil {
				t.Fatalf("call %d refused under an unlimited cap: %v", n, err)
			}
		}
		if got := usage.requests[c.ID]; got != 0 {
			t.Errorf("an unlimited capability was counted %d times; the bump must not run", got)
		}
	})

	t.Run("the cap is counted and then enforced", func(t *testing.T) {
		usage := newFakeUsage()
		i := &capabilityInterceptor{realIPHeader: "X-Forwarded-For", usage: usage}
		c := capWithCaveats(capability.Caveats{MaxRequests: 2})

		for n := range 2 {
			if err := i.enforceCaveats(context.Background(), c, hdr()); err != nil {
				t.Fatalf("call %d within the cap was refused: %v", n+1, err)
			}
		}
		err := i.enforceCaveats(context.Background(), c, hdr())
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
			realIPHeader: "X-Forwarded-For",
			usage:        erroringUsage{fakeUsage: newFakeUsage(), err: boom},
		}
		err := i.enforceCaveats(context.Background(), capWithCaveats(capability.Caveats{MaxRequests: 5}), hdr())
		if err == nil {
			t.Fatal("a capped capability was allowed when its counter could not be bumped")
		}
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Errorf("code = %v, want Unavailable — a store failure is not the caller's limit", got)
		}
	})
}

// clientIP decides which address the allow-list is checked against, so
// reading the wrong field of X-Forwarded-For checks a proxy's address instead
// of the caller's — and every request through that proxy would pass.
func TestClientIP(t *testing.T) {
	i := &capabilityInterceptor{realIPHeader: "X-Forwarded-For"}

	cases := map[string]struct {
		header http.Header
		want   string // "" means nil
	}{
		"single value":           {hdr("X-Forwarded-For", "10.0.0.1"), "10.0.0.1"},
		"leftmost of a chain":    {hdr("X-Forwarded-For", "10.0.0.1, 10.0.0.2, 10.0.0.3"), "10.0.0.1"},
		"chain without spaces":   {hdr("X-Forwarded-For", "10.0.0.1,10.0.0.2"), "10.0.0.1"},
		"surrounding whitespace": {hdr("X-Forwarded-For", "  10.0.0.1  "), "10.0.0.1"},
		"ipv6":                   {hdr("X-Forwarded-For", "2001:db8::1"), "2001:db8::1"},
		"real-ip fallback":       {hdr("X-Real-Ip", "10.0.0.9"), "10.0.0.9"},
		"forwarded wins":         {hdr("X-Forwarded-For", "10.0.0.1", "X-Real-Ip", "10.0.0.9"), "10.0.0.1"},
		"unparseable falls back": {hdr("X-Forwarded-For", "garbage", "X-Real-Ip", "10.0.0.9"), "10.0.0.9"},
		"leading comma":          {hdr("X-Forwarded-For", ",10.0.0.2"), ""},
		"both unparseable":       {hdr("X-Forwarded-For", "garbage", "X-Real-Ip", "junk"), ""},
		"nothing at all":         {hdr(), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := i.clientIP(tc.header)
			if tc.want == "" {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if got == nil || !got.Equal(net.ParseIP(tc.want)) {
				t.Errorf("got %v, want %s", got, tc.want)
			}
		})
	}
}

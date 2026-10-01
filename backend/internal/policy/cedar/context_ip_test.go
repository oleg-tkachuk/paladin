package cedar

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
)

// context.ip was declared in the schema and never filled, so a policy on it
// compared against "". It now carries the client address the listener
// resolved from its trusted proxies.
func TestContextIPComesFromTheResolvedClientAddress(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Tenant: `permit(principal, action, resource) when { context.ip == "198.51.100.7" };`,
	}}, 0)
	ask := func(ctx context.Context, rc RequestContext) Decision {
		t.Helper()
		d, err := e.IsAuthorized(ctx, &Principal{Subject: "member", TenantID: tid, TenantSlug: "acme"}, ActionGetObject,
			&Resource{TenantID: tid, TenantSlug: "acme", Collection: "docs"}, rc)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	from := func(addr string) context.Context {
		return clientip.WithAddr(context.Background(), netip.MustParseAddr(addr))
	}

	if got := ask(from("198.51.100.7"), RequestContext{}); got != DecisionAllow {
		t.Errorf("matching client address = %v, want Allow", got)
	}
	if got := ask(from("203.0.113.1"), RequestContext{}); got != DecisionDeny {
		t.Errorf("other client address = %v, want Deny", got)
	}
	if got := ask(context.Background(), RequestContext{}); got != DecisionDeny {
		t.Errorf("no resolved address = %v, want Deny", got)
	}
	if got := ask(from("203.0.113.1"), RequestContext{IP: "198.51.100.7"}); got != DecisionAllow {
		t.Errorf("an explicit RequestContext.IP = %v, want it to win", got)
	}
}

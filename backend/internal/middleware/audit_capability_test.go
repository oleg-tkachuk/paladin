package middleware

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
)

// TestCapabilityID_Absent confirms ctx without a capability returns
// uuid.Nil — the audit row will have NULL capability_id, the partial
// index stays small, JWT-only flows are unaffected.
func TestCapabilityID_Absent(t *testing.T) {
	t.Parallel()
	got := capabilityID(context.Background())
	if got != uuid.Nil() {
		t.Errorf("expected uuid.Nil, got %s", got)
	}
}

// TestCapabilityID_Present confirms ctx WITH a capability returns its
// ID — the audit row carries it, per-cap rollups work.
func TestCapabilityID_Present(t *testing.T) {
	t.Parallel()
	verified := captest.Verified(t, limes.Capability{
		Subject: limes.Principal{Type: limes.PrincipalTypeAgent},
		Caveats: limes.Caveats{Ops: []limes.Op{limes.OpGet}},
	})
	want := verified.Capability().ID
	ctx := auth.WithCapability(context.Background(), verified)
	got := capabilityID(ctx)
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// relayedBy names the client an exchanged token says relayed the call
// (ADR-0036), and nothing for a direct call or no principal at all.
func TestRelayedBy(t *testing.T) {
	t.Parallel()
	const mcpServer = "paladin-mcp-server"
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"no principal", context.Background(), ""},
		{"a direct call", auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u"}), ""},
		{"relayed by the MCP server", auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u", Actor: mcpServer}), mcpServer},
	} {
		if got := relayedBy(tc.ctx); got != tc.want {
			t.Errorf("%s: relayedBy = %q, want %q", tc.name, got, tc.want)
		}
	}
}

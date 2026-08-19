package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// TestCapabilityID_Absent confirms ctx without a capability returns
// uuid.Nil — the audit row will have NULL capability_id, the partial
// index stays small, JWT-only flows are unaffected.
func TestCapabilityID_Absent(t *testing.T) {
	t.Parallel()
	got := capabilityID(context.Background())
	if got != uuid.Nil {
		t.Errorf("expected uuid.Nil, got %s", got)
	}
}

// TestCapabilityID_Present confirms ctx WITH a capability returns its
// ID — the audit row carries it, per-cap rollups work.
func TestCapabilityID_Present(t *testing.T) {
	t.Parallel()
	want := uuid.New()
	cap := &capability.Capability{
		ID: want,
		Subject: capability.Principal{
			Type:     capability.PrincipalAgent,
			TenantID: uuid.New(),
		},
		Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}},
	}
	ctx := auth.WithCapability(context.Background(), cap)
	got := capabilityID(ctx)
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

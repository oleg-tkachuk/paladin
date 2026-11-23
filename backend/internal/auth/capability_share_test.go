package auth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/capop"
)

// shareTestPrefix restricts a capability to a resource, which a delegation
// does not name.
const shareTestPrefix = "object://t/c/"

// Share is admitted, and stamped for the policy engine, exactly when the
// caveats allow it. The rest of the caveats are Issuer.Delegate's to settle,
// so a resource-restricted capability or one requiring an idempotency key is
// admitted too.
func TestAssertCapabilityShare(t *testing.T) {
	cases := []struct {
		name    string
		caveats limes.Caveats
		refused bool
	}{
		{"share allowed", limes.Caveats{Ops: []limes.Op{limes.OpGet, limes.OpShare}}, false},
		{"resource-restricted", limes.Caveats{
			Ops: []limes.Op{limes.OpShare}, ResourcePrefixes: []string{shareTestPrefix},
		}, false},
		{"idempotency key required", limes.Caveats{
			Ops: []limes.Op{limes.OpShare}, IdempotencyKeyRequired: true,
		}, false},
		{"share not allowed", limes.Caveats{Ops: []limes.Op{limes.OpGet}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := admittedCtx(t, limes.Capability{Caveats: c.caveats})
			err := AssertCapabilityShare(ctx)
			if !c.refused {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got := capop.Admitted(ctx); got != string(limes.OpShare) {
					t.Fatalf("admitted = %q, want %q", got, limes.OpShare)
				}
				return
			}
			if got := connect.CodeOf(err); got != connect.CodePermissionDenied || !errors.Is(err, limes.ErrOpNotAllowed) {
				t.Fatalf("err = %v (%v), want PermissionDenied wrapping limes.ErrOpNotAllowed", err, got)
			}
			if got := capop.Admitted(ctx); got != "" {
				t.Fatalf("admitted after a refusal = %q, want empty", got)
			}
		})
	}
}

// Without a presented capability there is nothing to share from.
func TestAssertCapabilityShare_WithoutACapability(t *testing.T) {
	ctx := withLastOpHolder(context.Background())
	if got := connect.CodeOf(AssertCapabilityShare(ctx)); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	if got := capop.Admitted(ctx); got != "" {
		t.Fatalf("admitted = %q, want empty", got)
	}
}

// A capability acts as itself: its tenant, its id, its kind, and no roles.
func TestCapabilityPrincipal(t *testing.T) {
	c := &limes.Capability{ID: uuid.New(), Subject: limes.Principal{TenantID: uuid.New()}}
	p := CapabilityPrincipal(c, AudienceData)
	if p.TenantID != c.Subject.TenantID || p.Subject != SubjectPrefixCapability+c.ID.String() ||
		p.Kind != PrincipalKindCapability || p.Audience != AudienceData || len(p.Roles) != 0 {
		t.Fatalf("principal = %+v", p)
	}
}

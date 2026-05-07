package auth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/capability"
)

// TestAssertCapabilityOp_NoCapability covers the load-bearing
// "no capability presented = no-op" path. Handlers that wire this in
// must not fail JWT-auth flows that don't carry a capability token.
func TestAssertCapabilityOp_NoCapability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if err := AssertCapabilityOp(ctx, capability.OpGet, "object://acme/foo"); err != nil {
		t.Fatalf("expected nil for no-capability ctx, got %v", err)
	}
}

// TestAssertCapabilityOp_AllowedOp confirms the happy path: capability
// authorises the requested op, AssertCapabilityOp returns nil, handler
// proceeds.
func TestAssertCapabilityOp_AllowedOp(t *testing.T) {
	t.Parallel()
	cap := &capability.Capability{
		ID:       uuid.New(),
		Subject:  capability.Principal{TenantID: uuid.New(), Type: capability.PrincipalAgent},
		Audience: []string{capability.AudiencePlaneData},
		Caveats: capability.Caveats{
			Ops:              []capability.Op{capability.OpGet, capability.OpList},
			ResourcePrefixes: []string{"object://acme/"},
		},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, capability.OpGet, "object://acme/foo"); err != nil {
		t.Fatalf("expected nil for allowed op, got %v", err)
	}
}

// TestAssertCapabilityOp_DeniedOp verifies the op-rejection path —
// requesting an op not in Caveats.Ops fails with PermissionDenied.
func TestAssertCapabilityOp_DeniedOp(t *testing.T) {
	t.Parallel()
	cap := &capability.Capability{
		Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}},
	}
	ctx := WithCapability(context.Background(), cap)
	err := AssertCapabilityOp(ctx, capability.OpDelete, "")
	if err == nil {
		t.Fatal("expected error for denied op, got nil")
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied, got %v", ce.Code())
	}
}

// TestAssertCapabilityOp_DeniedResource covers the resource-prefix
// rejection: op is allowed but the URI escapes the configured prefix.
func TestAssertCapabilityOp_DeniedResource(t *testing.T) {
	t.Parallel()
	cap := &capability.Capability{
		Caveats: capability.Caveats{
			Ops:              []capability.Op{capability.OpGet},
			ResourcePrefixes: []string{"object://acme/"},
		},
	}
	ctx := WithCapability(context.Background(), cap)
	err := AssertCapabilityOp(ctx, capability.OpGet, "object://other-tenant/foo")
	if err == nil {
		t.Fatal("expected error for resource escape, got nil")
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodePermissionDenied {
		t.Errorf("expected CodePermissionDenied, got %v", ce.Code())
	}
}

// TestAssertCapabilityOp_EmptyResourceURI confirms the resource check
// is skipped when the caller passes empty URI — used by ops that don't
// target a specific resource (e.g. capability self-introspect).
func TestAssertCapabilityOp_EmptyResourceURI(t *testing.T) {
	t.Parallel()
	cap := &capability.Capability{
		Caveats: capability.Caveats{
			Ops:              []capability.Op{capability.OpManage},
			ResourcePrefixes: []string{"object://acme/"}, // would NOT match anything but irrelevant
		},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, capability.OpManage, ""); err != nil {
		t.Fatalf("expected nil for empty URI, got %v", err)
	}
}

// TestAssertCapabilityOp_UnrestrictedPrefix confirms an empty prefix
// set means "no restriction within tenant" — verifier already enforced
// tenant scope, so any URI passes the caveat check.
func TestAssertCapabilityOp_UnrestrictedPrefix(t *testing.T) {
	t.Parallel()
	cap := &capability.Capability{
		Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, capability.OpGet, "object://anything/anywhere"); err != nil {
		t.Fatalf("expected nil for unrestricted, got %v", err)
	}
}

// TestAssertCapabilityOp_ExactURIMatch confirms ResourceURIs (exact
// match) lets a hand-off flow pin specific artifacts.
func TestAssertCapabilityOp_ExactURIMatch(t *testing.T) {
	t.Parallel()
	cap := &capability.Capability{
		Caveats: capability.Caveats{
			Ops:          []capability.Op{capability.OpGet},
			ResourceURIs: []string{"object://acme/dataset-42"},
		},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, capability.OpGet, "object://acme/dataset-42"); err != nil {
		t.Fatalf("expected nil for exact URI match, got %v", err)
	}
	if err := AssertCapabilityOp(ctx, capability.OpGet, "object://acme/dataset-43"); err == nil {
		t.Fatal("expected error for non-matching URI")
	}
}

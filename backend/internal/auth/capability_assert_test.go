package auth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// TestAssertCapabilityOp_NoCapability covers the load-bearing
// "no capability presented = no-op" path. Handlers that wire this in
// must not fail JWT-auth flows that don't carry a capability token.
func TestAssertCapabilityOp_NoCapability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if err := AssertCapabilityOp(ctx, limes.OpGet, "object://acme/foo"); err != nil {
		t.Fatalf("expected nil for no-capability ctx, got %v", err)
	}
}

// TestAssertCapabilityOp_AllowedOp confirms the happy path: capability
// authorises the requested op, AssertCapabilityOp returns nil, handler
// proceeds.
func TestAssertCapabilityOp_AllowedOp(t *testing.T) {
	t.Parallel()
	cap := &limes.Capability{
		ID:       uuid.New(),
		Subject:  limes.Principal{TenantID: uuid.New(), Type: limes.PrincipalAgent},
		Audience: []string{limes.AudiencePlaneData},
		Caveats: limes.Caveats{
			Ops:              []limes.Op{limes.OpGet, limes.OpList},
			ResourcePrefixes: []string{"object://acme/"},
		},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, limes.OpGet, "object://acme/foo"); err != nil {
		t.Fatalf("expected nil for allowed op, got %v", err)
	}
}

// TestAssertCapabilityOp_DeniedOp verifies the op-rejection path —
// requesting an op not in Caveats.Ops fails with PermissionDenied.
func TestAssertCapabilityOp_DeniedOp(t *testing.T) {
	t.Parallel()
	cap := &limes.Capability{
		Caveats: limes.Caveats{Ops: []limes.Op{limes.OpGet}},
	}
	ctx := WithCapability(context.Background(), cap)
	err := AssertCapabilityOp(ctx, limes.OpDelete, "")
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
	cap := &limes.Capability{
		Caveats: limes.Caveats{
			Ops:              []limes.Op{limes.OpGet},
			ResourcePrefixes: []string{"object://acme/"},
		},
	}
	ctx := WithCapability(context.Background(), cap)
	err := AssertCapabilityOp(ctx, limes.OpGet, "object://other-tenant/foo")
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

// TestAssertCapabilityOp_EmptyResourceURI pins that an operation which
// cannot name its resource fails closed on a resource-restricted capability.
// The check used to be skipped for an empty URI, so a capability confined to
// one prefix could run every batch delete, tag edit and listing in its tenant:
// the handlers for those pass "" and the caveat never saw them.
func TestAssertCapabilityOp_EmptyResourceURI(t *testing.T) {
	t.Parallel()
	restricted := &limes.Capability{
		Caveats: limes.Caveats{
			Ops:              []limes.Op{limes.OpManage},
			ResourcePrefixes: []string{"object://acme/"},
		},
	}
	err := AssertCapabilityOp(WithCapability(context.Background(), restricted), limes.OpManage, "")
	if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, limes.ErrResourceNotAllowed) {
		t.Fatalf("restricted capability, unscoped op: err = %v, want PermissionDenied wrapping ErrResourceNotAllowed", err)
	}

	// An unrestricted capability may still run unscoped operations.
	unrestricted := &limes.Capability{
		Caveats: limes.Caveats{Ops: []limes.Op{limes.OpManage}},
	}
	if err := AssertCapabilityOp(WithCapability(context.Background(), unrestricted), limes.OpManage, ""); err != nil {
		t.Fatalf("unrestricted capability, unscoped op: %v", err)
	}
}

// TestAssertCapabilityOp_UnrestrictedPrefix confirms an empty prefix
// set means "no restriction within tenant" — verifier already enforced
// tenant scope, so any URI passes the caveat check.
func TestAssertCapabilityOp_UnrestrictedPrefix(t *testing.T) {
	t.Parallel()
	cap := &limes.Capability{
		Caveats: limes.Caveats{Ops: []limes.Op{limes.OpGet}},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, limes.OpGet, "object://anything/anywhere"); err != nil {
		t.Fatalf("expected nil for unrestricted, got %v", err)
	}
}

// TestAssertCapabilityOp_ExactURIMatch confirms ResourceURIs (exact
// match) lets a hand-off flow pin specific artifacts.
func TestAssertCapabilityOp_ExactURIMatch(t *testing.T) {
	t.Parallel()
	cap := &limes.Capability{
		Caveats: limes.Caveats{
			Ops:          []limes.Op{limes.OpGet},
			ResourceURIs: []string{"object://acme/dataset-42"},
		},
	}
	ctx := WithCapability(context.Background(), cap)
	if err := AssertCapabilityOp(ctx, limes.OpGet, "object://acme/dataset-42"); err != nil {
		t.Fatalf("expected nil for exact URI match, got %v", err)
	}
	if err := AssertCapabilityOp(ctx, limes.OpGet, "object://acme/dataset-43"); err == nil {
		t.Fatal("expected error for non-matching URI")
	}
}

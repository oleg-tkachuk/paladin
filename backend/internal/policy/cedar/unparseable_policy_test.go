package cedar

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// A tenant-authored policy layer that will not parse must freeze the scope it
// governs — and nothing else.
//
// It used to take everything down with it. The layers were concatenated before
// compiling, so one syntax error voided the tenant's inherited policy AND the
// platform's built-in for that scope, including the unconditional
// platform.admin permit. Every read, delete and authorization answered
// `internal: cedar: compile policy: parser error`, and repairing the policy
// required passing a check that needed the same policy to compile — a deadlock
// whose only exit was SQL.
//
// The layers compile separately now. A broken one is replaced by a freeze
// expressed IN Cedar, so no code path skips authorization, and the freeze
// carves out the management action for that layer. Who may take it is left to
// the layers that still compile: the tenant layer answers for a broken
// collection, the built-in answers for a broken tenant.

type layeredStore struct{ layers Layers }

func (s layeredStore) Fetch(context.Context, uuid.UUID, string) (Layers, []byte, string, error) {
	return s.layers, []byte("hash"), "acme", nil
}

func (layeredStore) Watch(ctx context.Context) (<-chan ChangeEvent, error) {
	ch := make(chan ChangeEvent)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func platformAdmin(tenantID uuid.UUID) *Principal {
	return &Principal{
		Subject: "ops@example.test", TenantID: tenantID, TenantSlug: "acme",
		Roles: []string{"platform.admin"},
	}
}

func decideOn(t *testing.T, e *Engine, p *Principal, action string, tenantID uuid.UUID, collection string) Decision {
	t.Helper()
	d, err := e.IsAuthorized(context.Background(), p, action,
		&Resource{TenantID: tenantID, TenantSlug: "acme", Collection: collection},
		RequestContext{})
	if err != nil {
		t.Fatalf("%s: unexpected error %v — a broken stored layer must not fail the decision", action, err)
	}
	return d
}

func TestBrokenCollectionLayerFreezesTheScopeButLeavesRepair(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Tenant:     `permit(principal, action, resource);`,
		Collection: `permit(principal);`, // not Cedar: three clauses required
	}}, 0)

	// Frozen: the blanket forbid beats every permit, including the built-in's
	// unconditional platform.admin allow.
	if got := decideOn(t, e, platformAdmin(tid), ActionGetObject, tid, "docs"); got != DecisionDeny {
		t.Errorf("GetObject on a frozen scope = %v, want Deny", got)
	}
	// Except the way out. Repair is a write to the broken layer, so the freeze
	// carves out that layer's management action and lets the surviving layers
	// decide who holds it.
	if got := decideOn(t, e, platformAdmin(tid), ActionManageCollection, tid, "docs"); got != DecisionAllow {
		t.Errorf("ManageCollection on a frozen scope = %v, want Allow — without it the entity can never be repaired or removed", got)
	}
}

func TestABrokenCollectionLayerDoesNotFreezeItsSiblings(t *testing.T) {
	tid := uuid.New()
	// The same tenant, a collection whose own layer is empty. Nothing here is
	// broken, so nothing here is frozen: the blast radius of a syntax error is
	// the layer that carries it.
	e := NewEngine(layeredStore{Layers{
		Tenant:     `permit(principal, action, resource);`,
		Collection: "",
	}}, 0)

	if got := decideOn(t, e, platformAdmin(tid), ActionGetObject, tid, "other"); got != DecisionAllow {
		t.Errorf("GetObject on an unaffected collection = %v, want Allow", got)
	}
}

func TestBrokenTenantLayerFreezesTheTenantButLeavesRepair(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Tenant:     `permit(principal);`, // not Cedar
		Collection: "",
	}}, 0)

	if got := decideOn(t, e, platformAdmin(tid), ActionGetObject, tid, "docs"); got != DecisionDeny {
		t.Errorf("GetObject under a frozen tenant = %v, want Deny", got)
	}
	// The built-in is the surviving layer here, and it grants platform.admin
	// unconditionally — which is exactly the recovery hierarchy: a broken
	// tenant layer is repaired by the authority that writes it normally.
	if got := decideOn(t, e, platformAdmin(tid), ActionManageTenant, tid, ""); got != DecisionAllow {
		t.Errorf("ManageTenant under a frozen tenant = %v, want Allow", got)
	}
}

func TestValidLayersAreUnaffected(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(layeredStore{Layers{
		Tenant:     `permit(principal, action, resource);`,
		Collection: `permit(principal, action, resource);`,
	}}, 0)

	if got := decideOn(t, e, platformAdmin(tid), ActionGetObject, tid, "docs"); got != DecisionAllow {
		t.Errorf("GetObject with both layers valid = %v, want Allow", got)
	}
}

// The degradation is reported, not silent: an operator has to be able to learn
// that a scope is frozen because its policy stopped parsing.
func TestDegradedLayersAreNamed(t *testing.T) {
	_, degraded := (&Engine{}).degradeUnparseableLayers(Layers{
		Tenant:     `permit(principal);`,
		Collection: `permit(principal);`,
	}, uuid.New(), "docs")

	if len(degraded) != 2 {
		t.Fatalf("degraded = %v, want both layers named", degraded)
	}
}

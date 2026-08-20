package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// authzCollection evaluates `policy` for a ManageCollection request on the given
// Collection resource, with the canonical-EUID flag on or off.
func authzCollection(t *testing.T, policy string, canonical bool, r *Resource) Decision {
	t.Helper()
	var opts []EngineOption
	if canonical {
		opts = append(opts, WithCanonicalCollectionEUID(true))
	}
	e := NewEngine(fakeStore{text: policy}, time.Minute, opts...)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "u@acme", TenantID: r.TenantID, Roles: []string{"tenant.user"}},
		ActionManageCollection,
		r,
		RequestContext{},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	return dec
}

// TestCanonicalEUID_AttributePolicyUnaffected is the core safety claim: an
// attribute/parent-based policy (the only kind Paladin ships) yields the SAME
// decision whether the Collection EUID is legacy or canonical — because the
// entity's attributes/parents don't depend on the UID string. This is why the
// switch is behaviourally invisible to real policies.
func TestCanonicalEUID_AttributePolicyUnaffected(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, Collection: "invoices", BackendID: "primary", BucketId: "b1"}
	const policy = `permit(principal, action, resource) when { resource.collection == "invoices" };`

	if got := authzCollection(t, policy, false, r); got != DecisionAllow {
		t.Errorf("legacy-EUID decision = %v, want Allow", got)
	}
	if got := authzCollection(t, policy, true, r); got != DecisionAllow {
		t.Errorf("canonical-EUID decision = %v, want Allow (attribute policy must be unaffected)", got)
	}

	// And a non-matching attribute is denied in both modes.
	const denyPolicy = `permit(principal, action, resource) when { resource.collection == "other" };`
	if got := authzCollection(t, denyPolicy, false, r); got != DecisionDeny {
		t.Errorf("legacy non-match = %v, want Deny", got)
	}
	if got := authzCollection(t, denyPolicy, true, r); got != DecisionDeny {
		t.Errorf("canonical non-match = %v, want Deny", got)
	}
}

// TestCanonicalEUID_LiteralShapeFlips proves the flag actually changes the
// entity UID: a policy that pins the legacy `resource == Collection::"{tid}/{ok}"`
// literal matches ONLY with the flag off, and a policy pinning the canonical
// A-shape literal matches ONLY with the flag on. (Paladin ships no such literal
// policies — cedar-authoring.md steers against them — this test just pins the
// shape switch.)
func TestCanonicalEUID_LiteralShapeFlips(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, Collection: "invoices", BackendID: "primary", BucketId: "b1"}

	legacyEUID := tid.String() + "/invoices"
	canonicalEUID := "storageBackends/primary/buckets/b1/tenants/" + tid.String() + "/collections/invoices"

	legacyPolicy := `permit(principal, action, resource == Collection::"` + legacyEUID + `");`
	canonicalPolicy := `permit(principal, action, resource == Collection::"` + canonicalEUID + `");`

	// Legacy-literal policy: matches with flag OFF, not with flag ON.
	if got := authzCollection(t, legacyPolicy, false, r); got != DecisionAllow {
		t.Errorf("legacy literal, flag off = %v, want Allow", got)
	}
	if got := authzCollection(t, legacyPolicy, true, r); got != DecisionDeny {
		t.Errorf("legacy literal, flag on = %v, want Deny (EUID is now canonical)", got)
	}

	// Canonical-literal policy: matches with flag ON, not with flag OFF.
	if got := authzCollection(t, canonicalPolicy, true, r); got != DecisionAllow {
		t.Errorf("canonical literal, flag on = %v, want Allow", got)
	}
	if got := authzCollection(t, canonicalPolicy, false, r); got != DecisionDeny {
		t.Errorf("canonical literal, flag off = %v, want Deny (EUID is still legacy)", got)
	}
}

// TestCanonicalEUID_FallsBackWithoutBinding: with the flag ON but no
// (backend, bucket) in scope, the EUID stays legacy — canonicalization only
// applies where the binding is available.
func TestCanonicalEUID_FallsBackWithoutBinding(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, Collection: "invoices"} // no backend/bucket
	legacyEUID := tid.String() + "/invoices"
	legacyPolicy := `permit(principal, action, resource == Collection::"` + legacyEUID + `");`

	if got := authzCollection(t, legacyPolicy, true, r); got != DecisionAllow {
		t.Errorf("flag on but no binding = %v, want Allow (falls back to legacy EUID)", got)
	}
}

package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// authzObjectKey evaluates `policy` for a ManageObjectKey request on the given
// ObjectKey resource, with the canonical-EUID flag on or off.
func authzObjectKey(t *testing.T, policy string, canonical bool, r *Resource) Decision {
	t.Helper()
	var opts []EngineOption
	if canonical {
		opts = append(opts, WithCanonicalObjectKeyEUID(true))
	}
	e := NewEngine(fakeStore{text: policy}, time.Minute, opts...)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "u@acme", TenantID: r.TenantID, Roles: []string{"tenant.user"}},
		ActionManageObjectKey,
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
// decision whether the ObjectKey EUID is legacy or canonical — because the
// entity's attributes/parents don't depend on the UID string. This is why the
// switch is behaviourally invisible to real policies.
func TestCanonicalEUID_AttributePolicyUnaffected(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, ObjectKey: "invoices", BackendID: "primary", BucketName: "b1"}
	const policy = `permit(principal, action, resource) when { resource.object_key == "invoices" };`

	if got := authzObjectKey(t, policy, false, r); got != DecisionAllow {
		t.Errorf("legacy-EUID decision = %v, want Allow", got)
	}
	if got := authzObjectKey(t, policy, true, r); got != DecisionAllow {
		t.Errorf("canonical-EUID decision = %v, want Allow (attribute policy must be unaffected)", got)
	}

	// And a non-matching attribute is denied in both modes.
	const denyPolicy = `permit(principal, action, resource) when { resource.object_key == "other" };`
	if got := authzObjectKey(t, denyPolicy, false, r); got != DecisionDeny {
		t.Errorf("legacy non-match = %v, want Deny", got)
	}
	if got := authzObjectKey(t, denyPolicy, true, r); got != DecisionDeny {
		t.Errorf("canonical non-match = %v, want Deny", got)
	}
}

// TestCanonicalEUID_LiteralShapeFlips proves the flag actually changes the
// entity UID: a policy that pins the legacy `resource == ObjectKey::"{tid}/{ok}"`
// literal matches ONLY with the flag off, and a policy pinning the canonical
// A-shape literal matches ONLY with the flag on. (Paladin ships no such literal
// policies — cedar-authoring.md steers against them — this test just pins the
// shape switch.)
func TestCanonicalEUID_LiteralShapeFlips(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, ObjectKey: "invoices", BackendID: "primary", BucketName: "b1"}

	legacyEUID := tid.String() + "/invoices"
	canonicalEUID := "storageBackends/primary/buckets/b1/tenants/" + tid.String() + "/objectKeys/invoices"

	legacyPolicy := `permit(principal, action, resource == ObjectKey::"` + legacyEUID + `");`
	canonicalPolicy := `permit(principal, action, resource == ObjectKey::"` + canonicalEUID + `");`

	// Legacy-literal policy: matches with flag OFF, not with flag ON.
	if got := authzObjectKey(t, legacyPolicy, false, r); got != DecisionAllow {
		t.Errorf("legacy literal, flag off = %v, want Allow", got)
	}
	if got := authzObjectKey(t, legacyPolicy, true, r); got != DecisionDeny {
		t.Errorf("legacy literal, flag on = %v, want Deny (EUID is now canonical)", got)
	}

	// Canonical-literal policy: matches with flag ON, not with flag OFF.
	if got := authzObjectKey(t, canonicalPolicy, true, r); got != DecisionAllow {
		t.Errorf("canonical literal, flag on = %v, want Allow", got)
	}
	if got := authzObjectKey(t, canonicalPolicy, false, r); got != DecisionDeny {
		t.Errorf("canonical literal, flag off = %v, want Deny (EUID is still legacy)", got)
	}
}

// TestCanonicalEUID_FallsBackWithoutBinding: with the flag ON but no
// (backend, bucket) in scope, the EUID stays legacy — canonicalization only
// applies where the binding is available.
func TestCanonicalEUID_FallsBackWithoutBinding(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, ObjectKey: "invoices"} // no backend/bucket
	legacyEUID := tid.String() + "/invoices"
	legacyPolicy := `permit(principal, action, resource == ObjectKey::"` + legacyEUID + `");`

	if got := authzObjectKey(t, legacyPolicy, true, r); got != DecisionAllow {
		t.Errorf("flag on but no binding = %v, want Allow (falls back to legacy EUID)", got)
	}
}

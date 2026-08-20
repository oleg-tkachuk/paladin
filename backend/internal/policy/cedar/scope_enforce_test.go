package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// blanketPermit permits every principal on every action/resource. With it as the
// tenant policy, ANY deny in these tests is attributable solely to the built-in
// scope-enforcement forbid (forbid beats permit in Cedar). That isolates the
// scope logic from role/tenant permit wiring — we are testing the ceiling, not
// the floor.
const blanketPermit = `permit (principal, action, resource);`

// scopeDecision evaluates GetObject for a principal carrying `scopes` against
// resource r, under the blanket permit. principal.TenantID is pinned to the
// resource tenant so membership is never the reason for a deny.
func scopeDecision(t *testing.T, scopes []string, r *Resource) Decision {
	t.Helper()
	e := NewEngine(fakeStore{text: blanketPermit}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "svc", TenantID: r.TenantID, Roles: []string{"tenant.user"}, Scopes: scopes},
		ActionGetObject,
		r,
		RequestContext{Now: time.Now()},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	return dec
}

// objRes is a fully-populated Object resource (Key set → the request resource is
// the Object entity, which carries scope_keys).
func objRes(tenant uuid.UUID, backend, bucket, collection, key string) *Resource {
	return &Resource{TenantID: tenant, BackendID: backend, BucketId: bucket, Collection: collection, Key: key}
}

// TestResourceScopeKeys_WireFormat pins the Go-side scope wire format that the
// enforcement policy matches against. It MUST agree with auth.Scope.String() /
// auth.MatchScope in internal/auth/scope.go — collection is
// "collection:<bucket>/<collection>", NOT "collection:<collection>". If this
// drifts, a scoped principal silently fails-open/closed on the wrong resources.
func TestResourceScopeKeys_WireFormat(t *testing.T) {
	tid := uuid.MustParse("0a8c0000-0000-7000-8000-0000000000aa")
	got := resourceScopeKeys(objRes(tid, "be-1", "medical", "patient-42", "scan.dcm"))
	want := []string{
		"tenant:" + tid.String(),
		"backend:be-1",
		"bucket:medical",
		"collection:medical/patient-42",
	}
	if len(got) != len(want) {
		t.Fatalf("scope keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("scope key[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// collection requires BOTH bucket and collection to be resolved — mirroring
	// MatchScope. With no bucket, only the tenant key is emitted.
	if ks := resourceScopeKeys(&Resource{TenantID: tid, Collection: "patient-42"}); len(ks) != 1 || ks[0] != "tenant:"+tid.String() {
		t.Errorf("no-bucket resource keys = %v, want [tenant:%s] only", ks, tid)
	}
}

// TestScopeEnforcement_Collection: a principal scoped to collection:b/medical may
// touch objects under (bucket b, collection medical) and nothing else.
func TestScopeEnforcement_Collection(t *testing.T) {
	tid := uuid.New()
	scope := []string{"collection:b/medical"}

	if got := scopeDecision(t, scope, objRes(tid, "be", "b", "medical", "f")); got != DecisionAllow {
		t.Errorf("collection:b/medical on {b,medical} = %v, want Allow", got)
	}
	if got := scopeDecision(t, scope, objRes(tid, "be", "b", "avatars", "f")); got != DecisionDeny {
		t.Errorf("collection:b/medical on {b,avatars} = %v, want Deny", got)
	}
	if got := scopeDecision(t, scope, objRes(tid, "be", "c", "medical", "f")); got != DecisionDeny {
		t.Errorf("collection:b/medical on {c,medical} (other bucket) = %v, want Deny", got)
	}
}

// TestScopeEnforcement_Bucket: a principal scoped to bucket:b may touch any
// object in bucket b, and nothing in another bucket.
func TestScopeEnforcement_Bucket(t *testing.T) {
	tid := uuid.New()
	scope := []string{"bucket:b"}

	if got := scopeDecision(t, scope, objRes(tid, "be", "b", "medical", "f")); got != DecisionAllow {
		t.Errorf("bucket:b on {b,medical} = %v, want Allow", got)
	}
	if got := scopeDecision(t, scope, objRes(tid, "be", "b", "avatars", "g")); got != DecisionAllow {
		t.Errorf("bucket:b on {b,avatars} = %v, want Allow", got)
	}
	if got := scopeDecision(t, scope, objRes(tid, "be", "c", "medical", "f")); got != DecisionDeny {
		t.Errorf("bucket:b on {c,medical} (other bucket) = %v, want Deny", got)
	}
}

// TestScopeEnforcement_Tenant: a tenant-scoped principal may touch anything in
// its tenant (this is the scope that works end-to-end on the data plane, where
// tenant_id is always resolved at authz time) and nothing in another tenant.
func TestScopeEnforcement_Tenant(t *testing.T) {
	tid := uuid.New()
	other := uuid.New()
	scope := []string{"tenant:" + tid.String()}

	if got := scopeDecision(t, scope, objRes(tid, "be", "b", "medical", "f")); got != DecisionAllow {
		t.Errorf("tenant:self on own-tenant object = %v, want Allow", got)
	}
	// A resource in a different tenant — the principal's tenant scope doesn't
	// admit it, so the forbid fires. (principal.TenantID follows the resource so
	// membership isn't the cause; the scope mismatch is.)
	if got := scopeDecision(t, scope, objRes(other, "be", "b", "medical", "f")); got != DecisionDeny {
		t.Errorf("tenant:%s on foreign-tenant object = %v, want Deny", tid, got)
	}
}

// TestScopeEnforcement_EmptyScopesUnaffected proves the guard: a principal with
// NO scopes is allowed exactly where the base policy already allowed — the
// scope-enforcement forbid never fires. This is the JWT-user / roles-only /
// unscoped-PAT case and MUST stay unchanged.
func TestScopeEnforcement_EmptyScopesUnaffected(t *testing.T) {
	tid := uuid.New()
	// A resource that matches no particular scope — with empty scopes the forbid
	// is inert, so the blanket permit stands.
	if got := scopeDecision(t, nil, objRes(tid, "be", "any-bucket", "any-ok", "f")); got != DecisionAllow {
		t.Errorf("empty-scopes principal = %v, want Allow (guard must keep it unaffected)", got)
	}
	if got := scopeDecision(t, []string{}, objRes(tid, "be", "b", "medical", "f")); got != DecisionAllow {
		t.Errorf("explicit empty-slice scopes = %v, want Allow", got)
	}
}

// TestScopeEnforcement_WildcardUnaffected: a principal carrying the "*" wildcard
// scope (reserved for platform admins) is exempt — the forbid's guard excludes
// it — so it is allowed even on a resource no other scope would admit.
func TestScopeEnforcement_WildcardUnaffected(t *testing.T) {
	tid := uuid.New()
	if got := scopeDecision(t, []string{"*"}, objRes(tid, "be", "somewhere", "else", "f")); got != DecisionAllow {
		t.Errorf("wildcard-scoped principal = %v, want Allow", got)
	}
	// Wildcard alongside a narrow scope still exempts (wildcard present ⇒ guard false).
	if got := scopeDecision(t, []string{"bucket:b", "*"}, objRes(tid, "be", "c", "x", "f")); got != DecisionAllow {
		t.Errorf("wildcard+narrow scoped principal = %v, want Allow", got)
	}
}

// TestScopeEnforcement_RolesOnlyUnaffected pins that a roles-only principal (the
// Cedar view of a JWT/role caller — Kind is not modeled here; empty scopes is
// the signal) is untouched by the forbid even on a non-matching resource.
func TestScopeEnforcement_RolesOnlyUnaffected(t *testing.T) {
	tid := uuid.New()
	e := NewEngine(fakeStore{text: blanketPermit}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "u@acme", TenantID: tid, Roles: []string{"tenant.admin", "bucket.admin"}}, // no scopes
		ActionGetObject,
		objRes(tid, "be", "b", "medical", "f"),
		RequestContext{Now: time.Now()},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionAllow {
		t.Errorf("roles-only principal = %v, want Allow (unaffected by scope enforcement)", dec)
	}
}

// TestScopeEnforcement_CollectionResourceEntity mirrors Collection-level access
// (no per-object Key → the request resource is the Collection entity), confirming
// scope_keys is stamped there too, not only on Object.
func TestScopeEnforcement_CollectionResourceEntity(t *testing.T) {
	tid := uuid.New()
	scope := []string{"collection:b/medical"}
	okRes := func(bucket, ok string) *Resource {
		return &Resource{TenantID: tid, BackendID: "be", BucketId: bucket, Collection: ok}
	}
	if got := scopeDecision(t, scope, okRes("b", "medical")); got != DecisionAllow {
		t.Errorf("Collection entity {b,medical} = %v, want Allow", got)
	}
	if got := scopeDecision(t, scope, okRes("b", "avatars")); got != DecisionDeny {
		t.Errorf("Collection entity {b,avatars} = %v, want Deny", got)
	}
}

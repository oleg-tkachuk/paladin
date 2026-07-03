package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeStore returns a fixed tenant policy text (concatenated with the
// built-in policy by the engine), a fixed authoritative slug, and a no-op
// Watch. slug defaults to "" (membership-irrelevant tests); isolation tests
// set it to the tenant's real slug (ADR-0012).
type fakeStore struct{ text, slug string }

func (f fakeStore) Fetch(context.Context, uuid.UUID, string) (string, []byte, string, error) {
	return f.text, []byte(f.text), f.slug, nil
}
func (f fakeStore) Watch(context.Context) (<-chan ChangeEvent, error) { return nil, nil }

func authzOAuth(t *testing.T, policy, clientID string) Decision {
	t.Helper()
	e := NewEngine(fakeStore{text: policy}, time.Minute)
	tid := uuid.New()
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "u@acme", TenantID: tid, Roles: []string{"tenant.user"}},
		ActionAuthorizeOAuth,
		&Resource{TenantID: tid},
		RequestContext{OAuthClientID: clientID, OAuthScopes: []string{"paladin.read"}},
	)
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	return dec
}

// The built-in policy permits AuthorizeOAuth for any authenticated principal,
// so consent works out of the box with an empty tenant policy.
func TestAuthorizeOAuth_DefaultPermit(t *testing.T) {
	if got := authzOAuth(t, "", "claude-desktop"); got != DecisionAllow {
		t.Fatalf("default decision = %v, want Allow", got)
	}
}

// A tenant policy can forbid specific clients via context.oauth_client_id;
// first-forbid beats the built-in permit, and other clients still pass.
func TestAuthorizeOAuth_TenantForbidByClient(t *testing.T) {
	const policy = `forbid(principal, action == Action::"AuthorizeOAuth", resource)
when { context.oauth_client_id == "blocked-client" };`

	if got := authzOAuth(t, policy, "blocked-client"); got != DecisionDeny {
		t.Errorf("blocked client decision = %v, want Deny", got)
	}
	if got := authzOAuth(t, policy, "claude-desktop"); got != DecisionAllow {
		t.Errorf("other client decision = %v, want Allow", got)
	}
}

// A tenant policy can forbid by requested scope via context.oauth_scopes.
func TestAuthorizeOAuth_TenantForbidByScope(t *testing.T) {
	const policy = `forbid(principal, action == Action::"AuthorizeOAuth", resource)
when { context.oauth_scopes.contains("paladin.read") };`
	if got := authzOAuth(t, policy, "claude-desktop"); got != DecisionDeny {
		t.Fatalf("scope-forbid decision = %v, want Deny", got)
	}
}

// TestBuiltin_ReadOwnTenant pins the builtin member-level ReadTenant permit
// against an EMPTY stored policy — the bootstrap tenant's shape, where only
// the builtins apply. Own-tenant reads allow for any member; foreign-tenant
// reads stay denied (the handler additionally platform-admin-gates them).
func TestBuiltin_ReadOwnTenant(t *testing.T) {
	e := NewEngine(fakeStore{text: ""}, time.Minute)
	tid := uuid.MustParse("0a8c0000-0000-7000-8000-000000000aaa")
	foreign := uuid.MustParse("0a8c0000-0000-7000-8000-000000000bbb")

	authz := func(roles []string, resourceTenant uuid.UUID) Decision {
		t.Helper()
		dec, err := e.IsAuthorized(context.Background(),
			&Principal{Subject: "op-1", TenantID: tid, Roles: roles},
			ActionReadTenant,
			&Resource{TenantID: resourceTenant},
			RequestContext{Now: time.Now()},
		)
		if err != nil {
			t.Fatalf("IsAuthorized: %v", err)
		}
		return dec
	}

	if got := authz([]string{"tenant.admin"}, tid); got != DecisionAllow {
		t.Errorf("tenant.admin own-tenant read on empty policy = %v, want Allow", got)
	}
	if got := authz([]string{"tenant.user"}, tid); got != DecisionAllow {
		t.Errorf("plain member own-tenant read on empty policy = %v, want Allow", got)
	}
	if got := authz([]string{"tenant.admin"}, foreign); got != DecisionDeny {
		t.Errorf("foreign-tenant read = %v, want Deny", got)
	}
}

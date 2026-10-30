package userh

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// fakeGrants records what the handler asked of the store.
type fakeGrants struct {
	grants         []authstore.UserOAuthGrant
	listedFor      uuid.UUID
	listedInactive bool
	revoked        struct{ user, grant uuid.UUID }
	found          bool
}

func (f *fakeGrants) ListOAuthGrants(_ context.Context, userID uuid.UUID, _ time.Time, includeInactive bool) ([]authstore.UserOAuthGrant, error) {
	f.listedFor, f.listedInactive = userID, includeInactive
	return f.grants, nil
}

func (f *fakeGrants) RevokeOAuthGrant(_ context.Context, userID, grantID uuid.UUID) (bool, error) {
	f.revoked.user, f.revoked.grant = userID, grantID
	return f.found, nil
}

// recordingAuthorizer allows, and remembers the action it was asked.
type recordingAuthorizer struct{ action cedar.Action }

func (r *recordingAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, a cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	r.action = a
	return cedar.DecisionAllow, nil
}

func grantFixture(t *testing.T) (authstore.User, *fakeGrants, *recordingAuthorizer, *Handler) {
	t.Helper()
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice"}
	g := &fakeGrants{grants: []authstore.UserOAuthGrant{{ID: uuid.New(), Grant: authstore.OAuthGrant{ClientID: "agent"}}}}
	az := &recordingAuthorizer{}
	return u, g, az, NewHandler(&fakeUserRepo{user: u}, az).WithOAuthGrants(g)
}

func TestListOAuthGrants(t *testing.T) {
	u, g, az, h := grantFixture(t)
	got, err := h.ListOAuthGrants(ctxAs(u.TenantID), ref(u), true)
	if err != nil {
		t.Fatal(err)
	}
	if !g.listedInactive {
		t.Error("include_inactive was not passed to the store")
	}
	if len(got) != 1 || g.listedFor != u.UserID {
		t.Errorf("listed %d grants for %s, want 1 for %s", len(got), g.listedFor, u.UserID)
	}
	if az.action != cedar.ActionReadOAuthGrants {
		t.Errorf("authorized %s, want %s", az.action, cedar.ActionReadOAuthGrants)
	}
}

func TestRevokeOAuthGrant(t *testing.T) {
	u, g, az, h := grantFixture(t)
	grant := g.grants[0].ID

	g.found = true
	if err := h.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), grant); err != nil {
		t.Fatal(err)
	}
	if g.revoked.user != u.UserID || g.revoked.grant != grant {
		t.Errorf("revoked %v, want user %s grant %s", g.revoked, u.UserID, grant)
	}
	if az.action != cedar.ActionRevokeOAuthGrant {
		t.Errorf("authorized %s, want %s", az.action, cedar.ActionRevokeOAuthGrant)
	}

	if err := h.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), uuid.New()); code(err) != connect.CodeNotFound {
		t.Errorf("a grant that is not the user's: code = %v, want NotFound", code(err))
	}
}

func TestOAuthGrants_Refusals(t *testing.T) {
	u, g, _, _ := grantFixture(t)

	// Another tenant's user is not found, to anyone but a platform admin.
	h := NewHandler(&fakeUserRepo{user: u}, allowAuthorizer{}).WithOAuthGrants(g)
	if _, err := h.ListOAuthGrants(ctxAs(uuid.New(), "tenant.admin"), ref(u), false); code(err) != connect.CodeNotFound {
		t.Errorf("another tenant's user: code = %v, want NotFound", code(err))
	}
	if _, err := h.ListOAuthGrants(ctxAs(uuid.New(), "platform.admin"), ref(u), false); err != nil {
		t.Errorf("a platform admin: %v", err)
	}

	// Cedar decides the rest.
	denied := NewHandler(&fakeUserRepo{user: u}, denyAuthorizer{}).WithOAuthGrants(g)
	if err := denied.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), uuid.New()); code(err) != connect.CodePermissionDenied {
		t.Errorf("denied by policy: code = %v, want PermissionDenied", code(err))
	}

	// No store: the RPCs are not served, rather than answering empty.
	bare := NewHandler(&fakeUserRepo{user: u}, allowAuthorizer{})
	if _, err := bare.ListOAuthGrants(ctxAs(u.TenantID), ref(u), false); code(err) != connect.CodeUnimplemented {
		t.Errorf("no store: code = %v, want Unimplemented", code(err))
	}

	// No caller at all.
	if _, err := h.ListOAuthGrants(context.Background(), ref(u), false); code(err) != connect.CodeUnauthenticated {
		t.Errorf("anonymous: code = %v, want Unauthenticated", code(err))
	}
}

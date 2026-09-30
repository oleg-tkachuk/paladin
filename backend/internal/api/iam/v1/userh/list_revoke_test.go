package userh

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// ListUsers and RevokeScopes were two of the thirteen handlers at 0.0% across
// unit and both integration suites (BACKLOG: "Half the admin API's RPCs have
// no behavioural test").
//
// ListUsers is the one that matters. It does not refuse a non-admin who asks
// for everyone — it silently narrows them to their own tenant, and refuses
// only when they name someone else's. Both halves are tenant isolation, and a
// refactor that turned the narrowing into a pass-through would leak every
// user in the deployment while every existing test stayed green.

type listingRepo struct {
	fakeUserRepo
	sawArgs authstore.ListUsersArgs
	page    []authstore.User
	next    string
	listErr error
}

func (r *listingRepo) List(_ context.Context, a authstore.ListUsersArgs) ([]authstore.User, string, error) {
	r.sawArgs = a
	return r.page, r.next, r.listErr
}

func TestListUsersNarrowsANonAdminToTheirOwnTenant(t *testing.T) {
	caller := uuid.New()
	repo := &listingRepo{next: "cursor"}
	h := NewHandler(repo, allowAuthorizer{})

	// TenantID unset means "every tenant". A tenant admin asking for that is
	// not refused — the scope is quietly reduced to their own.
	_, next, err := h.ListUsers(ctxAs(caller, "tenant.admin"), ListUsersInput{PageSize: 25})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if repo.sawArgs.TenantID != caller {
		t.Fatalf("repo was asked for tenant %v, want the caller's %v — an unscoped list would return every user in the deployment",
			repo.sawArgs.TenantID, caller)
	}
	if next != "cursor" {
		t.Errorf("next = %q, want the repo's cursor", next)
	}
}

func TestListUsersLetsPlatformAdminListEveryTenant(t *testing.T) {
	repo := &listingRepo{}
	h := NewHandler(repo, allowAuthorizer{})

	if _, _, err := h.ListUsers(ctxAs(uuid.New(), "platform.admin"), ListUsersInput{}); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	// Nil is the cross-tenant scan, and only this role reaches it.
	if repo.sawArgs.TenantID != uuid.Nil {
		t.Errorf("repo was scoped to %v, want the cross-tenant Nil", repo.sawArgs.TenantID)
	}
}

func TestListUsersRefusesANamedForeignTenant(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&listingRepo{}, allowAuthorizer{})

	// Asking for someone else's tenant BY NAME is refused rather than
	// narrowed: the caller said what they wanted, and the answer is no.
	_, _, err := h.ListUsers(ctxAs(caller, "tenant.admin"), ListUsersInput{TenantID: uuid.New()})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestListUsersAllowsANonAdminToNameTheirOwnTenant(t *testing.T) {
	caller := uuid.New()
	repo := &listingRepo{}
	h := NewHandler(repo, allowAuthorizer{})

	if _, _, err := h.ListUsers(ctxAs(caller, "tenant.admin"), ListUsersInput{TenantID: caller}); err != nil {
		t.Fatalf("naming one's own tenant was refused: %v", err)
	}
	if repo.sawArgs.TenantID != caller {
		t.Errorf("scope = %v, want %v", repo.sawArgs.TenantID, caller)
	}
}

func TestListUsersPassesPagingAndFilterThrough(t *testing.T) {
	repo := &listingRepo{}
	h := NewHandler(repo, allowAuthorizer{})

	if _, _, err := h.ListUsers(ctxAs(uuid.New(), "platform.admin"), ListUsersInput{
		PageSize: 7, PageToken: "tok", Filter: `disabled == false`,
	}); err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if repo.sawArgs.PageSize != 7 || repo.sawArgs.PageToken != "tok" || repo.sawArgs.Filter != `disabled == false` {
		t.Errorf("repo saw pageSize=%d token=%q filter=%q — the request was not carried through",
			repo.sawArgs.PageSize, repo.sawArgs.PageToken, repo.sawArgs.Filter)
	}
}

func TestListUsersDeniedByCedar(t *testing.T) {
	h := NewHandler(&listingRepo{}, denyAuthorizer{})

	_, _, err := h.ListUsers(ctxAs(uuid.New(), "platform.admin"), ListUsersInput{})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied — the role is not the only gate", code(err))
	}
}

func TestRevokeScopesRemovesOnlyWhatWasNamed(t *testing.T) {
	id := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{
		UserID: id,
		Scopes: []auth.Scope{
			{Type: auth.ScopeBucket, Value: "docs"},
			{Type: auth.ScopeBucket, Value: "media"},
			{Type: auth.ScopeCollection, Value: "reports"},
		},
	}}
	h := NewHandler(repo, allowAuthorizer{})

	got, err := h.RevokeScopes(ctxAs(uuid.New(), "platform.admin"), id, []auth.Scope{{Type: auth.ScopeBucket, Value: "media"}})
	if err != nil {
		t.Fatalf("RevokeScopes: %v", err)
	}
	// Revocation is subtraction, not replacement. A handler that wrote the
	// argument straight through would leave the user with exactly the scope
	// the caller meant to take away.
	if len(got.Scopes) != 2 {
		t.Fatalf("scopes = %v, want the two that were not named", got.Scopes)
	}
	for _, s := range got.Scopes {
		if s.Type == auth.ScopeBucket && s.Value == "media" {
			t.Error("the revoked scope survived")
		}
	}
}

func TestRevokeScopesIsIndifferentToScopesNotHeld(t *testing.T) {
	id := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{UserID: id, Scopes: []auth.Scope{{Type: auth.ScopeBucket, Value: "docs"}}}}
	h := NewHandler(repo, allowAuthorizer{})

	got, err := h.RevokeScopes(ctxAs(uuid.New(), "platform.admin"), id, []auth.Scope{{Type: auth.ScopeBackend, Value: "primary"}})
	if err != nil {
		t.Fatalf("revoking a scope the user never had should be a no-op, got: %v", err)
	}
	if len(got.Scopes) != 1 || got.Scopes[0].Value != "docs" {
		t.Errorf("scopes = %v, want the untouched original", got.Scopes)
	}
}

func TestRevokeScopesUnknownUserIsNotFound(t *testing.T) {
	h := NewHandler(&fakeUserRepo{getErr: authstore.ErrNotFound}, allowAuthorizer{})

	_, err := h.RevokeScopes(ctxAs(uuid.New(), "platform.admin"), uuid.New(), []auth.Scope{{Type: auth.ScopeBucket, Value: "docs"}})
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestRevokeScopesDeniedByCedar(t *testing.T) {
	id := uuid.New()
	h := NewHandler(&fakeUserRepo{user: authstore.User{UserID: id}}, denyAuthorizer{})

	_, err := h.RevokeScopes(ctxAs(uuid.New(), "platform.admin"), id, []auth.Scope{{Type: auth.ScopeBucket, Value: "docs"}})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

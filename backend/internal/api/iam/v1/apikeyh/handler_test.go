package apikeyh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// stubMinter is the seam the issuer-interface extraction made testable: it
// records the claims it was asked to mint and returns a fixed token.
type stubMinter struct {
	claims issuer.AccessClaims
	calls  int
}

func (m *stubMinter) MintAccess(c issuer.AccessClaims) (string, time.Time, error) {
	m.claims = c
	m.calls++
	return "tok-123", time.Unix(1_900_000_000, 0), nil
}

type fakeKeyRepo struct {
	key         authstore.ApiKey
	getErr      error
	createErr   error
	created     authstore.ApiKey
	revokeCalls int
}

func (f *fakeKeyRepo) Create(_ context.Context, k authstore.ApiKey) (authstore.ApiKey, error) {
	f.created = k
	if f.createErr != nil {
		return authstore.ApiKey{}, f.createErr
	}
	k.ApiKeyID = uuid.New()
	return k, nil
}
func (f *fakeKeyRepo) GetByID(context.Context, uuid.UUID) (authstore.ApiKey, error) {
	return f.key, f.getErr
}
func (f *fakeKeyRepo) GetByPrefix(context.Context, string) (authstore.ApiKey, error) {
	return authstore.ApiKey{}, authstore.ErrNotFound
}
func (f *fakeKeyRepo) List(context.Context, authstore.ListApiKeysArgs) ([]authstore.ApiKey, string, error) {
	return nil, "", nil
}
func (f *fakeKeyRepo) Revoke(context.Context, uuid.UUID) error {
	f.revokeCalls++
	return nil
}
func (f *fakeKeyRepo) UpdateSecretHash(context.Context, uuid.UUID, []byte, time.Time) error {
	return nil
}
func (f *fakeKeyRepo) TouchUse(context.Context, uuid.UUID, time.Time) error { return nil }

func ctxAs(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", TenantID: tenant, Roles: roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

func bucketScope(v string) auth.Scope { return auth.Scope{Type: auth.ScopeBucket, Value: v} }

// ─── CreateApiKey ────────────────────────────────────────────────────────────

func TestCreateApiKey_DescriptionRequired(t *testing.T) {
	h := NewHandler(&fakeKeyRepo{}, &stubMinter{}, allowAuthorizer{})
	_, err := h.CreateApiKey(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		CreateApiKeyInput{TenantID: uuid.New()})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestCreateApiKey_SuccessHashesSecret(t *testing.T) {
	repo := &fakeKeyRepo{}
	h := NewHandler(repo, &stubMinter{}, allowAuthorizer{})
	out, err := h.CreateApiKey(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		CreateApiKeyInput{TenantID: uuid.New(), Description: "ci-runner"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Secret == "" {
		t.Error("Create should return the one-time plaintext secret")
	}
	if len(repo.created.SecretHash) == 0 || string(repo.created.SecretHash) == out.Secret {
		t.Error("persisted secret must be a hash, never the plaintext")
	}
}

func TestCreateApiKey_CedarDenied(t *testing.T) {
	h := NewHandler(&fakeKeyRepo{}, &stubMinter{}, denyAuthorizer{})
	_, err := h.CreateApiKey(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		CreateApiKeyInput{TenantID: uuid.New(), Description: "x"})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

// ─── GetApiKey ───────────────────────────────────────────────────────────────

func TestGetApiKey_NotFound(t *testing.T) {
	h := NewHandler(&fakeKeyRepo{getErr: authstore.ErrNotFound}, &stubMinter{}, allowAuthorizer{})
	_, err := h.GetApiKey(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New())
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGetApiKey_CrossTenantHidden(t *testing.T) {
	caller := uuid.New()
	repo := &fakeKeyRepo{key: authstore.ApiKey{ApiKeyID: uuid.New(), TenantID: uuid.New()}}
	h := NewHandler(repo, &stubMinter{}, allowAuthorizer{})
	_, err := h.GetApiKey(ctxAs(caller, apiutil.RoleTenantAdmin), repo.key.ApiKeyID)
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound (cross-tenant hidden)", code(err))
	}
}

// ─── MintScopedToken ─────────────────────────────────────────────────────────

func TestMintScopedToken_InputValidation(t *testing.T) {
	h := NewHandler(&fakeKeyRepo{}, &stubMinter{}, allowAuthorizer{})
	ctx := ctxAs(uuid.New(), apiutil.RolePlatformAdmin)
	base := MintScopedTokenInput{ApiKeyID: uuid.New(), Audience: "data", Scopes: []auth.Scope{bucketScope("b1")}, TTL: time.Hour}

	noAud := base
	noAud.Audience = ""
	if _, _, err := h.MintScopedToken(ctx, noAud); code(err) != connect.CodeInvalidArgument {
		t.Errorf("missing audience: code = %v, want InvalidArgument", code(err))
	}
	noScopes := base
	noScopes.Scopes = nil
	if _, _, err := h.MintScopedToken(ctx, noScopes); code(err) != connect.CodeInvalidArgument {
		t.Errorf("missing scopes: code = %v, want InvalidArgument", code(err))
	}
	noTTL := base
	noTTL.TTL = 0
	if _, _, err := h.MintScopedToken(ctx, noTTL); code(err) != connect.CodeInvalidArgument {
		t.Errorf("missing ttl: code = %v, want InvalidArgument", code(err))
	}
}

func TestMintScopedToken_ParentNotFound(t *testing.T) {
	h := NewHandler(&fakeKeyRepo{getErr: authstore.ErrNotFound}, &stubMinter{}, allowAuthorizer{})
	_, _, err := h.MintScopedToken(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		MintScopedTokenInput{ApiKeyID: uuid.New(), Audience: "data", Scopes: []auth.Scope{bucketScope("b1")}, TTL: time.Hour})
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestMintScopedToken_RevokedParentDenied(t *testing.T) {
	repo := &fakeKeyRepo{key: authstore.ApiKey{
		ApiKeyID: uuid.New(), Revoked: true, Scopes: []auth.Scope{bucketScope("b1")},
	}}
	h := NewHandler(repo, &stubMinter{}, allowAuthorizer{})
	_, _, err := h.MintScopedToken(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		MintScopedTokenInput{ApiKeyID: repo.key.ApiKeyID, Audience: "data", Scopes: []auth.Scope{bucketScope("b1")}, TTL: time.Hour})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (revoked parent)", code(err))
	}
}

func TestMintScopedToken_ScopeEscalationDenied(t *testing.T) {
	// Parent grants only b1; the request asks for b2 — must be refused. This is
	// the load-bearing security invariant: a scoped token can never broaden its
	// parent's authority.
	repo := &fakeKeyRepo{key: authstore.ApiKey{
		ApiKeyID: uuid.New(), Scopes: []auth.Scope{bucketScope("b1")},
	}}
	minter := &stubMinter{}
	h := NewHandler(repo, minter, allowAuthorizer{})
	_, _, err := h.MintScopedToken(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		MintScopedTokenInput{ApiKeyID: repo.key.ApiKeyID, Audience: "data", Scopes: []auth.Scope{bucketScope("b2")}, TTL: time.Hour})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (scope escalation)", code(err))
	}
	if minter.calls != 0 {
		t.Error("no token must be minted when scopes escalate")
	}
}

func TestMintScopedToken_SubsetSuccess(t *testing.T) {
	id := uuid.New()
	repo := &fakeKeyRepo{key: authstore.ApiKey{
		ApiKeyID: id, TenantID: uuid.New(),
		Roles:  []string{"tenant.user"},
		Scopes: []auth.Scope{bucketScope("b1"), bucketScope("b2")},
	}}
	minter := &stubMinter{}
	h := NewHandler(repo, minter, allowAuthorizer{})
	tok, _, err := h.MintScopedToken(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		MintScopedTokenInput{ApiKeyID: id, Audience: "data", Scopes: []auth.Scope{bucketScope("b1")}, TTL: time.Hour})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "tok-123" {
		t.Errorf("got token %q, want the minted token", tok)
	}
	if minter.claims.Subject != id.String() {
		t.Errorf("minted sub = %q, want the parent api_key id %s", minter.claims.Subject, id)
	}
	if minter.claims.Audience != "data" || len(minter.claims.Scopes) != 1 {
		t.Errorf("minted claims mismatch: %+v", minter.claims)
	}
}

func TestMintScopedToken_WildcardParentAllowsAny(t *testing.T) {
	repo := &fakeKeyRepo{key: authstore.ApiKey{
		ApiKeyID: uuid.New(), Scopes: []auth.Scope{{Type: auth.ScopeWildcard}},
	}}
	h := NewHandler(repo, &stubMinter{}, allowAuthorizer{})
	_, _, err := h.MintScopedToken(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		MintScopedTokenInput{ApiKeyID: repo.key.ApiKeyID, Audience: "data", Scopes: []auth.Scope{bucketScope("anything")}, TTL: time.Hour})
	if err != nil {
		t.Fatalf("wildcard parent should permit any sub-scope, got %v", err)
	}
}

// ─── RevokeApiKey ────────────────────────────────────────────────────────────

func TestRevokeApiKey_Success(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeKeyRepo{key: authstore.ApiKey{ApiKeyID: id, TenantID: caller}}
	h := NewHandler(repo, &stubMinter{}, allowAuthorizer{})
	if err := h.RevokeApiKey(ctxAs(caller, apiutil.RolePlatformAdmin), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.revokeCalls != 1 {
		t.Errorf("Revoke called %d times, want 1", repo.revokeCalls)
	}
}

// Compile-time interface assertions — including the new minter seam.
var (
	_ authstore.ApiKeyRepository = (*fakeKeyRepo)(nil)
	_ tokenMinter                = (*stubMinter)(nil)
	_ cedar.Authorizer           = allowAuthorizer{}
)

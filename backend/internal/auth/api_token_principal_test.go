package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
)

// recordingVerifier is a TokenVerifier that records whether it was called — so a test can
// assert the JWT gate was (or was NOT) consulted.
type recordingVerifier struct {
	called    bool
	principal *Principal
	err       error
}

func (r *recordingVerifier) Verify(_ context.Context, _ string) (*Principal, error) {
	r.called = true
	return r.principal, r.err
}

func newReq(authz string) *connect.Request[emptypb.Empty] {
	req := connect.NewRequest(&emptypb.Empty{})
	if authz != "" {
		req.Header().Set("Authorization", authz)
	}
	return req
}

// TestInterceptorSkipAPITokens proves the data-plane JWT gate passes an `paladin_pat_…` bearer
// through untouched (deferring to the API-token interceptor) while still verifying JWTs and
// rejecting a missing bearer.
func TestInterceptorSkipAPITokens(t *testing.T) {
	tid := uuid.New()
	t.Run("PAT bearer skips JWT verification", func(t *testing.T) {
		v := &recordingVerifier{}
		var sawPrincipal bool
		next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			_, err := PrincipalFromContext(ctx)
			sawPrincipal = err == nil
			return connect.NewResponse(&emptypb.Empty{}), nil
		}
		_, err := InterceptorSkipAPITokens(v).WrapUnary(next)(context.Background(), newReq("Bearer paladin_pat_abc123"))
		if err != nil {
			t.Fatalf("PAT bearer should pass through, got %v", err)
		}
		if v.called {
			t.Error("JWT verifier must NOT be called for a PAT bearer")
		}
		if sawPrincipal {
			t.Error("the JWT gate must not set a principal for a PAT (the API-token interceptor does)")
		}
	})

	t.Run("JWT bearer is verified and sets the principal", func(t *testing.T) {
		v := &recordingVerifier{principal: &Principal{TenantID: tid, Audience: AudienceData}}
		var gotTenant uuid.UUID
		next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			if p, err := PrincipalFromContext(ctx); err == nil {
				gotTenant = p.TenantID
			}
			return connect.NewResponse(&emptypb.Empty{}), nil
		}
		if _, err := InterceptorSkipAPITokens(v).WrapUnary(next)(context.Background(), newReq("Bearer aaa.bbb.ccc")); err != nil {
			t.Fatal(err)
		}
		if !v.called {
			t.Error("JWT verifier must be called for a non-PAT bearer")
		}
		if gotTenant != tid {
			t.Errorf("principal tenant = %v, want %v", gotTenant, tid)
		}
	})

	t.Run("missing bearer is rejected", func(t *testing.T) {
		v := &recordingVerifier{}
		next := func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
			t.Fatal("handler must not run without auth")
			return nil, nil
		}
		_, err := InterceptorSkipAPITokens(v).WrapUnary(next)(context.Background(), newReq(""))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("missing bearer: got %v, want Unauthenticated", err)
		}
	})
}

// TestAPITokenEstablishesPrincipal proves a verified API token yields a tenant-scoped
// ApiKey principal with the plane audience — but only when the interceptor is the
// principal-establishing variant, and never overwriting an existing principal.
func TestAPITokenEstablishesPrincipal(t *testing.T) {
	tid := uuid.New()
	tok := &api_token.Token{ID: uuid.New(), TenantID: tid}

	t.Run("establishing variant sets an ApiKey principal (aud=paladin-data)", func(t *testing.T) {
		i := &apiTokenInterceptor{audience: "data", establishPrincipal: true}
		ctx, iderr := i.withTokenIdentity(context.Background(), tok)
		if iderr != nil {
			t.Fatalf("withTokenIdentity: %v", iderr)
		}
		p, err := PrincipalFromContext(ctx)
		if err != nil {
			t.Fatalf("expected a principal, got %v", err)
		}
		if p.TenantID != tid {
			t.Errorf("tenant = %v, want %v", p.TenantID, tid)
		}
		if p.Audience != AudienceData {
			t.Errorf("audience = %q, want %q", p.Audience, AudienceData)
		}
		if p.Kind != PrincipalKindApiKey {
			t.Errorf("kind = %v, want ApiKey", p.Kind)
		}
		if len(p.Roles) != 0 || len(p.Scopes) != 0 {
			t.Errorf("expected least-privilege (no roles/scopes), got roles=%v scopes=%v", p.Roles, p.Scopes)
		}
		if _, ok := APITokenFromContext(ctx); !ok {
			t.Error("the verified token must also be on the context")
		}
	})

	t.Run("additive variant does not set a principal", func(t *testing.T) {
		i := &apiTokenInterceptor{audience: "iam", establishPrincipal: false}
		ctx, iderr := i.withTokenIdentity(context.Background(), tok)
		if iderr != nil {
			t.Fatalf("withTokenIdentity: %v", iderr)
		}
		if _, err := PrincipalFromContext(ctx); err == nil {
			t.Error("additive api-token interceptor must not establish a principal")
		}
	})

	t.Run("does not overwrite an existing principal", func(t *testing.T) {
		existing := &Principal{TenantID: uuid.New(), Subject: "user-1", Audience: AudienceData}
		i := &apiTokenInterceptor{audience: "data", establishPrincipal: true}
		ctx, iderr := i.withTokenIdentity(WithPrincipal(context.Background(), existing), tok)
		if iderr != nil {
			t.Fatalf("withTokenIdentity: %v", iderr)
		}
		p, _ := PrincipalFromContext(ctx)
		if p.Subject != "user-1" {
			t.Errorf("existing principal was overwritten: subject=%q", p.Subject)
		}
	})
}

// TestAPITokenPrincipalScopes proves the opt-in half of the scope model at the
// interceptor boundary: a token minted WITH scopes surfaces them (parsed) on
// the derived principal, an unscoped token stays scope-free (today's baseline),
// and a token whose scope string can't parse fails the request fail-closed
// rather than silently dropping the scope and going tenant-wide.
func TestAPITokenPrincipalScopes(t *testing.T) {
	tid := uuid.New()

	t.Run("token scopes are parsed onto the principal", func(t *testing.T) {
		tok := &api_token.Token{
			ID:       uuid.New(),
			TenantID: tid,
			Scopes:   []string{"bucket:medical", "collection:medical/patient-42"},
		}
		i := &apiTokenInterceptor{audience: "data", establishPrincipal: true}
		ctx, err := i.withTokenIdentity(context.Background(), tok)
		if err != nil {
			t.Fatalf("withTokenIdentity: %v", err)
		}
		p, err := PrincipalFromContext(ctx)
		if err != nil {
			t.Fatalf("expected a principal, got %v", err)
		}
		if len(p.Scopes) != 2 {
			t.Fatalf("scopes len = %d, want 2 (%v)", len(p.Scopes), p.Scopes)
		}
		if p.Scopes[0].Type != ScopeBucket || p.Scopes[0].Value != "medical" {
			t.Errorf("scope[0] = %+v, want bucket:medical", p.Scopes[0])
		}
		if p.Scopes[1].Type != ScopeCollection || p.Scopes[1].Value != "medical/patient-42" {
			t.Errorf("scope[1] = %+v, want collection:medical/patient-42", p.Scopes[1])
		}
		// Wire round-trip: String() must reproduce the exact mint strings.
		if got := p.Scopes[1].String(); got != "collection:medical/patient-42" {
			t.Errorf("String() = %q, want collection:medical/patient-42", got)
		}
	})

	t.Run("unscoped token stays scope-free (baseline unaffected)", func(t *testing.T) {
		tok := &api_token.Token{ID: uuid.New(), TenantID: tid}
		i := &apiTokenInterceptor{audience: "data", establishPrincipal: true}
		ctx, err := i.withTokenIdentity(context.Background(), tok)
		if err != nil {
			t.Fatalf("withTokenIdentity: %v", err)
		}
		p, _ := PrincipalFromContext(ctx)
		if len(p.Scopes) != 0 {
			t.Errorf("unscoped token yielded scopes %v, want none", p.Scopes)
		}
	})

	t.Run("malformed scope fails closed (no principal established)", func(t *testing.T) {
		tok := &api_token.Token{
			ID:       uuid.New(),
			TenantID: tid,
			Scopes:   []string{"not-a-valid-scope-type:x"},
		}
		i := &apiTokenInterceptor{audience: "data", establishPrincipal: true}
		ctx, err := i.withTokenIdentity(context.Background(), tok)
		if err == nil {
			t.Fatal("expected an error for a malformed token scope, got nil")
		}
		// Fail-closed: the derived-principal path errored, so no unrestricted
		// tenant-wide principal was stamped onto the context as a fallback.
		if _, perr := PrincipalFromContext(ctx); perr == nil {
			t.Error("a malformed scope must NOT fall back to an unrestricted principal")
		}
	})
}

func TestPrincipalAudienceFor(t *testing.T) {
	cases := map[string]string{"data": AudienceData, "admin": AudienceAdmin, "iam": AudienceIAM, "mcp": "mcp"}
	for label, want := range cases {
		if got := principalAudienceFor(label); got != want {
			t.Errorf("principalAudienceFor(%q) = %q, want %q", label, got, want)
		}
	}
}

// ─── roles on tokens ───────────────────────────────────────────────────────

// A token's roles must reach the principal, or a machine caller can satisfy no
// role-gated policy — the blocker that forced a consumer to hold either a
// credential per tenant or a human session.
func TestPrincipalFromAPIToken_CarriesRoles(t *testing.T) {
	tok := &api_token.Token{
		ID: uuid.New(), TenantID: uuid.New(),
		Roles: []string{"platform.capability-issuer"},
	}

	p, err := principalFromAPIToken(tok, "admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.HasRole("platform.capability-issuer") {
		t.Errorf("roles = %v, want the token's", p.Roles)
	}
}

// An ordinary service token still carries none, so every role-gated policy
// keeps denying it.
func TestPrincipalFromAPIToken_RolelessStaysRoleless(t *testing.T) {
	p, err := principalFromAPIToken(&api_token.Token{ID: uuid.New(), TenantID: uuid.New()}, "data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Roles) != 0 {
		t.Errorf("roles = %v, want none", p.Roles)
	}
}

// The admin plane establishes identity only for a token carrying roles: a
// roleless one would gain nothing there and would newly reach any RPC gated on
// tenant alone. Pins both directions of that gate.
func TestWithTokenIdentity_RolesOnlyEstablishment(t *testing.T) {
	roleless := &api_token.Token{ID: uuid.New(), TenantID: uuid.New()}
	withRole := &api_token.Token{
		ID: uuid.New(), TenantID: uuid.New(),
		Roles: []string{"platform.capability-issuer"},
	}
	i := &apiTokenInterceptor{audience: "admin", establishPrincipal: true, requireRolesToEstablish: true}

	ctx, err := i.withTokenIdentity(context.Background(), roleless)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, perr := PrincipalFromContext(ctx); perr == nil {
		t.Error("a roleless token must not establish a principal on this plane")
	}

	ctx, err = i.withTokenIdentity(context.Background(), withRole)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p, perr := PrincipalFromContext(ctx)
	if perr != nil {
		t.Fatalf("a role-bearing token must establish one: %v", perr)
	}
	if !p.HasRole("platform.capability-issuer") {
		t.Errorf("roles = %v", p.Roles)
	}
}

// The data plane establishes for every valid token, roles or not — that is how a
// service authenticates ordinary object operations.
func TestWithTokenIdentity_DataPlaneEstablishesWithoutRoles(t *testing.T) {
	i := &apiTokenInterceptor{audience: "data", establishPrincipal: true}

	ctx, err := i.withTokenIdentity(context.Background(),
		&api_token.Token{ID: uuid.New(), TenantID: uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, perr := PrincipalFromContext(ctx); perr != nil {
		t.Error("the data plane must still authenticate a roleless service token")
	}
}

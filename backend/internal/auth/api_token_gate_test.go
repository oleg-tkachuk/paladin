package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The API-token gate end to end: verify, rate limit, identity.
//
// These began as streaming-path tests, written because the streaming wrapper
// was a near-duplicate of the unary one and held by nothing. One interceptor
// function now serves both call shapes, so there is no second copy to keep in
// step and the tests that only proved the copies agreed are gone. What
// remains checks the gate's behaviour, driven through a real contract RPC.
//
// These tests run the REAL *api_token.Verifier over the in-memory
// smokeStore rather than a stubbed verifier. Verifier has one
// implementation and no seam of its own; Store is the seam the design
// declares ("tests substitute an in-memory stub"). Going through the
// real verifier is also what makes the audience and expiry cases below
// mean anything — against a fake they would assert only that the
// interceptor forwards whatever error it is handed.

const tokenHMACKey = "api-token-test-hmac-key-0123456789"

// Token-fixture defaults: what a test gets when it does not care.
const (
	tokenFixtureName = "gate"
	tokenFixtureTTL  = time.Hour
)

// unknownPAT is well-formed but was never issued.
const unknownPAT = api_token.TokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// malformedPAT carries the prefix but is too short to be a token.
const malformedPAT = api_token.TokenPrefix + "short"

// tokenFixture wires a real issuer + verifier over one in-memory store,
// so a token minted here is a token the verifier genuinely accepts.
type tokenFixture struct {
	store    *smokeStore
	hasher   *api_token.Hasher
	issuer   *api_token.Issuer
	verifier *api_token.Verifier
}

func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	store := newSmokeStore()
	hasher, err := api_token.NewHasher([]byte(tokenHMACKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	issuer, err := api_token.NewIssuer(api_token.IssuerConfig{Store: store, Hasher: hasher, MaxTTL: tokenFixtureTTL})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := api_token.NewVerifier(api_token.VerifierConfig{Store: store, Hasher: hasher})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	return &tokenFixture{store: store, hasher: hasher, issuer: issuer, verifier: verifier}
}

func (f *tokenFixture) issue(t *testing.T, req api_token.IssueRequest) *api_token.Token {
	t.Helper()
	if req.Name == "" {
		req.Name = tokenFixtureName
	}
	if len(req.Audience) == 0 {
		req.Audience = []string{planeData}
	}
	if req.TenantID == uuid.Nil {
		req.TenantID = uuid.New()
	}
	if req.TTL == 0 {
		req.TTL = tokenFixtureTTL
	}
	tok, err := f.issuer.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return tok
}

// No token is not a failure: the call defers to whatever authenticates it
// downstream.
func TestAPITokenGate_NoToken(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	i := &apiTokenInterceptor{verifier: f.verifier, audience: planeData}

	c := callProbe(context.Background(), []connect.ServerInterceptor{i.intercept})
	if c.err != nil {
		t.Fatalf("an unauthenticated call must pass through, got %v", c.err)
	}
	if _, ok := APITokenFromContext(c.handlerCtx); ok {
		t.Error("no token was supplied, so none may be stamped on the context")
	}
}

func TestAPITokenGate_VerifiedTokenReachesHandler(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	tenant := uuid.New()
	tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
	i := &apiTokenInterceptor{verifier: f.verifier, audience: planeData}

	for name, pair := range map[string][2]string{
		"Authorization bearer": {paladin.HeaderAuthorization, bearerPrefix + tok.Plaintext},
		"X-Paladin-API-Token":  {HeaderAPIToken, tok.Plaintext},
	} {
		t.Run(name, func(t *testing.T) {
			c := callProbe(context.Background(), []connect.ServerInterceptor{i.intercept}, pair[0], pair[1])
			if c.err != nil {
				t.Fatalf("verify should succeed, got %v", c.err)
			}
			got, ok := APITokenFromContext(c.handlerCtx)
			if !ok {
				t.Fatal("verified token was not stamped on the context")
			}
			if got.TenantID != tenant {
				t.Errorf("tenant: got %s, want %s", got.TenantID, tenant)
			}
		})
	}
}

// The verifier's own gates have to run, not just the header parse. Each case
// below is refused by *api_token.Verifier, and reaches the caller only because
// the interceptor consults it.
func TestAPITokenGate_VerifierGates(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	good := f.issue(t, api_token.IssueRequest{Audience: []string{planeData}})

	// A verifier whose clock is past the token's expiry — same store,
	// so the row is identical and only the time gate differs.
	const pastExpiry = 2 * tokenFixtureTTL
	future, err := api_token.NewVerifier(api_token.VerifierConfig{
		Store:  f.store,
		Hasher: f.hasher,
		Now:    func() time.Time { return time.Now().Add(pastExpiry) },
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	cases := map[string]struct {
		verifier *api_token.Verifier
		audience string
		token    string
		want     connect.Code
	}{
		"unknown token":     {f.verifier, planeData, unknownPAT, connect.CodeUnauthenticated},
		"malformed token":   {f.verifier, planeData, malformedPAT, connect.CodeUnauthenticated},
		"expired token":     {future, planeData, good.Plaintext, connect.CodeUnauthenticated},
		"audience mismatch": {f.verifier, planeAdmin, good.Plaintext, connect.CodePermissionDenied},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			i := &apiTokenInterceptor{verifier: tc.verifier, audience: tc.audience}
			c := callProbe(context.Background(), []connect.ServerInterceptor{i.intercept},
				paladin.HeaderAuthorization, bearerPrefix+tc.token)
			if c.err == nil {
				t.Fatal("expected the call to be refused, got nil")
			}
			if c.reached() {
				t.Error("the handler ran for a token the verifier refused")
			}
			var ce *connect.Error
			if !errors.As(c.err, &ce) {
				t.Fatalf("expected *connect.Error, got %T: %v", c.err, c.err)
			}
			if ce.Code() != tc.want {
				t.Errorf("code: got %v, want %v", ce.Code(), tc.want)
			}
		})
	}
}

func TestAPITokenGate_RateLimitDenial(t *testing.T) {
	t.Parallel()
	const (
		rpm        = 60
		overLimit  = 65
		retryAfter = 30 * time.Second
	)
	f := newTokenFixture(t)
	tok := f.issue(t, api_token.IssueRequest{RateLimitRPM: rpm})
	rl := &recordingLimiter{resp: ratelimit.Decision{
		Allowed:       false,
		WeightedCount: overLimit,
		RetryAfter:    retryAfter,
	}}
	i := &apiTokenInterceptor{verifier: f.verifier, limiter: rl, audience: planeData}

	c := callProbe(context.Background(), []connect.ServerInterceptor{i.intercept},
		paladin.HeaderAuthorization, bearerPrefix+tok.Plaintext)
	if c.err == nil {
		t.Fatal("expected the rate-limit denial to refuse the call")
	}
	if c.reached() {
		t.Error("the handler ran despite the rate-limit denial")
	}
	if rl.calls.Load() != 1 {
		t.Errorf("limiter calls: got %d, want 1", rl.calls.Load())
	}
	if code := connect.CodeOf(c.err); code != connect.CodeResourceExhausted {
		t.Errorf("code: got %v, want ResourceExhausted", code)
	}
	want := strconv.Itoa(int(retryAfter.Seconds()))
	if got := c.responseHeader.Get(paladin.HeaderRetryAfter); got != want {
		t.Errorf("%s: got %q, want %q", paladin.HeaderRetryAfter, got, want)
	}
}

// Identity establishment is the half of this interceptor that differs per
// constructor.
func TestAPITokenGate_Identity(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	tenant := uuid.New()
	plain := f.issue(t, api_token.IssueRequest{TenantID: tenant})
	withRoles := f.issue(t, api_token.IssueRequest{TenantID: tenant, Roles: []string{"platform.admin"}})

	cases := map[string]struct {
		interceptor   *apiTokenInterceptor
		token         string
		wantPrincipal bool
	}{
		"additive interceptor establishes nothing": {
			&apiTokenInterceptor{verifier: f.verifier, audience: planeData}, plain.Plaintext, false,
		},
		"auth interceptor establishes the principal": {
			&apiTokenInterceptor{verifier: f.verifier, audience: planeData, establishPrincipal: true},
			plain.Plaintext, true,
		},
		"role-gated interceptor skips a roleless token": {
			&apiTokenInterceptor{verifier: f.verifier, audience: planeData, establishPrincipal: true, requireRolesToEstablish: true},
			plain.Plaintext, false,
		},
		"role-gated interceptor establishes for a token with roles": {
			&apiTokenInterceptor{verifier: f.verifier, audience: planeData, establishPrincipal: true, requireRolesToEstablish: true},
			withRoles.Plaintext, true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := callProbe(context.Background(), []connect.ServerInterceptor{tc.interceptor.intercept},
				paladin.HeaderAuthorization, bearerPrefix+tc.token)
			if c.err != nil {
				t.Fatalf("call should be admitted, got %v", c.err)
			}
			p, err := PrincipalFromContext(c.handlerCtx)
			if !tc.wantPrincipal {
				if err == nil {
					t.Fatalf("no principal expected, got %+v", p)
				}
				// The token itself is stamped either way — establishment
				// governs the principal, not the token.
				if _, ok := APITokenFromContext(c.handlerCtx); !ok {
					t.Error("verified token must be stamped even when no principal is established")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected a principal, got %v", err)
			}
			if p.Kind != PrincipalKindApiKey {
				t.Errorf("kind: got %v, want %v", p.Kind, PrincipalKindApiKey)
			}
			if p.TenantID != tenant {
				t.Errorf("tenant: got %s, want %s", p.TenantID, tenant)
			}
			if p.Audience != AudienceData {
				t.Errorf("audience: got %q, want %q", p.Audience, AudienceData)
			}
		})
	}
}

// An existing principal wins: the gate must not overwrite an identity a JWT
// already established.
func TestAPITokenGate_ExistingPrincipalWins(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	tok := f.issue(t, api_token.IssueRequest{})
	i := &apiTokenInterceptor{verifier: f.verifier, audience: planeData, establishPrincipal: true}

	jwtTenant := uuid.New()
	ctx := WithPrincipal(context.Background(), &Principal{TenantID: jwtTenant, Audience: AudienceData})

	c := callProbe(ctx, []connect.ServerInterceptor{i.intercept},
		paladin.HeaderAuthorization, bearerPrefix+tok.Plaintext)
	if c.err != nil {
		t.Fatalf("call should be admitted, got %v", c.err)
	}
	p, err := PrincipalFromContext(c.handlerCtx)
	if err != nil {
		t.Fatalf("principal: %v", err)
	}
	if p.TenantID != jwtTenant {
		t.Errorf("the API token overwrote an established principal: got %s, want %s", p.TenantID, jwtTenant)
	}
}

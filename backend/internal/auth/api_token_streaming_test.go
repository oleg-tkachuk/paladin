package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
)

// The streaming handler path is a near-duplicate of WrapUnary and was
// held by nothing: every API-token test in this package drove WrapUnary,
// so a gate could have been dropped from the streaming copy and every
// test would still pass. That is the shape of bug this package keeps
// finding — one policy, two copies, one of them covered.
//
// These tests run the REAL *api_token.Verifier over the in-memory
// smokeStore rather than a stubbed verifier. Verifier has one
// implementation and no seam of its own; Store is the seam the design
// declares ("tests substitute an in-memory stub"). Going through the
// real verifier is also what makes the audience and expiry cases below
// mean anything — against a fake they would assert only that the
// interceptor forwards whatever error it is handed.

// fakeStreamConn is the minimum connect.StreamingHandlerConn an
// interceptor touches: it reads the request header and passes the conn
// on. Receive/Send are never reached — the interceptor either
// short-circuits or delegates.
type fakeStreamConn struct {
	header    http.Header
	respHdr   http.Header
	respTrail http.Header
}

func newStreamConn() *fakeStreamConn {
	return &fakeStreamConn{
		header:    http.Header{},
		respHdr:   http.Header{},
		respTrail: http.Header{},
	}
}

func (c *fakeStreamConn) Spec() connect.Spec {
	return connect.Spec{StreamType: connect.StreamTypeServer, Procedure: "/auth.stream.Svc/Watch"}
}
func (c *fakeStreamConn) Peer() connect.Peer           { return connect.Peer{} }
func (c *fakeStreamConn) Receive(any) error            { return nil }
func (c *fakeStreamConn) RequestHeader() http.Header   { return c.header }
func (c *fakeStreamConn) Send(any) error               { return nil }
func (c *fakeStreamConn) ResponseHeader() http.Header  { return c.respHdr }
func (c *fakeStreamConn) ResponseTrailer() http.Header { return c.respTrail }

var _ connect.StreamingHandlerConn = (*fakeStreamConn)(nil)

const streamHMACKey = "streaming-test-hmac-key-0123456789"

// streamFixture wires a real issuer + verifier over one in-memory store,
// so a token minted here is a token the verifier genuinely accepts.
type streamFixture struct {
	store    *smokeStore
	hasher   *api_token.Hasher
	issuer   *api_token.Issuer
	verifier *api_token.Verifier
}

func newStreamFixture(t *testing.T) *streamFixture {
	t.Helper()
	store := newSmokeStore()
	hasher, err := api_token.NewHasher([]byte(streamHMACKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	issuer, err := api_token.NewIssuer(api_token.IssuerConfig{Store: store, Hasher: hasher, MaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := api_token.NewVerifier(api_token.VerifierConfig{Store: store, Hasher: hasher})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	return &streamFixture{store: store, hasher: hasher, issuer: issuer, verifier: verifier}
}

func (f *streamFixture) issue(t *testing.T, req api_token.IssueRequest) *api_token.Token {
	t.Helper()
	if req.Name == "" {
		req.Name = "stream"
	}
	if len(req.Audience) == 0 {
		req.Audience = []string{"data"}
	}
	if req.TenantID == uuid.Nil {
		req.TenantID = uuid.New()
	}
	if req.TTL == 0 {
		req.TTL = time.Hour
	}
	tok, err := f.issuer.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return tok
}

// streamNext returns a StreamingHandlerFunc that records whether it ran
// and what context it was handed.
func streamNext(called *bool, seen *context.Context) connect.StreamingHandlerFunc {
	return func(ctx context.Context, _ connect.StreamingHandlerConn) error {
		*called = true
		*seen = ctx
		return nil
	}
}

func TestWrapStreamingHandler_NoToken(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	i := &apiTokenInterceptor{verifier: f.verifier, audience: "data"}

	var called bool
	var seen context.Context
	if err := i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), newStreamConn()); err != nil {
		t.Fatalf("an unauthenticated stream must pass through, got %v", err)
	}
	if !called {
		t.Fatal("next was not called")
	}
	if _, ok := APITokenFromContext(seen); ok {
		t.Error("no token was supplied, so none may be stamped on the context")
	}
}

func TestWrapStreamingHandler_VerifiedTokenReachesHandler(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	tenant := uuid.New()
	tok := f.issue(t, api_token.IssueRequest{TenantID: tenant})
	i := &apiTokenInterceptor{verifier: f.verifier, audience: "data"}

	for name, set := range map[string]func(http.Header){
		"Authorization bearer": func(h http.Header) { h.Set("Authorization", "Bearer "+tok.Plaintext) },
		"X-Paladin-API-Token":  func(h http.Header) { h.Set(HeaderAPIToken, tok.Plaintext) },
	} {
		t.Run(name, func(t *testing.T) {
			conn := newStreamConn()
			set(conn.header)

			var called bool
			var seen context.Context
			if err := i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn); err != nil {
				t.Fatalf("verify should succeed, got %v", err)
			}
			if !called {
				t.Fatal("next was not called")
			}
			got, ok := APITokenFromContext(seen)
			if !ok {
				t.Fatal("verified token was not stamped on the context")
			}
			if got.TenantID != tenant {
				t.Errorf("tenant: got %s, want %s", got.TenantID, tenant)
			}
		})
	}
}

// The verifier's own gates have to run on this path, not just the
// header parse. Each case below is refused by *api_token.Verifier, and
// reaches the caller only because the streaming wrapper consults it.
func TestWrapStreamingHandler_VerifierGates(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	good := f.issue(t, api_token.IssueRequest{Audience: []string{"data"}})

	// A verifier whose clock is past the token's expiry — same store,
	// so the row is identical and only the time gate differs.
	future, err := api_token.NewVerifier(api_token.VerifierConfig{
		Store:  f.store,
		Hasher: f.hasher,
		Now:    func() time.Time { return time.Now().Add(2 * time.Hour) },
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
		"unknown token":     {f.verifier, "data", api_token.TokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", connect.CodeUnauthenticated},
		"malformed token":   {f.verifier, "data", api_token.TokenPrefix + "short", connect.CodeUnauthenticated},
		"expired token":     {future, "data", good.Plaintext, connect.CodeUnauthenticated},
		"audience mismatch": {f.verifier, "admin", good.Plaintext, connect.CodePermissionDenied},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			i := &apiTokenInterceptor{verifier: tc.verifier, audience: tc.audience}
			conn := newStreamConn()
			conn.header.Set("Authorization", "Bearer "+tc.token)

			var called bool
			var seen context.Context
			err := i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn)
			if err == nil {
				t.Fatal("expected the stream to be refused, got nil")
			}
			if called {
				t.Error("next ran for a token the verifier refused")
			}
			var ce *connect.Error
			if !errors.As(err, &ce) {
				t.Fatalf("expected *connect.Error, got %T: %v", err, err)
			}
			if ce.Code() != tc.want {
				t.Errorf("code: got %v, want %v", ce.Code(), tc.want)
			}
		})
	}
}

func TestWrapStreamingHandler_RateLimitDenial(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	tok := f.issue(t, api_token.IssueRequest{RateLimitRPM: 60})
	rl := &recordingLimiter{resp: ratelimit.Decision{
		Allowed:       false,
		WeightedCount: 65,
		RetryAfter:    30 * time.Second,
	}}
	i := &apiTokenInterceptor{verifier: f.verifier, limiter: rl, audience: "data"}

	conn := newStreamConn()
	conn.header.Set("Authorization", "Bearer "+tok.Plaintext)

	var called bool
	var seen context.Context
	err := i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn)
	if err == nil {
		t.Fatal("expected the rate-limit denial to refuse the stream")
	}
	if called {
		t.Error("next ran despite the rate-limit denial")
	}
	if rl.calls.Load() != 1 {
		t.Errorf("limiter calls: got %d, want 1", rl.calls.Load())
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeResourceExhausted {
		t.Errorf("code: got %v, want ResourceExhausted", ce.Code())
	}
	if got := ce.Meta().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After: got %q, want %q", got, "30")
	}
}

// Identity establishment is the half of this interceptor that differs
// per constructor, and each variant has to behave the same on a stream
// as it does on a unary call.
func TestWrapStreamingHandler_Identity(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	tenant := uuid.New()
	plain := f.issue(t, api_token.IssueRequest{TenantID: tenant})
	withRoles := f.issue(t, api_token.IssueRequest{TenantID: tenant, Roles: []string{"platform.admin"}})

	cases := map[string]struct {
		interceptor   *apiTokenInterceptor
		token         string
		wantPrincipal bool
	}{
		"additive interceptor establishes nothing": {
			&apiTokenInterceptor{verifier: f.verifier, audience: "data"}, plain.Plaintext, false,
		},
		"auth interceptor establishes the principal": {
			&apiTokenInterceptor{verifier: f.verifier, audience: "data", establishPrincipal: true},
			plain.Plaintext, true,
		},
		"role-gated interceptor skips a roleless token": {
			&apiTokenInterceptor{verifier: f.verifier, audience: "data", establishPrincipal: true, requireRolesToEstablish: true},
			plain.Plaintext, false,
		},
		"role-gated interceptor establishes for a token with roles": {
			&apiTokenInterceptor{verifier: f.verifier, audience: "data", establishPrincipal: true, requireRolesToEstablish: true},
			withRoles.Plaintext, true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			conn := newStreamConn()
			conn.header.Set("Authorization", "Bearer "+tc.token)

			var called bool
			var seen context.Context
			if err := tc.interceptor.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn); err != nil {
				t.Fatalf("stream should be admitted, got %v", err)
			}
			if !called {
				t.Fatal("next was not called")
			}
			p, err := PrincipalFromContext(seen)
			if !tc.wantPrincipal {
				if err == nil {
					t.Fatalf("no principal expected, got %+v", p)
				}
				// The token itself is stamped either way — establishment
				// governs the principal, not the token.
				if _, ok := APITokenFromContext(seen); !ok {
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

// An existing principal wins: the streaming path must not overwrite an
// identity a JWT already established.
func TestWrapStreamingHandler_ExistingPrincipalWins(t *testing.T) {
	t.Parallel()
	f := newStreamFixture(t)
	tok := f.issue(t, api_token.IssueRequest{})
	i := &apiTokenInterceptor{verifier: f.verifier, audience: "data", establishPrincipal: true}

	jwtTenant := uuid.New()
	ctx := WithPrincipal(context.Background(), &Principal{TenantID: jwtTenant, Audience: AudienceData})

	conn := newStreamConn()
	conn.header.Set("Authorization", "Bearer "+tok.Plaintext)

	var called bool
	var seen context.Context
	if err := i.WrapStreamingHandler(streamNext(&called, &seen))(ctx, conn); err != nil {
		t.Fatalf("stream should be admitted, got %v", err)
	}
	p, err := PrincipalFromContext(seen)
	if err != nil {
		t.Fatalf("principal: %v", err)
	}
	if p.TenantID != jwtTenant {
		t.Errorf("the API token overwrote an established principal: got %s, want %s", p.TenantID, jwtTenant)
	}
}

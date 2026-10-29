package issuer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

func newTestIssuer(t *testing.T) *Issuer {
	t.Helper()
	iss, err := New(Config{
		Issuer:            "paladin-test",
		SigningKey:        []byte("test-signing-key-32-bytes-min--padding"),
		AccessTokenTTL:    15 * time.Minute,
		RefreshTokenTTL:   7 * 24 * time.Hour,
		ScopedTokenMaxTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func TestMintAndVerifyAccess(t *testing.T) {
	iss := newTestIssuer(t)
	tenantID := uuid.NewV7()
	subject := uuid.NewV7().String()

	tok, exp, err := iss.MintAccess(AccessClaims{
		Subject:  subject,
		TenantID: tenantID,
		Audience: auth.AudienceData,
		Roles:    []string{"tenant.user"},
		Scopes:   []auth.Scope{{Type: auth.ScopeBucket, Value: "paladin-archive"}},
		Kind:     auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("MintAccess: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry not in future: %v", exp)
	}

	v := &auth.JWTVerifier{
		Key:              []byte("test-signing-key-32-bytes-min--padding"),
		ExpectedIssuer:   "paladin-test",
		ExpectedAudience: auth.AudienceData,
	}
	p, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.Subject != subject {
		t.Errorf("Subject: got %q want %q", p.Subject, subject)
	}
	if p.TenantID != tenantID {
		t.Errorf("TenantID: got %v want %v", p.TenantID, tenantID)
	}
	if p.Audience != auth.AudienceData {
		t.Errorf("Audience: got %q want %q", p.Audience, auth.AudienceData)
	}
	if !p.HasRole("tenant.user") {
		t.Error("missing role tenant.user")
	}
	if len(p.Scopes) != 1 || p.Scopes[0].Type != auth.ScopeBucket || p.Scopes[0].Value != "paladin-archive" {
		t.Errorf("Scopes: got %+v", p.Scopes)
	}
	if p.Kind != auth.PrincipalKindUser {
		t.Errorf("Kind: got %v want User", p.Kind)
	}
}

func TestMintAccessWrongAudience(t *testing.T) {
	iss := newTestIssuer(t)
	tok, _, err := iss.MintAccess(AccessClaims{
		Subject:  "u",
		Audience: auth.AudienceAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	v := &auth.JWTVerifier{
		Key:              []byte("test-signing-key-32-bytes-min--padding"),
		ExpectedAudience: auth.AudienceData, // mismatch
	}
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected audience mismatch error")
	}
}

func TestMintRefreshDecode(t *testing.T) {
	iss := newTestIssuer(t)
	userID := uuid.NewV7()
	tokenID := uuid.NewV7()
	tenantID := uuid.NewV7()

	tok, _, err := iss.MintRefresh(RefreshClaims{
		Subject:  userID.String(),
		TenantID: tenantID,
		UserID:   userID,
		TokenID:  tokenID,
	})
	if err != nil {
		t.Fatal(err)
	}
	dec := &auth.RefreshDecoder{
		Verifier: &auth.JWTVerifier{
			Key:              []byte("test-signing-key-32-bytes-min--padding"),
			ExpectedIssuer:   "paladin-test",
			ExpectedAudience: auth.AudienceIAM,
		},
	}
	jti, gotUserID, gotTenantID, err := dec.DecodeRefresh(tok)
	if err != nil {
		t.Fatalf("DecodeRefresh: %v", err)
	}
	if jti != tokenID {
		t.Errorf("jti: got %v want %v", jti, tokenID)
	}
	if gotUserID != userID {
		t.Errorf("user_id: got %v want %v", gotUserID, userID)
	}
	if gotTenantID != tenantID {
		t.Errorf("tenant_id: got %v want %v", gotTenantID, tenantID)
	}
}

func TestRefreshDecoderRejectsAccessToken(t *testing.T) {
	iss := newTestIssuer(t)
	tok, _, err := iss.MintAccess(AccessClaims{
		Subject:  "u",
		Audience: auth.AudienceData, // not IAM
	})
	if err != nil {
		t.Fatal(err)
	}
	dec := &auth.RefreshDecoder{
		Verifier: &auth.JWTVerifier{
			Key:              []byte("test-signing-key-32-bytes-min--padding"),
			ExpectedIssuer:   "paladin-test",
			ExpectedAudience: auth.AudienceIAM,
		},
	}
	if _, _, _, err := dec.DecodeRefresh(tok); err == nil {
		t.Fatal("expected audience mismatch")
	}
}

// Re-issuing a stored refresh token must sign the row's own expiry, not a new
// lifetime: a recovered lost rotation would otherwise outlive the token it
// stands for.
func TestMintRefreshSignsAGivenExpiry(t *testing.T) {
	iss := newTestIssuer(t)
	userID := uuid.NewV7()
	want := time.Now().Add(37 * time.Minute).Truncate(time.Second)

	_, exp, err := iss.MintRefresh(RefreshClaims{
		Subject: userID.String(), UserID: userID,
		TokenID: uuid.NewV7(), ExpiresAt: want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(want) {
		t.Errorf("expiry = %v, want %v", exp, want)
	}
}

// An OAuth client's grant survives the round trip as the RFC 9068 claims, and
// a token with no client carries none (ADR-0035).
func TestMintAccessCarriesTheOAuthGrant(t *testing.T) {
	iss := newTestIssuer(t)
	v := &auth.JWTVerifier{
		Key:              []byte("test-signing-key-32-bytes-min--padding"),
		ExpectedIssuer:   "paladin-test",
		ExpectedAudience: auth.AudienceData,
	}
	for _, tc := range []struct {
		name       string
		clientID   string
		scopes     []string
		wantScopes []string
	}{
		{"client with two scopes", "desktop-agent", []string{auth.OAuthScopeRead, auth.OAuthScopeWrite}, []string{auth.OAuthScopeRead, auth.OAuthScopeWrite}},
		{"client with no scope", "desktop-agent", nil, nil},
		{"no client", "", []string{auth.OAuthScopeWrite}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tok, _, err := iss.MintAccess(AccessClaims{
				Subject: "u", TenantID: uuid.New(), Audience: auth.AudienceData,
				OAuthClientID: tc.clientID, OAuthScopes: tc.scopes,
			})
			if err != nil {
				t.Fatalf("MintAccess: %v", err)
			}
			p, err := v.Verify(context.Background(), tok)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if p.OAuthClientID != tc.clientID {
				t.Errorf("OAuthClientID = %q, want %q", p.OAuthClientID, tc.clientID)
			}
			if strings.Join(p.OAuthScopes, ",") != strings.Join(tc.wantScopes, ",") {
				t.Errorf("OAuthScopes = %v, want %v", p.OAuthScopes, tc.wantScopes)
			}
		})
	}
}

// A minted token says what it is: a JWT, signed by HS256 — what any client
// reading the header expects, and what the verifier's key requires.
func TestMintAccessHeader(t *testing.T) {
	iss := newTestIssuer(t)
	tok, _, err := iss.MintAccess(AccessClaims{
		Subject: uuid.NewV7().String(), TenantID: uuid.NewV7(), Audience: auth.AudienceData,
		Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("MintAccess: %v", err)
	}
	msg, err := jws.ParseString(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	hdr := msg.Signatures()[0].ProtectedHeaders()
	alg, _ := hdr.Algorithm()
	typ, _ := hdr.Type()
	if alg != jwa.HS256() || typ != tokenType {
		t.Errorf("header alg %s typ %q, want HS256 and %q", alg, typ, tokenType)
	}
}

var issuerNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// issuerWith is an issuer whose clock stands at issuerNow, configured by edit.
func issuerWith(t *testing.T, edit func(*Config)) *Issuer {
	t.Helper()
	cfg := Config{
		Issuer:     "paladin-test",
		SigningKey: []byte(strings.Repeat("k", MinSigningKeyBytes)),
		Now:        func() time.Time { return issuerNow },
	}
	if edit != nil {
		edit(&cfg)
	}
	iss, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return iss
}

// accessTTL is how long a token minted with ttl lives.
func accessTTL(t *testing.T, iss *Issuer, ttl time.Duration) time.Duration {
	t.Helper()
	_, exp, err := iss.MintAccess(AccessClaims{Subject: "s", Audience: auth.AudienceData, TTL: ttl, Kind: auth.PrincipalKindUser})
	if err != nil {
		t.Fatalf("MintAccess: %v", err)
	}
	return exp.Sub(issuerNow)
}

// A signing key shorter than HS256's output is refused; one of that length is
// not.
func TestNewSigningKeyLength(t *testing.T) {
	short := strings.Repeat("k", MinSigningKeyBytes-1)
	if _, err := New(Config{Issuer: "i", SigningKey: []byte(short)}); err == nil {
		t.Errorf("a %d-byte key was accepted", len(short))
	}
	issuerWith(t, nil)
}

// A lifetime the config leaves at zero or below is the default; one it sets,
// however short, is kept; and no access token outlives the scoped maximum.
func TestIssuerLifetimes(t *testing.T) {
	const short = time.Nanosecond
	for _, tc := range []struct {
		name string
		edit func(*Config)
		ttl  time.Duration
		want time.Duration
	}{
		{"the default access lifetime", nil, 0, DefaultAccessTokenTTL},
		{"a negative access lifetime", func(c *Config) { c.AccessTokenTTL = -time.Second }, 0, DefaultAccessTokenTTL},
		{"a set access lifetime", func(c *Config) { c.AccessTokenTTL = short }, 0, short},
		{"a lifetime the caller asks for", nil, time.Minute, time.Minute},
		{"the shortest lifetime a caller asks for", nil, short, short},
		{"past the default maximum", nil, DefaultScopedTokenMaxTTL + time.Hour, DefaultScopedTokenMaxTTL},
		{"a negative maximum is the default", func(c *Config) { c.ScopedTokenMaxTTL = -time.Second }, DefaultScopedTokenMaxTTL + time.Hour, DefaultScopedTokenMaxTTL},
		{"a set maximum", func(c *Config) { c.ScopedTokenMaxTTL = short }, time.Minute, short},
	} {
		if got := accessTTL(t, issuerWith(t, tc.edit), tc.ttl); got != tc.want {
			t.Errorf("%s: lifetime %s, want %s", tc.name, got, tc.want)
		}
	}

	for _, tc := range []struct {
		name string
		edit func(*Config)
		want time.Duration
	}{
		{"the default refresh lifetime", nil, DefaultRefreshTokenTTL},
		{"a negative refresh lifetime", func(c *Config) { c.RefreshTokenTTL = -time.Second }, DefaultRefreshTokenTTL},
		{"a set refresh lifetime", func(c *Config) { c.RefreshTokenTTL = short }, short},
	} {
		_, exp, err := issuerWith(t, tc.edit).MintRefresh(RefreshClaims{TokenID: uuid.NewV7(), UserID: uuid.NewV7()})
		if err != nil {
			t.Fatalf("%s: MintRefresh: %v", tc.name, err)
		}
		if got := exp.Sub(issuerNow); got != tc.want {
			t.Errorf("%s: lifetime %s, want %s", tc.name, got, tc.want)
		}
	}
}

// A claim with nothing to say is left out: no tenant for a principal of none,
// no labels when there are none.
func TestMintAccessLeavesOutEmptyClaims(t *testing.T) {
	iss := issuerWith(t, nil)
	claimsOf := func(c AccessClaims) map[string]any {
		t.Helper()
		tok, _, err := iss.MintAccess(c)
		if err != nil {
			t.Fatalf("MintAccess: %v", err)
		}
		msg, err := jws.ParseString(tok)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(msg.Payload(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	bare := claimsOf(AccessClaims{Subject: "s", Audience: auth.AudienceData, Kind: auth.PrincipalKindUser})
	for _, claim := range []string{"tenant", "tenant_slug", "labels", auth.ClaimSessionID} {
		if _, ok := bare[claim]; ok {
			t.Errorf("claim %q set with nothing to say", claim)
		}
	}
	full := claimsOf(AccessClaims{Subject: "s", Audience: auth.AudienceData, Kind: auth.PrincipalKindUser,
		TenantID: uuid.NewV7(), TenantSlug: "acme", Labels: map[string]string{"team": "a"}, SessionID: "session"})
	for _, claim := range []string{"tenant", "tenant_slug", "labels", auth.ClaimSessionID} {
		if _, ok := full[claim]; !ok {
			t.Errorf("claim %q missing", claim)
		}
	}
}

// A token minted in a session names it as `sid`, which the verifier reads
// back; one minted in none carries no `sid`.
func TestMintAccessNamesItsSession(t *testing.T) {
	iss := newTestIssuer(t)
	v := &auth.JWTVerifier{
		Key:              []byte("test-signing-key-32-bytes-min--padding"),
		ExpectedIssuer:   "paladin-test",
		ExpectedAudience: auth.AudienceData,
	}
	for _, session := range []string{uuid.NewV7().String(), ""} {
		tok, _, err := iss.MintAccess(AccessClaims{Subject: "u", TenantID: uuid.New(), Audience: auth.AudienceData, SessionID: session})
		if err != nil {
			t.Fatalf("MintAccess: %v", err)
		}
		p, err := v.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if p.SessionID != session {
			t.Errorf("SessionID = %q, want %q", p.SessionID, session)
		}
	}
}

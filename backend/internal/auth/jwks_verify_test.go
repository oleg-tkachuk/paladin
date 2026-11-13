package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

// What the identity provider in these tests issues, and when.
const (
	idpIssuer   = "https://idp.example"
	idpAudience = "paladin-data"
	idpSubject  = "user-1"
	idpRSAKid   = "rsa-1"
	idpECKid    = "ec-1"
	idpHSKid    = "hs-1"
	idpLeeway   = time.Minute
	rsaTestBits = 2048
	tokenTTL    = time.Hour
)

var idpNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// idp is an identity provider publishing a JWK Set it can change.
type idp struct {
	t    *testing.T
	mu   sync.Mutex
	set  jwk.Set
	srv  *httptest.Server
	keys map[string]jwk.Key // kid → private key
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	p := &idp{t: t, set: jwk.NewSet(), keys: map[string]jwk.Key{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(p.set)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// add makes raw a signing key under kid; publish puts its public half in the set.
func (p *idp) add(kid string, raw any, publish bool) {
	p.t.Helper()
	key, err := jwk.Import[jwk.Key](raw)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		p.t.Fatal(err)
	}
	p.keys[kid] = key
	if !publish {
		return
	}
	pub, err := jwk.PublicKeyOf(key)
	if err != nil {
		p.t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.set.AddKey(pub); err != nil {
		p.t.Fatal(err)
	}
}

// sign issues a token under kid with alg, edited by edit.
func (p *idp) sign(kid string, alg jwa.SignatureAlgorithm, edit func(*jwt.Builder)) string {
	p.t.Helper()
	b := jwt.NewBuilder().
		Issuer(idpIssuer).Subject(idpSubject).Audience([]string{idpAudience}).
		Expiration(idpNow.Add(tokenTTL)).
		Claim("roles", []string{"tenant.admin"})
	if edit != nil {
		edit(b)
	}
	tok, err := b.Build()
	if err != nil {
		p.t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(alg, p.keys[kid]))
	if err != nil {
		p.t.Fatal(err)
	}
	return string(signed)
}

func (p *idp) verifier() *JWKSVerifier {
	p.t.Helper()
	v := NewJWKSVerifier(p.srv.URL)
	v.ExpectedIssuer = idpIssuer
	v.ExpectedAudience = idpAudience
	v.Leeway = idpLeeway
	v.Now = func() time.Time { return idpNow }
	if err := v.Start(p.t.Context()); err != nil {
		p.t.Fatalf("Start: %v", err)
	}
	return v
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, rsaTestBits)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A token the identity provider signed with a published key is accepted, by
// RSA and by EC, and names its principal from its claims.
func TestJWKSVerifierAcceptsAPublishedKey(t *testing.T) {
	p := newIDP(t)
	p.add(idpRSAKid, rsaKey(t), true)
	p.add(idpECKid, ecKey(t), true)
	v := p.verifier()
	tenant := uuid.New()

	for _, tc := range []struct {
		kid string
		alg jwa.SignatureAlgorithm
	}{{idpRSAKid, jwa.RS256()}, {idpECKid, jwa.ES256()}} {
		token := p.sign(tc.kid, tc.alg, func(b *jwt.Builder) { b.Claim("tenant", tenant.String()) })
		pr, err := v.Verify(t.Context(), token)
		if err != nil {
			t.Fatalf("%s: Verify: %v", tc.alg, err)
		}
		if pr.Subject != idpSubject || pr.TenantID != tenant || pr.Audience != idpAudience ||
			len(pr.Roles) != 1 || pr.Roles[0] != "tenant.admin" || !pr.ExpiresAt.Equal(idpNow.Add(tokenTTL)) {
			t.Errorf("%s: principal = %+v", tc.alg, pr)
		}
	}
}

// What the identity provider did not vouch for, or not for Paladin, now, is
// refused.
func TestJWKSVerifierRefuses(t *testing.T) {
	p := newIDP(t)
	p.add(idpRSAKid, rsaKey(t), true)
	p.add("unpublished", rsaKey(t), false)
	p.add(idpHSKid, []byte("a-shared-secret-of-thirty-two-b"), true)
	v := p.verifier()

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"another issuer", p.sign(idpRSAKid, jwa.RS256(), func(b *jwt.Builder) { b.Issuer("https://other.example") })},
		{"another audience", p.sign(idpRSAKid, jwa.RS256(), func(b *jwt.Builder) { b.Audience([]string{"paladin-admin"}) })},
		{"expired past the leeway", p.sign(idpRSAKid, jwa.RS256(), func(b *jwt.Builder) { b.Expiration(idpNow.Add(-2 * idpLeeway)) })},
		{"not yet valid past the leeway", p.sign(idpRSAKid, jwa.RS256(), func(b *jwt.Builder) { b.NotBefore(idpNow.Add(2 * idpLeeway)) })},
		{"a key the set does not publish", p.sign("unpublished", jwa.RS256(), nil)},
		{"a shared secret, even one the set publishes", p.sign(idpHSKid, jwa.HS256(), nil)},
		{"a malformed tenant", p.sign(idpRSAKid, jwa.RS256(), func(b *jwt.Builder) { b.Claim("tenant", "not-a-uuid") })},
		{"not a token", "a.b"},
	} {
		if pr, err := v.Verify(t.Context(), tc.token); err == nil {
			t.Errorf("%s: accepted as %+v", tc.name, pr)
		}
	}

	// Within the leeway, a just-expired token still passes.
	late := p.sign(idpRSAKid, jwa.RS256(), func(b *jwt.Builder) { b.Expiration(idpNow.Add(-idpLeeway / 2)) })
	if _, err := v.Verify(t.Context(), late); err != nil {
		t.Errorf("expired within the leeway: %v", err)
	}
}

// A key the identity provider rotates in is picked up on the first token
// signed with it, without waiting for the refresh interval.
func TestJWKSVerifierPicksUpARotatedKey(t *testing.T) {
	p := newIDP(t)
	p.add(idpRSAKid, rsaKey(t), true)
	v := p.verifier()
	// The refresh on a miss is debounced; past it, the new key is fetched.
	v.Now = func() time.Time { return idpNow.Add(time.Hour) }
	p.add("rsa-2", rsaKey(t), true)
	token := p.sign("rsa-2", jwa.RS256(), func(b *jwt.Builder) { b.Expiration(idpNow.Add(2 * time.Hour)) })
	if _, err := v.Verify(t.Context(), token); err != nil {
		t.Errorf("Verify with a rotated-in key: %v", err)
	}
}

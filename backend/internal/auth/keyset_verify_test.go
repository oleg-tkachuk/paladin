package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
)

func keySet(t *testing.T) (*sessionkey.Set, *ecdsa.PrivateKey) {
	t.Helper()
	key := ecKey(t)
	set, err := sessionkey.FromKeys(key)
	if err != nil {
		t.Fatal(err)
	}
	return set, key
}

// signedBySet is claims signed by set's active key under its kid, as the
// session issuer signs them.
func signedBySet(t *testing.T, set *sessionkey.Set, claims map[string]any) string {
	t.Helper()
	return signedWith(t, jwa.ES256(), set.Signer(), claims)
}

// The minting role verifies by its own key set: what the set signed passes
// with the principal its claims name; another key, a kid the set does not
// hold, and HS256 under the set's own kid — keyed by its public key's bytes
// or by a shared secret — are refused (ADR-0047).
func TestKeySetVerifierVerifiesWhatTheSetSigned(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	set, key := keySet(t)
	v := &KeySetVerifier{
		Keys: set.Public(), ExpectedIssuer: "paladin-test", ExpectedAudience: "paladin-data",
		Leeway: time.Second, Now: func() time.Time { return now },
	}
	p, err := v.Verify(context.Background(), signedBySet(t, set, baseClaims(now)))
	if err != nil || p.Subject != "subject-1" {
		t.Fatalf("a token the set signed: principal %+v, err %v", p, err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	underKid := func(alg jwa.SignatureAlgorithm, key any) string {
		hdr := jws.NewHeaders()
		if err := hdr.Set(jws.KeyIDKey, set.KeyID()); err != nil {
			t.Fatal(err)
		}
		tok := signedWith(t, alg, key, baseClaims(now))
		msg, err := jws.Parse([]byte(tok))
		if err != nil {
			t.Fatal(err)
		}
		signed, err := jws.Sign(msg.Payload(), jws.WithKey(alg, key, jws.WithProtectedHeaders(hdr)))
		if err != nil {
			t.Fatal(err)
		}
		return string(signed)
	}
	other, _ := keySet(t)
	for name, token := range map[string]string{
		"another key":                        signedBySet(t, other, baseClaims(now)),
		"no kid":                             es256(t, ecKey(t), baseClaims(now)),
		"HS256 by the public key's bytes":    underKid(jwa.HS256(), pubDER),
		"HS256 by a shared secret":           underKid(jwa.HS256(), sharedSecret),
		"ES256 by another key under its kid": underKid(jwa.ES256(), ecKey(t)),
	} {
		if p, err := v.Verify(context.Background(), token); err == nil {
			t.Errorf("%s: accepted as %+v", name, p)
		}
	}
}

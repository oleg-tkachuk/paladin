package sessionkey_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jws"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
)

func p256(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func writePEM(t *testing.T, kind string, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func privatePEM(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return writePEM(t, "PRIVATE KEY", der)
}

func publicPEM(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return writePEM(t, "PUBLIC KEY", der)
}

// What the set signs verifies by its published public key under the kid the
// signer names, by ES256 — the round trip every verifier relies on.
func TestASignatureVerifiesByThePublishedKey(t *testing.T) {
	set, err := sessionkey.Load(privatePEM(t, p256(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jws.Sign([]byte(`{"sub":"u"}`), jws.WithKey(jwa.ES256(), set.Signer()))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := jws.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	kid, _ := msg.Signatures()[0].ProtectedHeaders().KeyID()
	if kid == "" || kid != set.KeyID() {
		t.Fatalf("signed under kid %q, the set names %q", kid, set.KeyID())
	}
	if _, err := jws.Verify(signed, jws.WithKeySet(set.Public(), jws.WithInferAlgorithmFromKey(true))); err != nil {
		t.Fatalf("verify by the published set: %v", err)
	}
}

// The kid is the key's RFC 7638 thumbprint, so the same key always carries the
// same kid and a rotated one another.
func TestTheKeyIDIsTheThumbprint(t *testing.T) {
	key := p256(t)
	path := privatePEM(t, key)
	a, err := sessionkey.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sessionkey.Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sessionkey.Load(privatePEM(t, p256(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.KeyID() != b.KeyID() || a.KeyID() == other.KeyID() {
		t.Fatalf("kids %q, %q, %q: want the first two equal and the third different", a.KeyID(), b.KeyID(), other.KeyID())
	}
}

// A previous key stays published beside the active one, so a token it signed
// before a rotation still verifies; the set never signs with it.
func TestAPreviousKeyIsPublishedNotUsed(t *testing.T) {
	old := p256(t)
	before, err := sessionkey.Load(privatePEM(t, old), nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jws.Sign([]byte(`{}`), jws.WithKey(jwa.ES256(), before.Signer()))
	if err != nil {
		t.Fatal(err)
	}
	after, err := sessionkey.Load(privatePEM(t, p256(t)), []string{publicPEM(t, &old.PublicKey)})
	if err != nil {
		t.Fatal(err)
	}
	if after.Public().Len() != 2 {
		t.Fatalf("published %d keys, want the active and the previous", after.Public().Len())
	}
	if after.KeyID() == before.KeyID() {
		t.Fatal("the rotated set signs under the previous kid")
	}
	if _, err := jws.Verify(signed, jws.WithKeySet(after.Public(), jws.WithInferAlgorithmFromKey(true))); err != nil {
		t.Fatalf("a token of the previous key no longer verifies: %v", err)
	}
}

// Anything but a P-256 key is refused at load, rather than signing sessions
// with a key the verifiers would not accept.
func TestOnlyAP256KeyLoads(t *testing.T) {
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"P-384":        privatePEM(t, p384),
		"Ed25519":      privatePEM(t, ed),
		"not PEM":      writeRaw(t, "not a key"),
		"a public key": publicPEM(t, &p256(t).PublicKey),
		"no file":      filepath.Join(t.TempDir(), "absent.pem"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sessionkey.Load(path, nil); err == nil {
				t.Fatal("loaded")
			}
		})
	}
	for name, prev := range map[string]string{
		"previous P-384":   publicPEM(t, &p384.PublicKey),
		"previous garbage": writeRaw(t, "nope"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sessionkey.Load(privatePEM(t, p256(t)), []string{prev}); err == nil {
				t.Fatal("loaded")
			}
		})
	}
}

func writeRaw(t *testing.T, s string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raw")
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The JWKS document carries public keys only, each for signatures under ES256
// and a kid, with a cache lifetime verifiers can honour.
func TestTheJWKSServesPublicKeysOnly(t *testing.T) {
	set, err := sessionkey.Load(privatePEM(t, p256(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	set.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, sessionkey.WellKnownPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control %q names no max-age", cc)
	}
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("%d keys, want 1", len(doc.Keys))
	}
	k := doc.Keys[0]
	if _, private := k["d"]; private {
		t.Fatal("the JWKS publishes the private key")
	}
	if k["kid"] != set.KeyID() || k["alg"] != "ES256" || k["use"] != "sig" || k["kty"] != "EC" {
		t.Errorf("key %v: want kid %q, alg ES256, use sig, kty EC", k, set.KeyID())
	}
	parsed, err := jwk.Parse(rec.Body.Bytes())
	if err != nil || parsed.Len() != 1 {
		t.Fatalf("the document does not parse as a JWK set: %v", err)
	}
}

// Helm's genPrivateKey "ecdsa" writes a SEC1 P-256 key, which the chart
// generates the session key with; it loads as the PKCS#8 form of the same key
// does, under the same kid.
func TestASEC1KeyLoadsAsItsPKCS8Form(t *testing.T) {
	key := p256(t)
	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	fromSEC1, err := sessionkey.Load(writePEM(t, "EC PRIVATE KEY", sec1), nil)
	if err != nil {
		t.Fatal(err)
	}
	fromPKCS8, err := sessionkey.Load(privatePEM(t, key), nil)
	if err != nil {
		t.Fatal(err)
	}
	if fromSEC1.KeyID() != fromPKCS8.KeyID() {
		t.Fatalf("kids %q and %q for one key", fromSEC1.KeyID(), fromPKCS8.KeyID())
	}
}

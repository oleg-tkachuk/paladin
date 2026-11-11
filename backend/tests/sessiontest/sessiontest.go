// Package sessiontest gives tests a session key set (ADR-0047): an ephemeral
// ECDSA P-256 key, the file a config names it by, and verifiers over its
// public keys. Every test that mints or verifies a session signs ES256 with
// one, as the server does; none signs with a shared secret.
package sessiontest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
)

// keyFileName is the PEM KeyFile writes in the test's temporary directory.
const keyFileName = "session-key.pem"

// Keys is a fresh session key set.
func Keys(tb testing.TB) *sessionkey.Set {
	tb.Helper()
	_, set := generate(tb)
	return set
}

// MustKeys is a fresh session key set for a package-level fixture, where no
// testing.TB is at hand; it panics on the error Keys would report.
func MustKeys() *sessionkey.Set {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	set, err := sessionkey.FromKeys(key)
	if err != nil {
		panic(err)
	}
	return set
}

// KeyFile writes a fresh session key as PEM PKCS#8, as auth.session_key_path
// names it, and returns its path and the set it loads as.
func KeyFile(tb testing.TB) (string, *sessionkey.Set) {
	tb.Helper()
	key, _ := generate(tb)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(tb.TempDir(), keyFileName)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		tb.Fatal(err)
	}
	set, err := sessionkey.Load(path, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return path, set
}

// Verifier verifies sessions keys signed, for issuer and audience; an empty
// audience checks none.
func Verifier(keys *sessionkey.Set, issuer, audience string) *auth.KeySetVerifier {
	return &auth.KeySetVerifier{Keys: keys.Public(), ExpectedIssuer: issuer, ExpectedAudience: audience}
}

func generate(tb testing.TB) (*ecdsa.PrivateKey, *sessionkey.Set) {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatal(err)
	}
	set, err := sessionkey.FromKeys(key)
	if err != nil {
		tb.Fatal(err)
	}
	return key, set
}

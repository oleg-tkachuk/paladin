package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// pemTypePrivateKey is the PEM block type of a PKCS#8 private key, which
// limes.LoadKey reads.
const pemTypePrivateKey = "PRIVATE KEY"

// signingKeyFileMode keeps the written test key private to its owner.
const signingKeyFileMode = 0o600

// writeSigningKey writes a fresh Ed25519 key as PKCS#8 PEM and returns its
// path and public half.
func writeSigningKey(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signing.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: pemTypePrivateKey, Bytes: der}), signingKeyFileMode); err != nil {
		t.Fatal(err)
	}
	return path, pub
}

// With no signing_key_path the key is generated for this process alone and
// reported as such, under the kid derived from it.
func TestCapabilityKeyGeneratedWithoutPath(t *testing.T) {
	key, generated, err := capabilityKey(config.Capability{})
	if err != nil {
		t.Fatal(err)
	}
	if !generated {
		t.Error("generated = false for a key made with no signing_key_path")
	}
	if key.Private.IsZero() || len(key.Public()) != ed25519.PublicKeySize {
		t.Fatalf("key sealed = %v, public size = %d, want an Ed25519 pair", !key.Private.IsZero(), len(key.Public()))
	}
	if want := jwks.KeyID(key.Public()); key.KeyID != want {
		t.Errorf("KeyID = %q, want the derived %q", key.KeyID, want)
	}
}

// signing_key_kid names the published kid only for a key loaded from a path:
// a generated key is never published under an operator's name.
func TestCapabilityKeyGeneratedIgnoresKID(t *testing.T) {
	const kid = "operator-named"
	key, generated, err := capabilityKey(config.Capability{SigningKeyKID: kid})
	if err != nil {
		t.Fatal(err)
	}
	if !generated || key.KeyID == kid {
		t.Errorf("generated = %v, KeyID = %q: want a generated key under its derived kid", generated, key.KeyID)
	}
}

// A key at signing_key_path is loaded, not generated, under the derived kid
// unless signing_key_kid names one.
func TestCapabilityKeyLoadedFromPath(t *testing.T) {
	path, pub := writeSigningKey(t)
	const namedKID = "2026-10-signing"
	cases := map[string]struct {
		kid     string
		wantKID string
	}{
		"derived kid": {kid: "", wantKID: jwks.KeyID(pub)},
		"named kid":   {kid: namedKID, wantKID: namedKID},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			key, generated, err := capabilityKey(config.Capability{SigningKeyPath: path, SigningKeyKID: tc.kid})
			if err != nil {
				t.Fatal(err)
			}
			if generated {
				t.Error("generated = true for a key loaded from signing_key_path")
			}
			if !key.Public().Equal(pub) {
				t.Error("the loaded key is not the one at signing_key_path")
			}
			if key.KeyID != tc.wantKID {
				t.Errorf("KeyID = %q, want %q", key.KeyID, tc.wantKID)
			}
		})
	}
}

// A signing_key_path that holds no key is an error, never a fresh key: a
// missing mount must not silently become an ephemeral issuer.
func TestCapabilityKeyRefusesAnUnreadablePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pem")
	key, generated, err := capabilityKey(config.Capability{SigningKeyPath: missing})
	if err == nil {
		t.Fatal("a missing key file was accepted")
	}
	if generated || !key.Private.IsZero() {
		t.Errorf("generated = %v, key set = %v after a failed load", generated, !key.Private.IsZero())
	}
}

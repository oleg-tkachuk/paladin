package paladin

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A proof names its algorithm as RFC 9864 does: Ed25519, not the deprecated
// EdDSA, for an Ed25519 key, and ES256 for a P-256 one.
func TestDPoPProofNamesItsAlgorithm(t *testing.T) {
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for want, key := range map[string]crypto.Signer{dpopAlgEd25519: ed, dpopAlgES256: ec} {
		proof, err := dpopProof(key, "POST", "https://data.example.com/svc/M", "token", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.Split(proof, ".")[0])
		if err != nil {
			t.Fatal(err)
		}
		var header struct {
			Alg string `json:"alg"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		if header.Alg != want {
			t.Errorf("alg = %q, want %q", header.Alg, want)
		}
	}
}

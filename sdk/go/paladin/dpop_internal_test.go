package paladin

import (
	"context"
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

	"github.com/oleg-tkachuk/limes/dpop"
)

// The proof algorithms, as RFC 9864 names them.
const (
	algEd25519 = "Ed25519"
	algES256   = "ES256"
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
	for want, key := range map[string]crypto.Signer{algEd25519: ed, algES256: ec} {
		proof, err := dpopProof(context.Background(), key, "POST", "https://data.example.com/svc/M", "token", "", time.Now())
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

// The server's nonce is signed into the proof's claims, and absent without one.
func TestDPoPProofCarriesTheNonce(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, nonce := range []string{"", "server-nonce"} {
		proof, err := dpopProof(context.Background(), key, "POST", "https://data.example.com/svc/M", "token", nonce, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.Split(proof, ".")[1])
		if err != nil {
			t.Fatal(err)
		}
		var claims map[string]any
		if err := json.Unmarshal(raw, &claims); err != nil {
			t.Fatal(err)
		}
		got, has := claims["nonce"]
		if nonce == "" && has || nonce != "" && got != nonce {
			t.Errorf("nonce claim = %v (present %v), want %q", got, has, nonce)
		}
	}
}

// The header the SDK sends its proof in is the one limes reads it from. A
// literal in client.go, which the Python SDK's parity test reads.
func TestDPoPHeaderIsLimes(t *testing.T) {
	if HeaderDPoP != dpop.Header {
		t.Errorf("HeaderDPoP = %q, limes reads %q", HeaderDPoP, dpop.Header)
	}
}

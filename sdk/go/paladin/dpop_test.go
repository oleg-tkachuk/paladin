package paladin_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// RFC 8037 §A.3: the thumbprint of the Ed25519 key in §A.2.
func TestDPoPThumbprintMatchesRFC8037(t *testing.T) {
	x, _ := base64.RawURLEncoding.DecodeString("11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo")
	got, err := paladin.DPoPThumbprint(ed25519.PublicKey(x))
	if err != nil {
		t.Fatal(err)
	}
	if want := "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"; got != want {
		t.Fatalf("thumbprint = %s, want %s", got, want)
	}
}

func TestWithDPoPRefusesAnUnsupportedKey(t *testing.T) {
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := paladin.New("https://data.example.com", paladin.WithDPoP(p384)); err == nil {
		t.Fatal("a P-384 key was accepted; DPoP proofs here are EdDSA or ES256")
	}
}

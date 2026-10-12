package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

func TestBridgeKeyOptions(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	opts, err := bridgeKeyOptions(config.MCP{}, zap.NewNop())
	if err != nil || len(opts) != 0 {
		t.Errorf("no key: opts = %d, err = %v; want none", len(opts), err)
	}
	if _, err := bridgeKeyOptions(config.MCP{DPoPKey: "not a key"}, zap.NewNop()); err == nil {
		t.Error("a key the bridge cannot sign with was accepted")
	}
	opts, err = bridgeKeyOptions(config.MCP{DPoPKey: valid}, zap.NewNop())
	if err != nil || len(opts) != 1 {
		t.Errorf("valid key: opts = %d, err = %v; want one", len(opts), err)
	}
}

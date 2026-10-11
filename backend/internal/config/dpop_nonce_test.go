package config

import (
	"strings"
	"testing"
)

// The nonce key comes inline or from a Secret, not both: which would win is
// ambiguous.
func TestDPoPNonceKeyIsInlineOrASecret(t *testing.T) {
	c := minimalValidConfig()
	c.Capability.DPoPNonce.Key = strings.Repeat("k", 32)
	c.Capability.DPoPNonce.KeySecret = &SecretRef{Name: "paladin-dpop-nonce-key", Key: "key"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "dpop_nonce") {
		t.Fatalf("err = %v, want both refused", err)
	}
	c.Capability.DPoPNonce.KeySecret = nil
	if err := c.Validate(); err != nil {
		t.Fatalf("an inline key alone: %v", err)
	}
}

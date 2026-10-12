package mcp

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// pemPrivateKey is the PEM block type of a PKCS#8 private key.
const pemPrivateKey = "PRIVATE KEY"

// ErrUnsupportedDPoPKey is a bridge key DPoP cannot sign with: proofs are
// Ed25519 or ES256, so only Ed25519 and P-256 keys qualify.
var ErrUnsupportedDPoPKey = errors.New("mcp: the bridge DPoP key must be Ed25519 or ECDSA P-256")

// BridgeKey is the key the bridge proves possession of on the planes. A
// key-bound capability delegated to JKT can be forwarded through the
// bridge: it signs the DPoP proof for each call the capability rides on.
type BridgeKey struct {
	Signer crypto.Signer
	// JKT is the RFC 7638 thumbprint of the public key: the
	// confirmation_jkt to delegate a capability to the bridge with.
	JKT string
}

// ParseBridgeKey reads a PEM PKCS#8 Ed25519 or P-256 private key. It is
// parsed at boot so a wrong key stops the bridge rather than every call.
func ParseBridgeKey(pemText string) (*BridgeKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil || block.Type != pemPrivateKey {
		return nil, fmt.Errorf("mcp: the bridge DPoP key is not a PEM %q block", pemPrivateKey)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mcp: parse the bridge DPoP key: %w", err)
	}
	var signer crypto.Signer
	switch k := parsed.(type) {
	case ed25519.PrivateKey:
		signer = k
	case *ecdsa.PrivateKey:
		if k.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%w: curve %s", ErrUnsupportedDPoPKey, k.Curve.Params().Name)
		}
		signer = k
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedDPoPKey, parsed)
	}
	jkt, err := paladin.DPoPThumbprint(signer.Public())
	if err != nil {
		return nil, err
	}
	return &BridgeKey{Signer: signer, JKT: jkt}, nil
}

// errNoBridgeKey answers paladin_get_bridge_key on a bridge configured
// without mcp.dpop_key: nothing delegated to it could be forwarded.
var errNoBridgeKey = errors.New("this bridge holds no DPoP key, so it forwards unbound capabilities only")

// bridgeKeyResult is what paladin_get_bridge_key returns.
type bridgeKeyResult struct {
	ConfirmationJKT string `json:"confirmation_jkt"`
}

func registerBridgeKeyTool(s *mcpsdk.Server, c *Clients, filter *ToolFilter) {
	addTool(s, filter, &mcpsdk.Tool{
		Name: "paladin_get_bridge_key",
		Description: "The thumbprint of this bridge's DPoP key. To use a key-bound capability through the bridge, " +
			"delegate it (CapabilityService.Delegate, which needs the share op) with confirmation_jkt set to this value " +
			"and pass the child; the bridge proves possession for it.",
	}, func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
		if c.BridgeKey == nil {
			return nil, nil, errNoBridgeKey
		}
		res := bridgeKeyResult{ConfirmationJKT: c.BridgeKey.JKT}
		return jsonResult(&res, nil)
	})
}

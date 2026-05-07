package capability

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// JWKSDocument is the on-the-wire shape served at the issuer's JWKS
// endpoint (typically `/.well-known/jwks.json` on the admin plane).
// Verifiers in other planes / pods fetch and cache this document with
// a short TTL so capability verification stays local without an extra
// RPC per request.
//
// Format: RFC 7517 with kty=OKP and crv=Ed25519 (RFC 8037).
//
// Why this lives in the capability package rather than next to the
// existing internal/auth/jwks: that JWKS verifier consumes external
// IdP keys (RS256 / ES256). Capability tokens use a distinct cipher,
// distinct issuer set, distinct rotation cadence — sharing one
// document would conflate two trust roots and surprise auditors.
// Capability planes serve their own document; auth planes serve
// theirs (or proxy a customer IdP's).
type JWKSDocument struct {
	Keys []JWK `json:"keys"`
}

// JWK is one entry in the JWKS — exactly the fields needed to verify
// an Ed25519 signature; we do not embed `use` or `key_ops` because
// the kid + alg pair fully constrains use here.
type JWK struct {
	Kty string `json:"kty"`           // "OKP"
	Crv string `json:"crv"`           // "Ed25519"
	Kid string `json:"kid"`           // matches jwtHeader.Kid
	Alg string `json:"alg,omitempty"` // "EdDSA"
	X   string `json:"x"`             // base64url-encoded raw public key
}

// MarshalJWKS produces a JWKS document for the supplied kid → public key
// map. Iteration order is intentionally unstable (it's a map): callers
// who need stable output sort the result themselves. Most consumers
// (lookup-by-kid) don't care.
func MarshalJWKS(keys map[string]ed25519.PublicKey) ([]byte, error) {
	doc := JWKSDocument{Keys: make([]JWK, 0, len(keys))}
	for kid, pub := range keys {
		if len(pub) != ed25519.PublicKeySize {
			return nil, errors.New("capability: public key wrong size for Ed25519")
		}
		doc.Keys = append(doc.Keys, JWK{
			Kty: "OKP",
			Crv: "Ed25519",
			Alg: "EdDSA",
			Kid: kid,
			X:   base64.RawURLEncoding.EncodeToString(pub),
		})
	}
	return json.Marshal(doc)
}

// ParseJWKS decodes a JWKS document into a kid → public key map. Used
// by remote verifiers that fetch the document from the issuer's
// endpoint. Unknown / non-Ed25519 entries are skipped silently —
// future kty values land here without breaking older verifiers.
func ParseJWKS(raw []byte) (map[string]ed25519.PublicKey, error) {
	var doc JWKSDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]ed25519.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			continue
		}
		decoded, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		if len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("capability: invalid Ed25519 public key length")
		}
		out[k.Kid] = ed25519.PublicKey(decoded)
	}
	return out, nil
}

package capability

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Signer mints capability tokens. Production wiring uses an Ed25519
// keypair held by the cap-issuer service (split out at compliance
// scale per BACKLOG); the verifier picks up public keys from the JWKS
// endpoint and validates locally — no RPC per request.
//
// Signer is intentionally narrow: callers populate a Capability struct
// and ask Sign to encode + sign. The compact JWT format is the wire
// representation; we expose Encode / Decode helpers so debugging
// tooling can inspect tokens without the full verifier path.
type Signer interface {
	// KeyID is the JWKS key ID embedded in tokens this signer mints.
	// Verifiers index their key set by it, and rotation works by
	// running two signers in parallel until clients have caught up.
	KeyID() string

	// Sign emits a compact JWT for the supplied capability. The cap's
	// IssuedAt / NotBefore / ExpiresAt must already be populated; Sign
	// does not normalise times.
	Sign(c Capability) (string, error)
}

// Verifier validates tokens. Production verifiers cache the JWKS for
// the configured issuer and check the revocation list with a small
// in-memory TTL (≤2s) so revocation propagates quickly without
// hammering Postgres.
type Verifier interface {
	// Verify decodes the token, checks the signature against the
	// known JWKS, applies time / audience / revocation gates, and
	// returns the typed Capability on success.
	//
	// The audience parameter is the plane the call is hitting — the
	// verifier rejects tokens whose Capability.Audience does not
	// include it. Use one of the AudiencePlane* constants.
	Verify(ctx context.Context, token string, audience string) (*Capability, error)
}

// AudiencePlane* constants pin verifier expectations and capability
// claims. Mirrors the existing internal/auth audiences but lives
// alongside the capability types so cap-aware code never needs to
// import internal/auth (avoiding a circular dependency once the
// interceptor swap lands).
const (
	AudiencePlaneData  = "data"
	AudiencePlaneAdmin = "admin"
	AudiencePlaneMCP   = "mcp"
)

// ed25519Signer is the Ed25519-backed Signer.
type ed25519Signer struct {
	keyID string
	priv  ed25519.PrivateKey
}

// NewEd25519Signer constructs a signer from a key ID and Ed25519 private
// key. Caller is responsible for key generation / KMS interaction —
// this package is pure crypto + encoding.
func NewEd25519Signer(keyID string, priv ed25519.PrivateKey) (Signer, error) {
	if keyID == "" {
		return nil, errors.New("capability: signer key ID required")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("capability: ed25519 private key wrong length %d", len(priv))
	}
	return &ed25519Signer{keyID: keyID, priv: priv}, nil
}

func (s *ed25519Signer) KeyID() string { return s.keyID }

// jwtHeader is the protected header. We use compact serialization:
// `header.payload.signature` with each segment base64url-encoded.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// jwtClaims is the payload. We follow JWT conventions for standard
// claims (iss/sub/aud/iat/nbf/exp/jti) and namespace PALADIN-specific
// claims under `paladin:`. The unmarshal path tolerates unknown fields.
type jwtClaims struct {
	// Standard JWT claims.
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"` // canonical principal string
	Audience  []string `json:"aud"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf,omitempty"`
	ExpiresAt int64    `json:"exp"`
	ID        string   `json:"jti"`

	// PALADIN capability claims.
	PALADINPrincipal *Principal `json:"paladin_principal,omitempty"`
	PALADINCaveats   Caveats    `json:"paladin_caveats"`
	PALADINParentID  string     `json:"paladin_parent_id,omitempty"`
	PALADINGen       int64      `json:"paladin_gen,omitempty"`
}

// Sign implements Signer. Encodes the header + claims, signs the
// dotted concatenation, returns the compact form.
func (s *ed25519Signer) Sign(c Capability) (string, error) {
	if c.ID == uuid.Nil {
		return "", errors.New("capability: ID required")
	}
	if c.Issuer == "" {
		return "", errors.New("capability: Issuer required")
	}
	if c.Subject.TenantID == uuid.Nil {
		return "", errors.New("capability: Subject.TenantID required")
	}
	if len(c.Caveats.Ops) == 0 {
		return "", errors.New("capability: at least one Op required")
	}
	if c.ExpiresAt.Before(c.IssuedAt) || c.ExpiresAt.IsZero() {
		return "", errors.New("capability: ExpiresAt must be after IssuedAt")
	}

	header := jwtHeader{Alg: "EdDSA", Kid: s.keyID, Typ: "paladin-cap+jwt"}
	claims := jwtClaims{
		Issuer:       c.Issuer,
		Subject:      c.Subject.Subject,
		Audience:     c.Audience,
		IssuedAt:     c.IssuedAt.Unix(),
		ExpiresAt:    c.ExpiresAt.Unix(),
		ID:           c.ID.String(),
		PALADINPrincipal: &c.Subject,
		PALADINCaveats:   c.Caveats,
		PALADINGen:       c.Generation,
	}
	if !c.NotBefore.IsZero() {
		claims.NotBefore = c.NotBefore.Unix()
	}
	if c.ParentID != uuid.Nil {
		claims.PALADINParentID = c.ParentID.String()
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("capability: marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("capability: marshal claims: %w", err)
	}

	signingInput := strings.Join([]string{
		base64.RawURLEncoding.EncodeToString(headerJSON),
		base64.RawURLEncoding.EncodeToString(claimsJSON),
	}, ".")

	// Ed25519 ignores the crypto.Hash argument (PureEdDSA hashes the
	// message itself); we pass crypto.Hash(0) per the stdlib contract.
	sig, err := s.priv.Sign(nil, []byte(signingInput), crypto.Hash(0))
	if err != nil {
		return "", fmt.Errorf("capability: sign: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Decode parses a compact-form token without verifying the signature
// or applying time / audience / revocation gates. Use only for tooling
// (`paladin cap show`); production code goes through Verifier.Verify.
func Decode(token string) (*Capability, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("capability: token must have 3 segments")
	}

	var header jwtHeader
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("capability: decode header: %w", err)
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("capability: parse header: %w", err)
	}
	if header.Alg != "EdDSA" {
		return nil, fmt.Errorf("capability: unexpected alg %q (want EdDSA)", header.Alg)
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("capability: decode claims: %w", err)
	}
	var claims jwtClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("capability: parse claims: %w", err)
	}

	id, err := uuid.Parse(claims.ID)
	if err != nil {
		return nil, fmt.Errorf("capability: parse jti: %w", err)
	}
	cap := &Capability{
		ID:         id,
		Issuer:     claims.Issuer,
		Audience:   claims.Audience,
		Caveats:    claims.PALADINCaveats,
		IssuedAt:   time.Unix(claims.IssuedAt, 0).UTC(),
		ExpiresAt:  time.Unix(claims.ExpiresAt, 0).UTC(),
		Generation: claims.PALADINGen,
	}
	if claims.NotBefore != 0 {
		cap.NotBefore = time.Unix(claims.NotBefore, 0).UTC()
	}
	if claims.PALADINPrincipal != nil {
		cap.Subject = *claims.PALADINPrincipal
	} else {
		cap.Subject = Principal{Subject: claims.Subject}
	}
	if claims.PALADINParentID != "" {
		pid, err := uuid.Parse(claims.PALADINParentID)
		if err != nil {
			return nil, fmt.Errorf("capability: parse parent_id: %w", err)
		}
		cap.ParentID = pid
	}
	return cap, nil
}

// VerifySignature checks the Ed25519 signature on a compact token
// against the supplied public key. Time / audience / revocation gates
// are NOT applied here — Verify in the production verifier wraps this
// call with the full check chain.
func VerifySignature(token string, pub ed25519.PublicKey) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("capability: token must have 3 segments")
	}
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("capability: decode sig: %w", err)
	}
	if !ed25519.Verify(pub, []byte(signingInput), sig) {
		return ErrInvalidSignature
	}
	return nil
}

package capability

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Issuer mints capabilities. Two responsibilities, in this order:
//
//  1. Validate the request (caveats sane, parent narrowing if delegated).
//  2. Persist the audit row, then sign and return the token.
//
// The persist-then-sign order is deliberate. If we signed first and the
// DB write failed, an emitted token would have no record — verifiers
// that consult capability_records (admin tooling) would see a forged-
// looking token, and a missing row prevents Revoke from doing anything
// useful. Persist-first means the token in the caller's hand is always
// reflected in storage; a failed sign just means the row is unused
// (cleaned up by housekeeping at the natural expiry).
type Issuer struct {
	signer Signer
	store  Store
	clock  func() time.Time

	issuerName string
	defaultTTL time.Duration
}

// IssuerConfig is the wiring for Issuer.New.
type IssuerConfig struct {
	// Signer mints tokens. Production wraps a KMS-held private key;
	// dev / tests use NewEd25519Signer with a local generated key.
	Signer Signer

	// Store persists issuance + revocation rows.
	Store Store

	// IssuerName is the value placed in the `iss` claim. Verifiers
	// require this to be in their TrustedIssuers list.
	IssuerName string

	// DefaultTTL caps the lifetime when IssueRequest.TTL is zero.
	// Default 15 minutes when zero — short enough that revocation
	// propagation latency rarely matters.
	DefaultTTL time.Duration

	// Now is the clock; tests override.
	Now func() time.Time
}

// NewIssuer validates wiring and returns a ready Issuer.
func NewIssuer(cfg IssuerConfig) (*Issuer, error) {
	if cfg.Signer == nil {
		return nil, errors.New("capability: IssuerConfig.Signer required")
	}
	if cfg.Store == nil {
		return nil, errors.New("capability: IssuerConfig.Store required")
	}
	if cfg.IssuerName == "" {
		return nil, errors.New("capability: IssuerConfig.IssuerName required")
	}
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Issuer{
		signer:     cfg.Signer,
		store:      cfg.Store,
		clock:      cfg.Now,
		issuerName: cfg.IssuerName,
		defaultTTL: cfg.DefaultTTL,
	}, nil
}

// IssueRequest is the input shape for Issuer.Issue. All fields except
// TTL / NotBefore are required; the issuer fills in ID, IssuedAt,
// ExpiresAt, Issuer, Generation when not supplied.
type IssueRequest struct {
	Subject Principal
	// IssuedBy is the principal REQUESTING the capability — an operator for a
	// root issuance, the parent's holder for a delegation. Distinct from
	// Subject, which is who the capability authorises. Required: a capability
	// nobody requested cannot be audited, and the store rejects an empty one.
	IssuedBy   Principal
	Audience   []string
	Caveats    Caveats
	TTL        time.Duration // 0 → DefaultTTL
	NotBefore  time.Time     // zero → now
	Generation int64         // 0 → 1
}

// Issue mints a top-level capability. Generation defaults to 1 (rotation
// follows by reissuing with Generation+1 + revoking the previous row).
//
// Returns the typed Capability and the compact-form token in one call —
// callers usually need both: the token to ship to the agent, the struct
// to log / display.
func (i *Issuer) Issue(ctx context.Context, req IssueRequest) (*Capability, string, error) {
	if req.Subject.TenantID == uuid.Nil {
		return nil, "", errors.New("capability: Issue requires Subject.TenantID")
	}
	if len(req.Caveats.Ops) == 0 {
		return nil, "", errors.New("capability: Issue requires at least one Op")
	}
	if len(req.Audience) == 0 {
		return nil, "", errors.New("capability: Issue requires non-empty Audience")
	}

	now := i.clock().UTC()
	ttl := req.TTL
	if ttl == 0 {
		ttl = i.defaultTTL
	}
	cap := Capability{
		ID:         uuid.New(),
		Issuer:     i.issuerName,
		Subject:    req.Subject,
		Audience:   req.Audience,
		Caveats:    req.Caveats,
		IssuedAt:   now,
		ExpiresAt:  now.Add(ttl),
		Generation: req.Generation,
	}
	if cap.Generation == 0 {
		cap.Generation = 1
	}
	if !req.NotBefore.IsZero() {
		cap.NotBefore = req.NotBefore.UTC()
	}

	if req.IssuedBy.Subject == "" {
		return nil, "", errors.New("capability: IssuedBy is required")
	}

	if err := i.store.Insert(ctx, cap, req.IssuedBy); err != nil {
		return nil, "", fmt.Errorf("capability: persist issuance: %w", err)
	}
	token, err := i.signer.Sign(cap)
	if err != nil {
		return nil, "", fmt.Errorf("capability: sign: %w", err)
	}
	return &cap, token, nil
}

// DelegateRequest narrows from a parent capability the caller already
// holds. The issuer enforces strict narrowing (Narrows) before persisting
// so a runtime widening attempt fails before any token is emitted.
type DelegateRequest struct {
	Parent    Capability
	Subject   Principal
	Audience  []string
	Caveats   Caveats
	TTL       time.Duration
	NotBefore time.Time
}

// Delegate mints a child capability narrower than the supplied parent.
// The parent must already be a verified Capability — callers run it
// through Verifier.Verify first; the Issuer trusts the in-memory shape.
func (i *Issuer) Delegate(ctx context.Context, req DelegateRequest) (*Capability, string, error) {
	if req.Parent.ID == uuid.Nil {
		return nil, "", errors.New("capability: Delegate requires Parent.ID")
	}
	if req.Subject.TenantID == uuid.Nil {
		req.Subject.TenantID = req.Parent.Subject.TenantID
	}
	if len(req.Audience) == 0 {
		req.Audience = append([]string(nil), req.Parent.Audience...)
	}
	if len(req.Caveats.Ops) == 0 {
		// Default to parent ops; the caller must explicitly drop ops to
		// narrow, not implicitly inherit then narrow elsewhere.
		req.Caveats = req.Parent.Caveats
	}

	now := i.clock().UTC()
	ttl := req.TTL
	if ttl == 0 {
		ttl = i.defaultTTL
	}
	expires := now.Add(ttl)
	if expires.After(req.Parent.ExpiresAt) {
		expires = req.Parent.ExpiresAt
	}

	child := Capability{
		ID:         uuid.New(),
		Issuer:     i.issuerName,
		Subject:    req.Subject,
		Audience:   req.Audience,
		Caveats:    req.Caveats,
		IssuedAt:   now,
		ExpiresAt:  expires,
		ParentID:   req.Parent.ID,
		Generation: 1,
	}
	if !req.NotBefore.IsZero() {
		child.NotBefore = req.NotBefore.UTC()
	}

	if err := Narrows(req.Parent, child); err != nil {
		return nil, "", fmt.Errorf("capability: delegate %w", err)
	}

	// A delegation is requested by whoever holds the parent — that is not a
	// caller-supplied fact, it is what delegation means, so it is derived
	// rather than accepted as an argument.
	if err := i.store.Insert(ctx, child, req.Parent.Subject); err != nil {
		return nil, "", fmt.Errorf("capability: persist delegation: %w", err)
	}
	token, err := i.signer.Sign(child)
	if err != nil {
		return nil, "", fmt.Errorf("capability: sign delegation: %w", err)
	}
	return &child, token, nil
}

// GenerateEd25519Keypair is a convenience for boot wiring: returns a new
// Ed25519 keypair plus the kid you should associate with it. kid is
// derived from the public key bytes (first 16 hex chars of the raw key)
// so two boot paths producing the same key never end up with different
// kids — useful when a deploy upgrades the persisted key set without
// touching the JWKS doc.
func GenerateEd25519Keypair() (kid string, pub ed25519.PublicKey, priv ed25519.PrivateKey, err error) {
	pub, priv, err = ed25519.GenerateKey(nil)
	if err != nil {
		return "", nil, nil, fmt.Errorf("capability: generate keypair: %w", err)
	}
	kid = fmt.Sprintf("%x", pub[:8])
	return kid, pub, priv, nil
}

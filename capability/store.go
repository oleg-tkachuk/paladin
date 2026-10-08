package capability

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Store persists capability records and revocation entries. Consumers supply
// the implementation; memstore ships an in-memory one and the reference
// deployment supplies a relational one.
//
// Records are append-mostly: capabilities aren't mutated after issuance
// except via Revoke. Revocations are checked by the verifier on every
// request; put a CachedRevocationChecker in front to keep the per-call
// cost flat.
type Store interface {
	// Insert records a capability at issuance. The persisted row holds
	// the full claim set so admin tooling can render `paladin cap show`
	// without parsing the JWT, and so audit can correlate without
	// keeping every issued token.
	//
	// issuedBy is the principal that ASKED for the capability, which is not
	// c.Subject (the principal it authorises) and not c.Issuer (the service
	// that minted it). A delegated capability is requested by its parent's
	// holder; a root one by an operator. Passed as an argument rather than
	// carried on Capability because it is issuance metadata, not a claim —
	// putting it in the struct would change the token's frozen wire format.
	Insert(ctx context.Context, c Capability, issuedBy Principal) error

	// Get fetches a capability record by ID. Returns ErrNotFound when
	// no row exists; callers normally treat that as ErrInvalidSignature
	// (token forgery) rather than a missing entity.
	//
	// ErrNotFound is defined in THIS package (below), not in any store
	// implementation — a third party writing its own Store must have a
	// sentinel to return without importing someone else's persistence.
	Get(ctx context.Context, id uuid.UUID) (*Capability, error)

	// IsRevoked reports whether the capability, or any ancestor of it
	// that is still on record, is in the revocation list. Answering for
	// the chain is what makes revoking an orchestrator stop the workers it
	// spawned. Verifiers gate every request on it, so implementations
	// should make the walk a single round trip (a recursive query) and
	// bound its depth.
	IsRevoked(ctx context.Context, id uuid.UUID) (bool, error)

	// Revoke adds an entry to the revocation list. Idempotent: a
	// duplicate revoke is a no-op.
	//
	// Every descendant stops verifying either way, because IsRevoked
	// walks the chain. CascadeChildren=true additionally writes a
	// revocation entry for each descendant, so the audit trail names
	// every capability that was stopped, not only the one an operator
	// picked.
	Revoke(ctx context.Context, args RevokeRequest) error

	// PurgeExpired drops revocation rows whose underlying capability
	// has been expired for at least the supplied grace period. Run by
	// the housekeeping worker so the revocation list stays bounded.
	PurgeExpired(ctx context.Context, expiredFor time.Duration) (int64, error)

	// ListByPrincipal is admin-tooling support: list every active
	// capability issued to a principal (for display in the UI).
	// Pagination: cursor-based opaque to caller.
	ListByPrincipal(ctx context.Context, args ListByPrincipalRequest) ([]Capability, string, error)
}

// ErrNotFound is the typed not-found return from Store.Get. It lives in the
// core package rather than in a store implementation so that any consumer —
// in-memory, relational, or otherwise — can satisfy the Store contract using
// only what this package publishes (FR-004).
//
// Verifiers map it to an invalid-token response: a syntactically valid token
// whose record is absent was forged or purged, which is an authentication
// failure, not a missing entity.
var ErrNotFound = errors.New("capability: not found")

// RevokeRequest is the input shape for Store.Revoke.
type RevokeRequest struct {
	ID uuid.UUID
	// Reason is an operator-supplied label ("compromise", "rotation",
	// "policy-change"). Stored on the revocation row for audit.
	Reason string
	// Actor identifies who triggered the revocation (a user UUID or
	// an automation principal). Stored on the row.
	Actor string
	// CascadeChildren records a revocation entry for every descendant as
	// well. Descendants stop verifying regardless; see Store.Revoke.
	CascadeChildren bool
}

// ListByPrincipalRequest is the input shape for Store.ListByPrincipal.
type ListByPrincipalRequest struct {
	TenantID       uuid.UUID
	PrincipalType  PrincipalType
	Subject        string
	IncludeExpired bool
	IncludeRevoked bool
	Cursor         string
	Limit          int32
}

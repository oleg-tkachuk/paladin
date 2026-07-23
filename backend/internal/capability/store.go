package capability

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Store persists capability records and revocation entries. The Postgres
// implementation lands when the issuer / verifier RPC surface goes in;
// this interface is here so callers (issuer, verifier, admin tooling)
// have a stable seam from the start.
//
// Records are append-mostly: capabilities aren't mutated after issuance
// except via Revoke. Revocations are checked by the verifier on every
// request; production puts a small in-memory TTL cache (≤2s) in front
// to keep the per-call cost flat.
type Store interface {
	// Insert records a capability at issuance. The persisted row holds
	// the full claim set so admin tooling can render `paladin cap show`
	// without parsing the JWT, and so audit can correlate without
	// keeping every issued token.
	Insert(ctx context.Context, c Capability) error

	// Get fetches a capability record by ID. Returns ErrNotFound when
	// no row exists; callers normally treat that as ErrInvalidSignature
	// (token forgery) rather than a missing entity.
	//
	// ErrNotFound is defined in THIS package (below), not in any store
	// implementation — a third party writing its own Store must have a
	// sentinel to return without importing someone else's persistence.
	Get(ctx context.Context, id uuid.UUID) (*Capability, error)

	// IsRevoked reports whether the supplied capability ID is in the
	// revocation list. Cheap call — verifiers gate every request on it.
	IsRevoked(ctx context.Context, id uuid.UUID) (bool, error)

	// Revoke adds an entry to the revocation list. Idempotent: a
	// duplicate revoke is a no-op.
	//
	// CascadeChildren=true revokes every descendant in the delegation
	// tree (admin-tier action). When false, only the supplied ID is
	// revoked; child capabilities keep working until their own TTL
	// expires or they're revoked individually.
	Revoke(ctx context.Context, args RevokeArgs) error

	// PurgeExpired drops revocation rows whose underlying capability
	// has been expired for at least the supplied grace period. Run by
	// the housekeeping worker so the revocation list stays bounded.
	PurgeExpired(ctx context.Context, expiredFor time.Duration) (int64, error)

	// ListByPrincipal is admin-tooling support: list every active
	// capability issued to a principal (for display in the UI).
	// Pagination: cursor-based opaque to caller.
	ListByPrincipal(ctx context.Context, args ListByPrincipalArgs) ([]Capability, string, error)
}

// RevokeArgs is the input shape for Store.Revoke.
// ErrNotFound is the typed not-found return from Store.Get. It lives in the
// core package rather than in a store implementation so that any consumer —
// in-memory, relational, or otherwise — can satisfy the Store contract using
// only what this package publishes (FR-004).
//
// Verifiers map it to an invalid-token response: a syntactically valid token
// whose record is absent was forged or purged, which is an authentication
// failure, not a missing entity.
var ErrNotFound = errors.New("capability: not found")

type RevokeArgs struct {
	ID uuid.UUID
	// Reason is an operator-supplied label ("compromise", "rotation",
	// "policy-change"). Stored on the revocation row for audit.
	Reason string
	// Actor identifies who triggered the revocation (a user UUID or
	// an automation principal). Stored on the row.
	Actor string
	// CascadeChildren revokes every descendant in the delegation tree
	// (admin-tier). When false, only the supplied ID is revoked.
	CascadeChildren bool
}

// ListByPrincipalArgs is the input shape for Store.ListByPrincipal.
type ListByPrincipalArgs struct {
	TenantID       uuid.UUID
	PrincipalT     PrincipalType
	Subject        string
	IncludeExpired bool
	IncludeRevoked bool
	Cursor         string
	Limit          int32
}

// RevocationCache is a small in-memory map used by the verifier to
// avoid hitting Postgres on every request. Entries TTL out after the
// configured period (default 2s); a stale entry can serve a revoked
// token for that long, which is the usual safety / latency trade-off.
//
// The cache is intentionally simple — hits, misses, and writes are
// concurrency-safe via sync.RWMutex. A million entries fit comfortably
// in a few MB of heap; per-tenant caps fit individual pods just fine.
type RevocationCache struct {
	// implementation lands with the Postgres store; declared here so
	// callers can take a *RevocationCache parameter without an import
	// cycle once the verifier glues to the store.
}

package capability

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// UsageStore tracks per-capability runtime counters used to enforce
// the MaxRequests and MaxBudgetUSD caveats. Separated from Store so
// the issuance / revocation surface stays focused on identity, not
// runtime state.
//
// All methods are concurrency-safe — implementations atomic-UPSERT
// per capability row in a single round-trip. Two callers may race
// to bump the counter; whichever loses sees the post-increment value
// the other wrote.
type UsageStore interface {
	// BumpRequest increments the request counter and returns the new
	// value. When maxRequests > 0 and the post-increment would
	// exceed it, returns ErrRequestLimitExceeded without mutating
	// the row.
	BumpRequest(ctx context.Context, capID uuid.UUID, maxRequests int64) (newCount int64, err error)

	// Charge adds amountUSD to the spend counter. When maxBudgetUSD > 0
	// and the post-increment would exceed it, returns ErrBudgetExceeded
	// without mutating the row.
	Charge(ctx context.Context, capID uuid.UUID, amountUSD, maxBudgetUSD float64) (newSpent float64, err error)

	// Get returns the current snapshot. Returns ErrUsageNotFound when
	// no row exists for the capability — typically means it's never
	// been used (no requests, no charges).
	Get(ctx context.Context, capID uuid.UUID) (Usage, error)

	// Delete drops the row. Called by CapabilityPurger when the parent
	// capability is reaped.
	Delete(ctx context.Context, capID uuid.UUID) error

	// PurgeOrphans deletes usage rows whose capability_id is no longer
	// in capability_records. Bounded per-call (10k rows) so the
	// reaper sweep doesn't pin a single statement; caller loops.
	PurgeOrphans(ctx context.Context) (int64, error)
}

// Usage is the snapshot view of a capability's runtime counters.
type Usage struct {
	CapabilityID uuid.UUID
	RequestCount int64
	SpentUSD     float64
}

// Usage-related sentinels. ErrBudgetExceeded already lives in types.go
// (declared before the runtime enforcement landed); reused here.
var (
	// ErrRequestLimitExceeded — capability used MaxRequests times,
	// next attempt was blocked.
	ErrRequestLimitExceeded = errors.New("capability: request limit exceeded")

	// ErrUsageNotFound — no usage row exists for this capability.
	// Get-only; bump/charge always inserts on first hit.
	ErrUsageNotFound = errors.New("capability: usage row not found")
)

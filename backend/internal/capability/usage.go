package capability

import (
	"context"
	"errors"
	"time"

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

	// Charge adds amountUSD to the per-capability spend counter AND
	// to the tenant aggregate (when tenantID is non-zero). The two
	// counters are atomic individually; cross-counter consistency is
	// best-effort: if the tenant charge fails after the capability
	// charge succeeded, the capability spend remains incremented and
	// the caller is expected to refund. Order of checks:
	//
	//   1. Capability cap (cap.Caveats.MaxBudgetUSD)
	//   2. Tenant aggregate cap (tenant_budgets.max_budget_usd)
	//
	// If either rejects, returns the matching sentinel
	// (ErrBudgetExceeded for the capability, ErrTenantBudgetExceeded
	// for the tenant) and does NOT mutate the rejected counter.
	// On capability-side rejection the tenant counter is not bumped
	// (the call short-circuits). On tenant-side rejection the
	// capability counter has already been bumped — a refund is
	// queued via RefundCapability(amountUSD) so the operator's
	// audit reflects "attempted but rejected".
	//
	// tenantID == uuid.Nil disables the tenant-aggregate path.
	// Existing callers that don't yet pass a tenant ID get the
	// per-capability semantics from before this method's signature
	// change.
	Charge(
		ctx context.Context,
		capID uuid.UUID,
		amountUSD, maxBudgetUSD float64,
		tenantID uuid.UUID,
	) (newSpent float64, err error)

	// RefundCapability subtracts amountUSD from the per-capability
	// spend counter (floored at 0). Idempotent: a refund applied to a
	// row that doesn't exist is a no-op.
	RefundCapability(ctx context.Context, capID uuid.UUID, amountUSD float64) error

	// RefundTenant subtracts amountUSD from the tenant aggregate
	// counter (floored at 0). Used when a charge succeeded against
	// the capability but failed on the tenant cap, or when a handler
	// detects a partial-failure post-charge.
	RefundTenant(ctx context.Context, tenantID uuid.UUID, amountUSD float64) error

	// Get returns the current snapshot. Returns ErrUsageNotFound when
	// no row exists for the capability — typically means it's never
	// been used (no requests, no charges).
	Get(ctx context.Context, capID uuid.UUID) (Usage, error)

	// GetTenantBudget returns the tenant-aggregate snapshot. Returns
	// ErrTenantBudgetNotFound when no row exists (tenant has no cap
	// configured AND has never been charged).
	GetTenantBudget(ctx context.Context, tenantID uuid.UUID) (TenantBudget, error)

	// SetTenantBudget upserts the tenant cap. resetSpend=true rolls
	// the accounting period and zeroes spent_usd — operators call
	// this on each billing close. resetSpend=false adjusts the cap
	// mid-cycle without affecting accumulated spend.
	SetTenantBudget(ctx context.Context, args SetTenantBudgetArgs) (TenantBudget, error)

	// Delete drops the row. Called by CapabilityPurger when the parent
	// capability is reaped.
	Delete(ctx context.Context, capID uuid.UUID) error

	// PurgeOrphans deletes usage rows whose capability_id is no longer
	// in capability_records. Bounded per-call (10k rows) so the
	// reaper sweep doesn't pin a single statement; caller loops.
	PurgeOrphans(ctx context.Context) (int64, error)
}

// TenantBudget is the snapshot view of the tenant aggregate cap.
type TenantBudget struct {
	TenantID     uuid.UUID
	MaxBudgetUSD float64
	SpentUSD     float64
	PeriodStart  time.Time
	PeriodEnd    *time.Time
	UpdatedAt    time.Time
}

// SetTenantBudgetArgs is the input for UsageStore.SetTenantBudget.
type SetTenantBudgetArgs struct {
	TenantID     uuid.UUID
	MaxBudgetUSD float64
	// PeriodEnd, if non-nil, pins a closing time for the accounting
	// window. Nil = open-ended (typical for "just set the cap, I'll
	// roll later").
	PeriodEnd *time.Time
	// ResetSpend=true zeroes spent_usd and rolls period_start to now.
	// false leaves the counter alone — the cap changes mid-window.
	ResetSpend bool
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

	// ErrTenantBudgetExceeded — the tenant aggregate cap has been
	// hit. Distinct from ErrBudgetExceeded so admin tooling can
	// render "this tenant is out of budget" vs. "this capability is
	// out of budget". Operators handle differently — tenant lift
	// is an admin RPC; capability lift requires re-issuing the
	// caveat.
	ErrTenantBudgetExceeded = errors.New("capability: tenant aggregate budget exceeded")

	// ErrTenantBudgetNotFound — Get-only; the tenant has no cap
	// configured AND has never been charged. Distinct from "cap = 0"
	// (which means "configured but unlimited").
	ErrTenantBudgetNotFound = errors.New("capability: tenant budget row not found")
)

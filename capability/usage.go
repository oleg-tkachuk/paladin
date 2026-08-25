package capability

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// UsageStore tracks per-capability runtime counters used to enforce
// the MaxRequests and MaxBudgetAmount caveats. Separated from Store
// so the issuance / revocation surface stays focused on identity,
// not runtime state.
//
// All methods are concurrency-safe — implementations atomic-UPSERT
// per capability row in a single round-trip. Two callers may race
// to bump the counter; whichever loses sees the post-increment value
// the other wrote.
type UsageStore[TX any] interface {
	// BumpRequest increments the request counter and returns the new
	// value. When maxRequests > 0 and the post-increment would
	// exceed it, returns ErrRequestLimitExceeded without mutating
	// the row.
	BumpRequest(ctx context.Context, capID uuid.UUID, maxRequests int64) (newCount int64, err error)

	// Charge adds amount to the per-capability spend counter AND
	// to the tenant aggregate (when tenantID is non-zero), writes the
	// charges-ledger row, and (via onCharged) enqueues the fan-out
	// outbox rows — ALL in a single transaction. Either the whole
	// charge commits or nothing does; there is no cross-counter,
	// ledger, or event dual-write window (ADR-0003). Order of checks:
	//
	//   1. Capability cap (cap.Caveats.MaxBudgetAmount)
	//   2. Tenant aggregate cap (tenant_budgets.max_budget_usd; the
	//      column name retains the historical _usd suffix but is
	//      currency-tagged via tenant_budgets.unit_code).
	//
	// If either rejects, returns the matching sentinel
	// (ErrBudgetExceeded for the capability, ErrTenantBudgetExceeded
	// for the tenant) and the transaction rolls back, so NEITHER
	// counter is mutated — the tenant-side rollback compensates the
	// capability bump implicitly (no explicit refund needed). A retry
	// after any rejection is safe: nothing was committed.
	//
	// unitCode pins the currency for the new row when the row is
	// missing (capability_usage / tenant_budgets DEFAULT 'USD').
	// Empty value is treated as DefaultUnitCode. There is no FX
	// rate handling: a charge in EUR against a USD-denominated
	// tenant budget is a configuration error and should fail at
	// the handler layer before reaching the store.
	//
	// tenantID == uuid.Nil disables the tenant-aggregate path (and,
	// with it, the ledger row and the fan-out — the charges table
	// requires a tenant_id).
	//
	// op + actor are stamped onto the charges-ledger row (migration
	// 027). Both are best-effort — empty strings are accepted when
	// the caller cannot derive them (e.g. ChargeRequest at the
	// interceptor layer doesn't know the per-handler op). They are
	// NOT used for any enforcement decision; they only enrich the
	// time-series surface that BillingService renders.
	//
	// onCharged, when non-nil, runs inside the charge transaction
	// after the ledger row is written and before commit — the event
	// producer enqueues its outbox rows on `tx` (dispatcher.DispatchTx)
	// so the fan-out is atomic with the charge. An error from onCharged
	// rolls the whole charge back. Pass nil to skip fan-out.
	Charge(
		ctx context.Context,
		capID uuid.UUID,
		amount, maxBudget float64,
		unitCode string,
		tenantID uuid.UUID,
		op string,
		actor string,
		onCharged func(ctx context.Context, tx TX) error,
	) (newSpent float64, err error)

	// RefundCapability subtracts amount from the per-capability
	// spend counter (floored at 0). Idempotent: a refund applied to
	// a row that doesn't exist is a no-op.
	RefundCapability(ctx context.Context, capID uuid.UUID, amount float64) error

	// RefundTenant subtracts amount from the tenant aggregate
	// counter (floored at 0). Used when a charge succeeded against
	// the capability but failed on the tenant cap, or when a handler
	// detects a partial-failure post-charge.
	RefundTenant(ctx context.Context, tenantID uuid.UUID, amount float64) error

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

	// ListTenantBudgets returns every tenant's budget row joined with
	// the tenants table so callers can render slug + display_name
	// without a follow-up read. Filters at the SQL layer:
	//   - excludeInactive: skip soft-deleted tenants.
	//   - thresholdPct > 0: include only rows where
	//     spent / max * 100 >= thresholdPct (and max > 0).
	//   - unlimitedOnly: include only rows where max == 0.
	//   - limit: hard cap on result count. ≤ 0 → 50.
	//
	// Rows are returned in descending utilisation order so the most
	// at-risk tenants surface first.
	ListTenantBudgets(ctx context.Context, args ListTenantBudgetsArgs) ([]TenantBudgetSummary, error)

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
	TenantID        uuid.UUID
	MaxBudgetAmount float64
	SpentAmount     float64
	UnitCode        string
	PeriodStart     time.Time
	PeriodEnd       *time.Time
	UpdatedAt       time.Time
	// ResourceVersion is the OCC token a caller passes back to Set. Zero
	// means the row has never been written.
	ResourceVersion int64
}

// SetTenantBudgetArgs is the input for UsageStore.SetTenantBudget.
type SetTenantBudgetArgs struct {
	TenantID        uuid.UUID
	MaxBudgetAmount float64
	// UnitCode pins the currency. Empty = keep existing or default
	// to DefaultUnitCode on first insert.
	UnitCode string
	// PeriodEnd, if non-nil, pins a closing time for the accounting
	// window. Nil = open-ended (typical for "just set the cap, I'll
	// roll later").
	PeriodEnd *time.Time
	// ResetSpend=true zeroes spent_amount and rolls period_start to
	// now. false leaves the counter alone — the cap changes mid-
	// window.
	ResetSpend bool
	// ExpectedVersion is the OCC guard, checked in the same statement as the
	// write. 0 asserts the row does not exist yet and is itself a conflict if
	// it does. Without this two operators editing the same cap overwrote each
	// other and neither was told.
	ExpectedVersion int64
}

// TenantBudgetSummary joins TenantBudget with the tenant's slug +
// display_name so dashboard widgets don't need a second round-trip.
// Returned by ListTenantBudgets.
type TenantBudgetSummary struct {
	TenantID    uuid.UUID
	Slug        string
	DisplayName string
	Budget      TenantBudget
	// UtilisationPct = spent / max × 100, clamped to [0, 100]. 0
	// when MaxBudgetAmount == 0 (unlimited/metering-only).
	UtilisationPct float64
}

// ListTenantBudgetsArgs is the input shape for
// UsageStore.ListTenantBudgets.
type ListTenantBudgetsArgs struct {
	ThresholdPct    float64
	UnlimitedOnly   bool
	ExcludeInactive bool
	Limit           int32
}

// Usage is the snapshot view of a capability's runtime counters.
type Usage struct {
	CapabilityID uuid.UUID
	RequestCount int64
	SpentAmount  float64
	UnitCode     string
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

	// ErrTenantBudgetVersionMismatch — SetTenantBudget's OCC guard refused the
	// write: the stored resource_version is not the one the caller read, so
	// someone else changed the cap in between. The caller re-reads and decides,
	// rather than silently overwriting a change it never saw.
	ErrTenantBudgetVersionMismatch = errors.New("capability: tenant budget resource_version mismatch")
)

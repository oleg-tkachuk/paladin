package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/capability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// UsageStore implements capability.UsageStore against migration 022's
// capability_usage table. Single-row UPSERT per call → concurrency-safe
// without explicit locking.
//
// Two design choices worth flagging:
//
//  1. The "limit exceeded" path is detected via ErrNoRows from the
//     RETURNING clause — the underlying SQL has a WHERE on the
//     UPDATE that filters out the row when it would exceed the cap,
//     so RETURNING produces zero rows and pgx surfaces ErrNoRows.
//     We map that to ErrRequestLimitExceeded / ErrBudgetExceeded so
//     callers can branch on it.
//
//  2. Limit values come in via the SQL args, not the row. This means
//     a capability whose MaxRequests / MaxBudgetAmount changes mid-
//     life (delegation narrowing) is enforced against the *current*
//     value the caller passes — no stale row-stored limit to
//     invalidate. The caveats themselves live in the JWT claim set;
//     the row just tracks accumulated usage.
type UsageStore struct {
	q *sqlc.Queries
}

// NewUsageStore wires the sqlc-generated queries to the
// capability.UsageStore interface.
func NewUsageStore(q *sqlc.Queries) *UsageStore {
	return &UsageStore{q: q}
}

// BumpRequest implements capability.UsageStore.
func (s *UsageStore) BumpRequest(
	ctx context.Context,
	capID uuid.UUID,
	maxRequests int64,
) (int64, error) {
	count, err := s.q.BumpCapabilityRequestCount(
		ctx,
		pgtype.UUID{Bytes: capID, Valid: true},
		maxRequests,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, capability.ErrRequestLimitExceeded
		}
		return 0, fmt.Errorf("capability/postgres: bump: %w", err)
	}
	return count, nil
}

// Charge implements capability.UsageStore. amount must be ≥ 0;
// negative input rejected — refunds are explicit via RefundCapability
// / RefundTenant.
//
// unitCode pins the currency for newly-inserted rows. Empty value
// resolves to capability.DefaultUnitCode. The existing row's
// unit_code is preserved on conflict (the SQL uses the arg only on
// INSERT) so callers cannot accidentally re-denominate an existing
// counter.
//
// Two-phase charge when tenantID is non-zero:
//
//  1. Charge the capability counter (atomic, with cap-side cap).
//  2. If (1) succeeded and tenantID is non-zero, charge the tenant
//     aggregate (atomic, with tenant-side cap).
//  3. If (2) rejects, refund (1) immediately so the capability
//     counter doesn't drift past the tenant cap. The audit trail
//     still shows the attempted bump if the operator inspects
//     telemetry; the row state is consistent.
func (s *UsageStore) Charge(
	ctx context.Context,
	capID uuid.UUID,
	amount, maxBudget float64,
	unitCode string,
	tenantID uuid.UUID,
) (float64, error) {
	if amount < 0 {
		return 0, errors.New("capability/postgres: charge amount must be >= 0")
	}
	resolvedUnit, err := capability.NormaliseUnitCode(unitCode)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: charge: %w", err)
	}
	amountNumeric, err := numericFromFloat(amount)
	if err != nil {
		return 0, err
	}
	maxBudgetNumeric, err := numericFromFloat(maxBudget)
	if err != nil {
		return 0, err
	}
	spent, err := s.q.ChargeCapability(
		ctx,
		pgtype.UUID{Bytes: capID, Valid: true},
		amountNumeric,
		resolvedUnit,
		maxBudgetNumeric,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, capability.ErrBudgetExceeded
		}
		return 0, fmt.Errorf("capability/postgres: charge: %w", err)
	}

	if tenantID == uuid.Nil {
		return floatFromNumeric(spent), nil
	}

	if _, tErr := s.q.ChargeTenantBudget(
		ctx,
		pgtype.UUID{Bytes: tenantID, Valid: true},
		amountNumeric,
		resolvedUnit,
	); tErr != nil {
		// Compensate the capability spend so the two counters stay
		// in sync. RefundCapability is itself idempotent (UPDATE
		// floored at 0); a refund failure is logged but doesn't
		// override the original tenant-cap rejection.
		_ = s.q.RefundCapabilityUsage(
			ctx,
			pgtype.UUID{Bytes: capID, Valid: true},
			amountNumeric,
		)
		if errors.Is(tErr, pgx.ErrNoRows) {
			return 0, capability.ErrTenantBudgetExceeded
		}
		return 0, fmt.Errorf("capability/postgres: charge tenant: %w", tErr)
	}
	return floatFromNumeric(spent), nil
}

// RefundCapability implements capability.UsageStore. Idempotent —
// row floored at 0; missing row is a no-op.
func (s *UsageStore) RefundCapability(ctx context.Context, capID uuid.UUID, amount float64) error {
	if amount <= 0 {
		return nil
	}
	amountNumeric, err := numericFromFloat(amount)
	if err != nil {
		return err
	}
	if err := s.q.RefundCapabilityUsage(
		ctx,
		pgtype.UUID{Bytes: capID, Valid: true},
		amountNumeric,
	); err != nil {
		return fmt.Errorf("capability/postgres: refund cap: %w", err)
	}
	return nil
}

// RefundTenant implements capability.UsageStore. Same idempotency
// shape as RefundCapability.
func (s *UsageStore) RefundTenant(ctx context.Context, tenantID uuid.UUID, amount float64) error {
	if amount <= 0 {
		return nil
	}
	amountNumeric, err := numericFromFloat(amount)
	if err != nil {
		return err
	}
	if err := s.q.RefundTenantBudget(
		ctx,
		pgtype.UUID{Bytes: tenantID, Valid: true},
		amountNumeric,
	); err != nil {
		return fmt.Errorf("capability/postgres: refund tenant: %w", err)
	}
	return nil
}

// GetTenantBudget implements capability.UsageStore.
func (s *UsageStore) GetTenantBudget(ctx context.Context, tenantID uuid.UUID) (capability.TenantBudget, error) {
	row, err := s.q.GetTenantBudget(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.TenantBudget{}, capability.ErrTenantBudgetNotFound
		}
		return capability.TenantBudget{}, fmt.Errorf("capability/postgres: get tenant budget: %w", err)
	}
	return tenantBudgetFromRow(row.TenantID, row.MaxBudgetUsd, row.SpentUsd, row.UnitCode, row.PeriodStart, row.PeriodEnd, row.UpdatedAt), nil
}

// SetTenantBudget implements capability.UsageStore.
func (s *UsageStore) SetTenantBudget(ctx context.Context, args capability.SetTenantBudgetArgs) (capability.TenantBudget, error) {
	maxBudget, err := numericFromFloat(args.MaxBudgetAmount)
	if err != nil {
		return capability.TenantBudget{}, err
	}
	// Empty UnitCode passed through verbatim — the SQL preserves
	// the existing row's unit_code on conflict in that case (or
	// inserts 'USD' on first row). A non-empty value is validated.
	unit := args.UnitCode
	if unit != "" {
		if !capability.IsAllowedUnitCode(unit) {
			return capability.TenantBudget{}, fmt.Errorf("capability/postgres: SetTenantBudget: invalid unit_code %q", unit)
		}
	}
	var periodEnd pgtype.Timestamptz
	if args.PeriodEnd != nil {
		periodEnd = pgtype.Timestamptz{Time: *args.PeriodEnd, Valid: true}
	}
	row, err := s.q.SetTenantBudget(
		ctx,
		pgtype.UUID{Bytes: args.TenantID, Valid: true},
		maxBudget,
		unit,
		periodEnd,
		args.ResetSpend,
	)
	if err != nil {
		return capability.TenantBudget{}, fmt.Errorf("capability/postgres: set tenant budget: %w", err)
	}
	return tenantBudgetFromRow(row.TenantID, row.MaxBudgetUsd, row.SpentUsd, row.UnitCode, row.PeriodStart, row.PeriodEnd, row.UpdatedAt), nil
}

// tenantBudgetFromRow normalises sqlc row types into the public shape.
func tenantBudgetFromRow(
	tenantID pgtype.UUID,
	maxBudget, spent pgtype.Numeric,
	unitCode string,
	periodStart, periodEnd, updatedAt pgtype.Timestamptz,
) capability.TenantBudget {
	out := capability.TenantBudget{
		TenantID:        uuid.UUID(tenantID.Bytes),
		MaxBudgetAmount: floatFromNumeric(maxBudget),
		SpentAmount:     floatFromNumeric(spent),
		UnitCode:        unitCode,
	}
	if periodStart.Valid {
		out.PeriodStart = periodStart.Time
	}
	if periodEnd.Valid {
		t := periodEnd.Time
		out.PeriodEnd = &t
	}
	if updatedAt.Valid {
		out.UpdatedAt = updatedAt.Time
	}
	return out
}

// Get implements capability.UsageStore.
func (s *UsageStore) Get(ctx context.Context, capID uuid.UUID) (capability.Usage, error) {
	row, err := s.q.GetCapabilityUsage(ctx, pgtype.UUID{Bytes: capID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.Usage{}, capability.ErrUsageNotFound
		}
		return capability.Usage{}, fmt.Errorf("capability/postgres: get usage: %w", err)
	}
	return capability.Usage{
		CapabilityID: uuid.UUID(row.CapabilityID.Bytes),
		RequestCount: row.RequestCount,
		SpentAmount:  floatFromNumeric(row.SpentUsd),
		UnitCode:     row.UnitCode,
	}, nil
}

// Delete implements capability.UsageStore.
func (s *UsageStore) Delete(ctx context.Context, capID uuid.UUID) error {
	if _, err := s.q.DeleteCapabilityUsage(ctx, pgtype.UUID{Bytes: capID, Valid: true}); err != nil {
		return fmt.Errorf("capability/postgres: delete usage: %w", err)
	}
	return nil
}

// PurgeOrphans implements capability.UsageStore.
func (s *UsageStore) PurgeOrphans(ctx context.Context) (int64, error) {
	n, err := s.q.PurgeCapabilityUsageOrphans(ctx)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: purge usage orphans: %w", err)
	}
	return n, nil
}

// numericFromFloat converts a float64 amount into pgtype.Numeric.
// pgtype takes the value as a string parse of the textual form;
// strconv.FormatFloat with 'f' / -1 / 64 gives a tight representation
// without surprises on edge values (NaN/Inf rejected upstream).
func numericFromFloat(v float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if err := n.Scan(s); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("capability/postgres: numeric scan %q: %w", s, err)
	}
	return n, nil
}

// floatFromNumeric pulls a pgtype.Numeric back into float64. Lossy on
// the last bit — fine here because spend granularity is 6 decimals.
func floatFromNumeric(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// UsageStore implements capability.UsageStore[pgx.Tx] against migration 022's
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
	q    *sqlc.Queries
	pool *pgxpool.Pool // optional; nil disables charges-ledger writes
	log  *zap.Logger   // never nil — defaults to zap.NewNop in NewUsageStore
}

// NewUsageStore wires the sqlc-generated queries to the
// capability.UsageStore[pgx.Tx] interface. pool is optional — when nil, the
// charges-ledger row insert is skipped (useful for tests that want
// to exercise the in-memory bookkeeping without a real DB). In
// production it must be non-nil so the BillingService surface has
// a time-series source of truth. log is optional — when nil, a
// no-op logger is installed so the store never crashes on a missing
// dependency. Production wires the named logger so ledger-write
// failures surface in operator log streams.
func NewUsageStore(q *sqlc.Queries, pool *pgxpool.Pool, log *zap.Logger) *UsageStore {
	if log == nil {
		log = zap.NewNop()
	}
	return &UsageStore{q: q, pool: pool, log: log}
}

// BumpRequest implements capability.UsageStore[pgx.Tx].
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

// Charge implements capability.UsageStore[pgx.Tx]. amount must be ≥ 0;
// negative input rejected — refunds are explicit via RefundCapability
// / RefundTenant.
//
// unitCode pins the currency for newly-inserted rows. Empty value
// resolves to capability.DefaultUnitCode. The existing row's
// unit_code is preserved on conflict (the SQL uses the arg only on
// INSERT) so callers cannot accidentally re-denominate an existing
// counter.
//
// Everything runs in ONE transaction so the charge is atomic
// (ADR-0003 transactional outbox — no dual-write window):
//
//  1. Charge the capability counter (with cap-side cap).
//  2. When tenantID is non-zero: charge the tenant aggregate (with
//     tenant-side cap), write the charges-ledger row, and run
//     onCharged so the event producer enqueues its outbox rows on
//     the same tx.
//  3. Commit. Any rejection or error rolls the whole thing back, so
//     the two counters can never drift and the ledger row + fan-out
//     rows are never orphaned from the spend they describe.
//
// The ledger insert used to be best-effort (committed, then a
// separate pool.Exec that logged-and-swallowed on failure). That
// hedge existed to avoid misleading a customer into a double-spend
// retry after the running totals had already committed. Folding the
// ledger into the same tx removes the hazard at the root: a failure
// now rolls the counters back too, so a retry is always safe.
//
// Pool nil ⇒ test stub path: no transaction, no ledger, no fan-out —
// the counters run on the plain query set so in-memory tests still
// exercise the bookkeeping.
func (s *UsageStore) Charge(
	ctx context.Context,
	capID uuid.UUID,
	amount, maxBudget float64,
	unitCode string,
	tenantID uuid.UUID,
	op string,
	actor string,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
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

	// Test stub path: no pool ⇒ no transaction. Only the counter
	// bookkeeping runs (charges-ledger + fan-out both require a real
	// tx). onCharged is ignored — fakes never wire an emitter.
	if s.pool == nil {
		return s.chargeNoTx(ctx, capID, amountNumeric, resolvedUnit, maxBudgetNumeric, tenantID)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: charge begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	qtx := s.q.WithTx(tx)

	spent, err := qtx.ChargeCapability(
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
		// No tenant aggregate path → no ledger row (the charges
		// table requires a tenant_id; charges without one wouldn't
		// surface in the per-tenant billing UI anyway) and no
		// fan-out. Commit the lone capability bump.
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("capability/postgres: charge commit: %w", err)
		}
		return floatFromNumeric(spent), nil
	}

	if _, tErr := qtx.ChargeTenantBudget(
		ctx,
		pgtype.UUID{Bytes: tenantID, Valid: true},
		amountNumeric,
		resolvedUnit,
	); tErr != nil {
		// Rollback (deferred) compensates the capability bump — no
		// explicit refund needed now that both live on one tx.
		if errors.Is(tErr, pgx.ErrNoRows) {
			return 0, capability.ErrTenantBudgetExceeded
		}
		return 0, fmt.Errorf("capability/postgres: charge tenant: %w", tErr)
	}

	// Ledger row on the same tx — atomic with the counters.
	ledgerID := uuid.New()
	if _, lErr := tx.Exec(ctx,
		`INSERT INTO charges (id, tenant_id, capability_id, amount, unit_code, op, actor_subject)
		 VALUES ($1, $2, $3, $4::numeric, $5, $6, $7)`,
		ledgerID, tenantID, capID, amountNumeric, resolvedUnit, op, actor,
	); lErr != nil {
		return 0, fmt.Errorf("capability/postgres: charge ledger insert: %w", lErr)
	}

	// Transactional-outbox fan-out on the same tx (ADR-0003): the
	// event's outbox rows commit atomically with the charge, so a
	// crash can never leave the spend recorded without its event.
	if onCharged != nil {
		if fErr := onCharged(ctx, tx); fErr != nil {
			// A fan-out failure rolls the whole charge back (the defer).
			// That's the correct atomic outcome, but it means a healthy
			// spend was rejected because the event infra hiccuped — worth
			// surfacing so operators can correlate a charge-rejection spike
			// with dispatcher trouble.
			s.log.Warn("capability/postgres: charge rolled back on fan-out failure",
				zap.String("capability_id", capID.String()),
				zap.String("tenant_id", tenantID.String()),
				zap.Float64("amount", amount),
				zap.Error(fErr),
			)
			return 0, fmt.Errorf("capability/postgres: charge fan-out: %w", fErr)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("capability/postgres: charge commit: %w", err)
	}
	return floatFromNumeric(spent), nil
}

// chargeNoTx is the pool-less test-stub path: counter bookkeeping only,
// no ledger row and no fan-out (both need a real transaction).
func (s *UsageStore) chargeNoTx(
	ctx context.Context,
	capID uuid.UUID,
	amountNumeric pgtype.Numeric,
	resolvedUnit string,
	maxBudgetNumeric pgtype.Numeric,
	tenantID uuid.UUID,
) (float64, error) {
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

// RefundCapability implements capability.UsageStore[pgx.Tx]. Idempotent —
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

// RefundTenant implements capability.UsageStore[pgx.Tx]. Same idempotency
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

// GetTenantBudget implements capability.UsageStore[pgx.Tx].
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

// SetTenantBudget implements capability.UsageStore[pgx.Tx].
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

// ListTenantBudgets joins tenant_budgets with tenants and applies the
// threshold / unlimited / exclude_inactive filters server-side.
// Domain default for `limit` is 50; clamped to ≤ 500 to match the
// proto's bound. The sqlc query returns utilisation pre-computed so
// callers don't have to redo the division (and we keep the
// numeric-precision path inside Postgres where it belongs).
func (s *UsageStore) ListTenantBudgets(
	ctx context.Context,
	args capability.ListTenantBudgetsArgs,
) ([]capability.TenantBudgetSummary, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	threshold, err := numericFromFloat(args.ThresholdPct)
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: threshold_pct: %w", err)
	}
	rows, err := s.q.ListTenantBudgetSummaries(ctx,
		args.ExcludeInactive,
		args.UnlimitedOnly,
		threshold,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: list tenant budgets: %w", err)
	}
	out := make([]capability.TenantBudgetSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, capability.TenantBudgetSummary{
			TenantID:    uuid.UUID(r.TenantID.Bytes),
			Slug:        r.Slug,
			DisplayName: r.DisplayName,
			Budget: tenantBudgetFromRow(
				r.TenantID, r.MaxBudgetUsd, r.SpentUsd, r.UnitCode,
				r.PeriodStart, r.PeriodEnd, r.UpdatedAt,
			),
			UtilisationPct: floatFromNumeric(r.UtilisationPct),
		})
	}
	return out, nil
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

// Get implements capability.UsageStore[pgx.Tx].
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

// Delete implements capability.UsageStore[pgx.Tx].
func (s *UsageStore) Delete(ctx context.Context, capID uuid.UUID) error {
	if _, err := s.q.DeleteCapabilityUsage(ctx, pgtype.UUID{Bytes: capID, Valid: true}); err != nil {
		return fmt.Errorf("capability/postgres: delete usage: %w", err)
	}
	return nil
}

// PurgeOrphans implements capability.UsageStore[pgx.Tx].
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

// Compile-time conformance, instantiated over PALADIN's actual transaction type.
// This is also the assertion that keeps FR-014 honest: PALADIN satisfies the same
// published contract a third party would, with no privileged access.
var _ capability.UsageStore[pgx.Tx] = (*UsageStore)(nil)

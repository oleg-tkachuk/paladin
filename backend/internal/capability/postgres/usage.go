package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/capability"
)

// UsageStore implements capability.UsageStore[pgx.Tx] against the schema baseline (001_initial_schema.sql)'s
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
//  2. Limit values come in via the SQL args, not the row: the leaf's from
//     the verified token, each ancestor's from its capability_records
//     row. The usage row just tracks accumulated usage — for a capability
//     with delegated children, the whole subtree's.
type UsageStore struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool // required — see NewUsageStore
	log  *zap.Logger   // never nil — defaults to zap.NewNop in NewUsageStore
}

// NewUsageStore wires the sqlc-generated queries to the
// capability.UsageStore[pgx.Tx] interface.
//
// pool is REQUIRED. It used to be optional, with a nil value selecting a
// "test stub path" that ran the counters and skipped the charges ledger and
// the outbox fan-out. That made one type carry two contracts: the same Charge
// call either committed a spend with its ledger row and its event, or
// committed the spend alone — and the caller had no way to tell which
// instance it held. A weaker guarantee reachable by construction is worse
// than no guarantee, because the strong one is what every call site was
// written against.
//
// Nothing selected it. Not production, not a single test. So the branch is
// gone and the requirement is explicit: a nil pool panics here, at wiring
// time, rather than silently downgrading every charge that follows.
//
// log is genuinely optional — nil installs a no-op, and a missing logger
// costs visibility, not correctness.
func NewUsageStore(q *sqlc.Queries, pool *pgxpool.Pool, log *zap.Logger) *UsageStore {
	if pool == nil {
		panic("capability/postgres: NewUsageStore requires a non-nil pool")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &UsageStore{q: q, pool: pool, log: log}
}

// maxLineageDepth bounds the ancestor walk, matching Revoke's cascade guard.
const maxLineageDepth = 64

// noCeiling is the ceiling argument the counter queries read as "no limit".
// An overrun under OverrunRecord re-runs a refused query with it.
var noCeiling = pgtype.Numeric{Int: big.NewInt(0), Valid: true}

// chargeTenantPastCeilingQuery adds to the tenant aggregate whatever its
// ceiling, as ChargeTenantBudget does short of the check: a cost already
// incurred, recorded under OverrunRecord.
const chargeTenantPastCeilingQuery = `
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start, updated_at)
VALUES ($1, 0, $2::numeric, COALESCE(NULLIF($3::text, ''), 'USD'), now(), now())
ON CONFLICT (tenant_id) DO UPDATE
SET spent_usd  = tenant_budgets.spent_usd + $2::numeric,
    updated_at = now()
RETURNING spent_usd;
`

// chargeByRefQuery finds the charge a capability already took under an
// external ref.
const chargeByRefQuery = `SELECT id, overrun FROM charges WHERE capability_id = $1 AND external_ref = $2`

// chargeByReservationQuery finds the charge that settled a reservation.
const chargeByReservationQuery = `SELECT id, capability_id, overrun FROM charges WHERE reservation_id = $1`

// spentQuery reads a capability's spend for a replayed receipt.
const spentQuery = `SELECT COALESCE((SELECT spent_usd FROM capability_usage WHERE capability_id = $1), 0)`

// replay is the receipt for a charge already in the ledger, returned when the
// same charge arrives again: nothing moves, and Spent is read as it is now.
func replay(ctx context.Context, tx pgx.Tx, chargeID, capID uuid.UUID, overrun bool) (capability.ChargeReceipt, error) {
	var spent pgtype.Numeric
	if err := tx.QueryRow(ctx, spentQuery, capID).Scan(&spent); err != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: replay read spend: %w", err)
	}
	return capability.ChargeReceipt{ChargeID: chargeID, Spent: floatFromNumeric(spent), Replayed: true, Overrun: overrun}, nil
}

// ancestor is one link of a capability's delegation chain, with the ceilings
// its own record carries. The ceilings are read from capability_records, not
// taken from the caller: only the leaf's limits arrive in the verified token.
type ancestor struct {
	id          uuid.UUID
	maxBudget   pgtype.Numeric
	maxRequests int64
}

// ancestorsQuery walks parent_id upwards from a capability, nearest first. It
// runs on the charging transaction, whose connection is scoped to the
// capability's tenant; delegation never crosses tenants, so the whole chain is
// visible under that scope. Caveats are stored as the Go struct's JSON, hence
// the field names.
const ancestorsQuery = `
WITH RECURSIVE chain(id, parent_id, caveats, depth) AS (
    SELECT id, parent_id, caveats, 0 FROM capability_records WHERE id = $1
    UNION ALL
    SELECT r.id, r.parent_id, r.caveats, c.depth + 1
    FROM   capability_records r
    JOIN   chain c ON r.id = c.parent_id
    WHERE  c.depth < $2
)
SELECT id,
       COALESCE((caveats->>'MaxBudgetAmount')::numeric, 0),
       COALESCE((caveats->>'MaxRequests')::bigint, 0)
FROM   chain
WHERE  depth > 0
ORDER  BY depth;
`

func ancestorsOf(ctx context.Context, tx pgx.Tx, capID uuid.UUID) ([]ancestor, error) {
	rows, err := tx.Query(ctx, ancestorsQuery, capID, maxLineageDepth)
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: lineage: %w", err)
	}
	defer rows.Close()
	var out []ancestor
	for rows.Next() {
		var a ancestor
		if err := rows.Scan(&a.id, &a.maxBudget, &a.maxRequests); err != nil {
			return nil, fmt.Errorf("capability/postgres: lineage scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("capability/postgres: lineage: %w", err)
	}
	return out, nil
}

// BumpRequest implements capability.Meter. The capability and every
// ancestor are bumped in one transaction, leaf first and then nearest
// ancestor first — the same order every charge takes, so two requests
// sharing part of a chain lock it in the same order and cannot deadlock.
func (s *UsageStore) Bump(ctx context.Context, req capability.BumpRequest) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: bump begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	qtx := s.q.WithTx(tx)

	if err := bumpCopies(ctx, tx, req.CapabilityID, req.Copies); err != nil {
		return 0, err
	}
	count, err := qtx.BumpCapabilityRequestCount(ctx, pgtype.UUID{Bytes: req.CapabilityID, Valid: true}, req.MaxRequests)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, capability.ErrRequestLimitExceeded
		}
		return 0, fmt.Errorf("capability/postgres: bump: %w", err)
	}
	ancestors, err := ancestorsOf(ctx, tx, req.CapabilityID)
	if err != nil {
		return 0, err
	}
	for _, a := range ancestors {
		if _, err := qtx.BumpCapabilityRequestCount(ctx, pgtype.UUID{Bytes: a.id, Valid: true}, a.maxRequests); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, fmt.Errorf("%w: ancestor %s", capability.ErrRequestLimitExceeded, a.id)
			}
			return 0, fmt.Errorf("capability/postgres: bump ancestor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("capability/postgres: bump commit: %w", err)
	}
	return count, nil
}

// Charge implements capability.Meter.
//
// unitCode pins the currency for newly-inserted rows. Empty value
// resolves to capability.DefaultUnitCode. An existing row's unit_code is
// preserved on conflict (the SQL uses the arg only on INSERT) so callers
// cannot accidentally re-denominate an existing counter.
//
// Everything runs in ONE transaction so the charge is atomic
// (ADR-0003 transactional outbox — no dual-write window):
//
//  1. Charge the capability counter (against the token's ceiling).
//  2. Charge each ancestor's counter (against the ceiling on its record),
//     so a parent's budget bounds everything delegated from it.
//  3. Charge the tenant aggregate (against the tenant ceiling).
//  4. Write the charges-ledger row and run onCharged so the event producer
//     enqueues its outbox rows on the same tx.
//  5. Commit. Any rejection or error rolls the whole thing back, so the
//     counters can never drift and the ledger row + fan-out rows are never
//     orphaned from the spend they describe.
func (s *UsageStore) Charge(
	ctx context.Context,
	req capability.ChargeRequest,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
) (capability.ChargeReceipt, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return capability.ChargeReceipt{}, err
	}
	if err := req.Overrun.Validate(); err != nil {
		return capability.ChargeReceipt{}, err
	}
	if err := capability.ValidateExternalRef(req.ExternalRef); err != nil {
		return capability.ChargeReceipt{}, err
	}
	if req.TenantID == uuid.Nil {
		// The ledger row and the tenant ceiling both need one, and a
		// capability always has one — the verifier refuses a tenantless token.
		return capability.ChargeReceipt{}, errors.New("capability/postgres: charge requires a tenant")
	}
	resolvedUnit, err := capability.NormaliseUnitCode(req.UnitCode)
	if err != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge: %w", err)
	}
	amountNumeric, err := numericFromFloat(req.Amount)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	maxBudgetNumeric, err := numericFromFloat(req.MaxBudget)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit

	if req.ExternalRef != "" {
		// Serialise charges of one (capability, ref): the second waits, then
		// finds the first's row. The unique index (052) is the backstop.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`,
			req.CapabilityID.String()+":"+req.ExternalRef); err != nil {
			return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge lock: %w", err)
		}
		var (
			chargeID uuid.UUID
			overrun  bool
		)
		err := tx.QueryRow(ctx, chargeByRefQuery, req.CapabilityID, req.ExternalRef).Scan(&chargeID, &overrun)
		if err == nil {
			return replay(ctx, tx, chargeID, req.CapabilityID, overrun)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge read ref: %w", err)
		}
	}

	receipt, err := s.chargeInTx(ctx, tx, req, uuid.Nil, resolvedUnit, amountNumeric, maxBudgetNumeric, onCharged)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge commit: %w", err)
	}
	return receipt, nil
}

// chargeInTx is Charge's body on a transaction the caller owns: the
// capability, ancestor and tenant counters, the ledger row and onCharged.
// The caller commits. Settle runs it after releasing a hold on the same tx,
// naming the reservation; a direct charge passes uuid.Nil.
//
// Each counter query refuses an amount past its ceiling by returning no row.
// Under OverrunRecord a refused one runs again with noCeiling and the receipt
// reports the overrun; a refusal returns no error from Postgres, so the
// transaction is still usable for the second run.
func (s *UsageStore) chargeInTx(
	ctx context.Context,
	tx pgx.Tx,
	req capability.ChargeRequest,
	reservationID uuid.UUID,
	resolvedUnit string,
	amountNumeric, maxBudgetNumeric pgtype.Numeric,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
) (capability.ChargeReceipt, error) {
	qtx := s.q.WithTx(tx)
	record := req.Overrun == capability.OverrunRecord

	overrun, err := addCopySpendPast(ctx, tx, req.CapabilityID, req.Copies, amountNumeric, copySpent, req.Overrun)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	spent, err := qtx.ChargeCapability(ctx, pgtype.UUID{Bytes: req.CapabilityID, Valid: true},
		amountNumeric, resolvedUnit, maxBudgetNumeric)
	if errors.Is(err, pgx.ErrNoRows) && record {
		overrun = true
		spent, err = qtx.ChargeCapability(ctx, pgtype.UUID{Bytes: req.CapabilityID, Valid: true},
			amountNumeric, resolvedUnit, noCeiling)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.ChargeReceipt{}, capability.ErrBudgetExceeded
		}
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge: %w", err)
	}

	ancestors, err := ancestorsOf(ctx, tx, req.CapabilityID)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	for _, a := range ancestors {
		_, err := qtx.ChargeCapability(ctx, pgtype.UUID{Bytes: a.id, Valid: true},
			amountNumeric, resolvedUnit, a.maxBudget)
		if errors.Is(err, pgx.ErrNoRows) && record {
			overrun = true
			_, err = qtx.ChargeCapability(ctx, pgtype.UUID{Bytes: a.id, Valid: true},
				amountNumeric, resolvedUnit, noCeiling)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return capability.ChargeReceipt{}, fmt.Errorf("%w: ancestor %s", capability.ErrBudgetExceeded, a.id)
			}
			return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge ancestor: %w", err)
		}
	}

	_, tErr := qtx.ChargeTenantBudget(ctx, pgtype.UUID{Bytes: req.TenantID, Valid: true},
		amountNumeric, resolvedUnit)
	if errors.Is(tErr, pgx.ErrNoRows) && record {
		overrun = true
		var tenantSpent pgtype.Numeric
		tErr = tx.QueryRow(ctx, chargeTenantPastCeilingQuery, req.TenantID, amountNumeric, resolvedUnit).Scan(&tenantSpent)
	}
	if tErr != nil {
		// Rollback (deferred) compensates every bump above — no explicit
		// refund needed now that all of them live on one tx.
		if errors.Is(tErr, pgx.ErrNoRows) {
			return capability.ChargeReceipt{}, capability.ErrTenantBudgetExceeded
		}
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge tenant: %w", tErr)
	}

	// Ledger row on the same tx — atomic with the counters.
	ledgerID := uuid.New()
	tag, lErr := tx.Exec(ctx,
		// tenant_slug is captured here rather than joined at read time: the
		// ledger has to stay readable after a tenant renames itself, and a
		// charge is a record of what was true when it happened. Resolved in
		// the same statement so it cannot drift from tenant_id.
		`INSERT INTO charges (id, tenant_id, tenant_slug, capability_id,
		                      amount, unit_code, op, actor_subject, copy_ids,
		                      external_ref, reservation_id, overrun)
		 SELECT $1, $2, t.slug, $3, $4::numeric, $5, $6, $7, $8::bytea[],
		        NULLIF($9::text, ''), $10::uuid, $11
		   FROM tenants t WHERE t.id = $2`,
		ledgerID, req.TenantID, req.CapabilityID, amountNumeric, resolvedUnit, req.Op, req.Actor, copyIDs(req.Copies),
		req.ExternalRef, nullableUUID(reservationID), overrun,
	)
	if lErr != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge ledger insert: %w", lErr)
	}
	if tag.RowsAffected() != 1 {
		// The INSERT ... SELECT found no tenant row. Committing would record
		// spend with no ledger entry — and hand back a charge ID no refund
		// could ever find.
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge ledger: tenant %s not found", req.TenantID)
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
			logger.FromContext(ctx).Warn("capability/postgres: charge rolled back on fan-out failure",
				zap.String("capability_id", req.CapabilityID.String()),
				zap.String("tenant_id", req.TenantID.String()),
				zap.Float64("amount", req.Amount),
				zap.Error(fErr),
			)
			return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge fan-out: %w", fErr)
		}
	}

	return capability.ChargeReceipt{ChargeID: ledgerID, Spent: floatFromNumeric(spent), Overrun: overrun}, nil
}

// nullableUUID is id for a uuid column, with uuid.Nil as NULL.
func nullableUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

// chargeRecordQuery reads a charge a capability took under an external ref,
// with what has been refunded from it.
const chargeRecordQuery = `
SELECT ch.id, ch.amount,
       COALESCE((SELECT sum(cr.amount) FROM charge_refunds cr WHERE cr.charge_id = ch.id), 0),
       ch.unit_code, ch.overrun
FROM   charges ch
WHERE  ch.capability_id = $1 AND ch.external_ref = $2
`

// ChargeByRef implements capability.Meter.
func (s *UsageStore) ChargeByRef(ctx context.Context, capID uuid.UUID, externalRef string) (capability.ChargeRecord, error) {
	if externalRef == "" {
		return capability.ChargeRecord{}, capability.ErrChargeNotFound
	}
	rec := capability.ChargeRecord{CapabilityID: capID, ExternalRef: externalRef}
	var amount, refunded pgtype.Numeric
	err := s.pool.QueryRow(ctx, chargeRecordQuery, capID, externalRef).
		Scan(&rec.ChargeID, &amount, &refunded, &rec.UnitCode, &rec.Overrun)
	if errors.Is(err, pgx.ErrNoRows) {
		return capability.ChargeRecord{}, capability.ErrChargeNotFound
	}
	if err != nil {
		return capability.ChargeRecord{}, fmt.Errorf("capability/postgres: charge by ref: %w", err)
	}
	rec.Amount, rec.Refunded = floatFromNumeric(amount), floatFromNumeric(refunded)
	return rec, nil
}

// refundableQuery reads what is left of a charge and resolves the amount to
// refund, all in numeric so a "refund the rest" is exact.
const refundableQuery = `
SELECT ch.capability_id,
       ch.tenant_id,
       ch.copy_ids,
       r.refund,
       r.refund > r.remaining AS exceeds
FROM   charges ch
CROSS  JOIN LATERAL (
    SELECT rem.remaining,
           CASE WHEN $2::numeric = 0 THEN rem.remaining ELSE $2::numeric END AS refund
    FROM (
        SELECT ch.amount - COALESCE(
                   (SELECT sum(cr.amount) FROM charge_refunds cr WHERE cr.charge_id = ch.id), 0
               ) AS remaining
    ) rem
) r
WHERE  ch.id = $1;
`

// Refund implements capability.Meter.
//
// Concurrent refunds of one charge are serialised on a transaction-scoped
// advisory lock keyed by the charge: charges is append-only by policy, so a
// row lock (SELECT ... FOR UPDATE, which needs an UPDATE policy) is not
// available, and without serialisation two refunds could each see the full
// remainder.
func (s *UsageStore) Refund(ctx context.Context, req capability.RefundRequest) (float64, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return 0, err
	}
	amountNumeric, err := numericFromFloat(req.Amount)
	if err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: refund begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.q.WithTx(tx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`,
		req.ChargeID.String()); err != nil {
		return 0, fmt.Errorf("capability/postgres: refund lock: %w", err)
	}

	var (
		capID, tenantID uuid.UUID
		copies          [][]byte
		refund          pgtype.Numeric
		exceeds         bool
	)
	if err := tx.QueryRow(ctx, refundableQuery, req.ChargeID, amountNumeric).
		Scan(&capID, &tenantID, &copies, &refund, &exceeds); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, capability.ErrChargeNotFound
		}
		return 0, fmt.Errorf("capability/postgres: refund read: %w", err)
	}
	if exceeds {
		return 0, fmt.Errorf("%w: charge %s", capability.ErrRefundExceedsCharge, req.ChargeID)
	}
	refunded := floatFromNumeric(refund)
	if refunded == 0 {
		return 0, nil // nothing left: a repeated full refund is a no-op
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO charge_refunds (charge_id, tenant_id, amount) VALUES ($1, $2, $3::numeric)`,
		req.ChargeID, tenantID, refund); err != nil {
		return 0, fmt.Errorf("capability/postgres: refund record: %w", err)
	}

	if err := subtractCopySpend(ctx, tx, copies, refund, copySpent); err != nil {
		return 0, err
	}
	ids := []uuid.UUID{capID}
	ancestors, err := ancestorsOf(ctx, tx, capID)
	if err != nil {
		return 0, err
	}
	for _, a := range ancestors {
		ids = append(ids, a.id)
	}
	for _, id := range ids {
		if err := qtx.RefundCapabilityUsage(ctx, pgtype.UUID{Bytes: id, Valid: true}, refund); err != nil {
			return 0, fmt.Errorf("capability/postgres: refund capability: %w", err)
		}
	}
	if err := qtx.RefundTenantBudget(ctx, pgtype.UUID{Bytes: tenantID, Valid: true}, refund); err != nil {
		return 0, fmt.Errorf("capability/postgres: refund tenant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("capability/postgres: refund commit: %w", err)
	}
	return refunded, nil
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
	got := tenantBudgetFromRow(row.TenantID, row.MaxBudgetUsd, row.SpentUsd, row.UnitCode, row.PeriodStart, row.PeriodEnd, row.UpdatedAt)
	got.ReservedAmount = floatFromNumeric(row.ReservedUsd)
	got.ResourceVersion = row.ResourceVersion
	return got, nil
}

// SetTenantBudget implements capability.UsageStore[pgx.Tx].
func (s *UsageStore) SetTenantBudget(ctx context.Context, args capability.SetTenantBudgetRequest) (capability.TenantBudget, error) {
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
		args.ExpectedVersion,
	)
	if err != nil {
		// No row back means the upsert's INSERT did not fire and its DO UPDATE's
		// version guard did not hold: the row exists and its version is not the
		// one the caller read.
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.TenantBudget{}, capability.ErrTenantBudgetVersionMismatch
		}
		return capability.TenantBudget{}, fmt.Errorf("capability/postgres: set tenant budget: %w", err)
	}
	out := tenantBudgetFromRow(row.TenantID, row.MaxBudgetUsd, row.SpentUsd, row.UnitCode, row.PeriodStart, row.PeriodEnd, row.UpdatedAt)
	out.ResourceVersion = row.ResourceVersion
	return out, nil
}

// ListTenantBudgets joins tenant_budgets with tenants and applies the
// threshold / unlimited / exclude_inactive filters server-side.
// Domain default for `limit` is 50; clamped to ≤ 500 to match the
// proto's bound. The sqlc query returns utilisation pre-computed so
// callers don't have to redo the division (and we keep the
// numeric-precision path inside Postgres where it belongs).
func (s *UsageStore) ListTenantBudgets(
	ctx context.Context,
	args capability.ListTenantBudgetsRequest,
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
func (s *UsageStore) GetUsage(ctx context.Context, capID uuid.UUID) (capability.Usage, error) {
	row, err := s.q.GetCapabilityUsage(ctx, pgtype.UUID{Bytes: capID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.Usage{}, capability.ErrUsageNotFound
		}
		return capability.Usage{}, fmt.Errorf("capability/postgres: get usage: %w", err)
	}
	return capability.Usage{
		CapabilityID:   uuid.UUID(row.CapabilityID.Bytes),
		RequestCount:   row.RequestCount,
		SpentAmount:    floatFromNumeric(row.SpentUsd),
		ReservedAmount: floatFromNumeric(row.ReservedUsd),
		UnitCode:       row.UnitCode,
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
// without surprises on edge values.
//
// NaN and ±Inf are refused here rather than "rejected upstream", which the
// comment used to claim and no code did. Both encode cleanly into a numeric,
// and both are silently wrong once stored: Postgres orders NaN above every
// other numeric, so a NaN cap makes `spent + amount <= cap` always true — a
// spend ceiling that reads as a number in the console and enforces nothing.
// The API cannot deliver one (buf.validate pins gte = 0, which NaN fails), so
// this guards the paths that do not go through it.
func numericFromFloat(v float64) (pgtype.Numeric, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return pgtype.Numeric{}, fmt.Errorf("capability/postgres: amount must be finite, got %v", v)
	}
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

// Compile-time conformance, instantiated over Paladin's actual transaction type.
// This is also the assertion that keeps FR-014 honest: Paladin satisfies the same
// published contract a third party would, with no privileged access.
var _ capability.UsageStore[pgx.Tx] = (*UsageStore)(nil)

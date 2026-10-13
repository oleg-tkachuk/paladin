package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgmoney"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// UsageStore implements limes.UsageStore[pgx.Tx] against the schema baseline (001_initial_schema.sql)'s
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
//     with delegated children, the whole subtree's. A NULL ceiling is
//     unlimited; 0 allows nothing (066).
//
// What the meter keeps outlives the capability's record (067): the ledger
// for good, the counters and holds until PurgeOrphans. The rows of a
// capability no longer on record are never written again — their row level
// security reads tenancy through the record — only released and dropped.
type UsageStore struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool // required — see NewUsageStore
	log  *zap.Logger   // never nil — defaults to zap.NewNop in NewUsageStore
	// now is the clock a reservation expires by.
	now func() time.Time
	// defaultUnit is what a tenant budget set with no unit is first written
	// in: the platform's currency (capability.currency).
	defaultUnit string
}

// UsageOption configures a UsageStore.
type UsageOption func(*UsageStore)

// WithDefaultUnit makes unit — the platform's currency, capability.currency —
// what a tenant budget set with no unit is first written in. Without it that
// is limes.DefaultUnitCode, the unit limes reads an unnamed one as.
func WithDefaultUnit(unit string) UsageOption {
	return func(s *UsageStore) { s.defaultUnit = unit }
}

// WithUsageClock expires reservations by now instead of the wall clock. For
// tests that move time; the server runs on the wall clock.
func WithUsageClock(now func() time.Time) UsageOption {
	return func(s *UsageStore) { s.now = now }
}

// NewUsageStore wires the sqlc-generated queries to the
// limes.UsageStore[pgx.Tx] interface.
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
func NewUsageStore(q *sqlc.Queries, pool *pgxpool.Pool, log *zap.Logger, opts ...UsageOption) *UsageStore {
	if pool == nil {
		panic("capability/postgres: NewUsageStore requires a non-nil pool")
	}
	if log == nil {
		log = zap.NewNop()
	}
	s := &UsageStore{q: q, pool: pool, log: log, now: time.Now, defaultUnit: limes.DefaultUnitCode}
	for _, o := range opts {
		o(s)
	}
	return s
}

// unlimitedCeiling is the ceiling argument the counter queries read as "no
// limit". An overrun under OverrunRecord re-runs a refused query with it, and
// a settle within its hold passes it to the counters that hold it.
var unlimitedCeiling = pgtype.Numeric{}

// maxNanosNumeric is the most a counter holds (limes.MaxNanos), as the
// columns' CHECKs bound it (055).
var maxNanosNumeric = pgmoney.NumericFromNanos(limes.MaxNanos)

// chargeTenantPastCeilingQuery adds to the tenant aggregate whatever its
// ceiling, as ChargeTenantBudget does short of the check: a cost already
// incurred, recorded under OverrunRecord, or a settle within its hold. $3 is
// the charge's unit, normalised by the caller.
const chargeTenantPastCeilingQuery = `
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start, updated_at)
VALUES ($1, NULL, $2::numeric, $3::text, $4::timestamptz, now())
ON CONFLICT (tenant_id) DO UPDATE
SET spent_usd  = tenant_budgets.spent_usd + $2::numeric,
    updated_at = now()
RETURNING spent_usd;
`

// chargeByRefQuery finds the charge a capability already took under an
// external ref, with what it charged: the same ref for another cost is a
// conflict, not a replay.
const chargeByRefQuery = `SELECT id, amount, unit_code, tenant_id FROM charges WHERE capability_id = $1 AND external_ref = $2`

// chargeByReservationQuery finds the charge that settled a reservation, with
// the capability and the copies it was charged under.
const chargeByReservationQuery = `SELECT id, capability_id, copy_ids FROM charges WHERE reservation_id = $1`

// chargeReplayQuery reads what a replayed receipt reports of a charge.
const chargeReplayQuery = `SELECT capability_id, tenant_id, unit_code, overrun FROM charges WHERE id = $1`

// spentQuery reads a capability's spend: zero for one with no counter.
const spentQuery = `SELECT COALESCE((SELECT spent_usd FROM capability_usage WHERE capability_id = $1), 0)`

// requestCountQuery reads a capability's request count: zero for one with no
// counter.
const requestCountQuery = `SELECT COALESCE((SELECT request_count FROM capability_usage WHERE capability_id = $1), 0)`

// advisoryLockQuery serialises a transaction on a key until it ends.
const advisoryLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`

// replay is the receipt for a charge already in the ledger, returned when the
// same charge arrives again: nothing moves, and SpentAmount is read as it is
// now.
func replay(ctx context.Context, tx pgx.Tx, chargeID uuid.UUID) (limes.ChargeReceipt, error) {
	receipt := limes.ChargeReceipt{ChargeID: chargeID, Replayed: true}
	var capID uuid.UUID
	if err := tx.QueryRow(ctx, chargeReplayQuery, chargeID).
		Scan(&capID, &receipt.TenantID, &receipt.UnitCode, &receipt.Overrun); err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: replay read charge: %w", err)
	}
	spent, err := readSpent(ctx, tx, capID)
	if err != nil {
		return limes.ChargeReceipt{}, err
	}
	receipt.SpentAmount = spent
	return receipt, nil
}

// querier is a pool or a transaction: what a read needs.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// readSpent is a capability's spend as it stands.
func readSpent(ctx context.Context, q querier, capID uuid.UUID) (limes.Nanos, error) {
	var spent pgtype.Numeric
	if err := q.QueryRow(ctx, spentQuery, capID).Scan(&spent); err != nil {
		return 0, fmt.Errorf("capability/postgres: read spend: %w", err)
	}
	return pgmoney.NanosFromNumeric(spent)
}

// ancestor is one link of a capability's delegation chain, with the ceilings
// its own record carries. The ceilings are read from capability_records, not
// taken from the caller: only the leaf's limits arrive in the verified token.
type ancestor struct {
	id uuid.UUID
	// maxBudget and maxRequests are the record's ceiling columns, NULL for
	// unlimited, passed to the counter queries as they are.
	maxBudget   pgtype.Numeric
	maxRequests *int64
}

// lineageQuery walks parent_id upwards from a capability, the capability
// itself at depth 0 and its ancestors nearest first, one past
// MaxDelegationDepth so a chain deeper than that is told apart. It runs on
// the metering transaction, whose connection is scoped to the capability's
// tenant; delegation never crosses tenants, so the whole chain is visible
// under that scope. The ceilings come from their own columns, never from the
// caveats JSON.
const lineageQuery = `
WITH RECURSIVE chain(id, parent_id, max_budget, max_requests, depth) AS (
    SELECT id, parent_id, max_budget, max_requests, 0 FROM capability_records WHERE id = $1
    UNION ALL
    SELECT r.id, r.parent_id, r.max_budget, r.max_requests, c.depth + 1
    FROM   capability_records r
    JOIN   chain c ON r.id = c.parent_id
    WHERE  c.depth <= $2
)
SELECT id, max_budget, max_requests, depth
FROM   chain
ORDER  BY depth;
`

// lineage returns the ancestors whose counters move with capID's, nearest
// first, stopping at the first one no longer on record. limes.ErrNotFound
// when capID itself is not on record: with no record its ancestors cannot be
// read, and metering it alone would let it spend past every live ancestor's
// ceiling. A chain with more than MaxDelegationDepth ancestors on record is
// limes.ErrDelegationTooDeep.
func lineage(ctx context.Context, tx pgx.Tx, capID uuid.UUID) ([]ancestor, error) {
	rows, err := tx.Query(ctx, lineageQuery, capID, limes.MaxDelegationDepth)
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: lineage: %w", err)
	}
	defer rows.Close()
	found := false
	var out []ancestor
	for rows.Next() {
		var (
			a     ancestor
			depth int
		)
		if err := rows.Scan(&a.id, &a.maxBudget, &a.maxRequests, &depth); err != nil {
			return nil, fmt.Errorf("capability/postgres: lineage scan: %w", err)
		}
		switch {
		case depth == 0:
			found = true
		case depth > limes.MaxDelegationDepth:
			return nil, fmt.Errorf("%w: %s has more than %d ancestors", limes.ErrDelegationTooDeep, capID, limes.MaxDelegationDepth)
		default:
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("capability/postgres: lineage: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("%w: capability/postgres: capability %s is not on record", limes.ErrNotFound, capID)
	}
	return out, nil
}

// ancestorIDs lists the ancestors' ids, nearest first, as a ledger row or a
// reservation keeps them.
func ancestorIDs(ancestors []ancestor) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(ancestors))
	for _, a := range ancestors {
		ids = append(ids, a.id)
	}
	return ids
}

// checkSubject refuses a subject no verifier or record made: the zero
// MeterSubject names no capability and no tenant to count against.
func checkSubject(s limes.MeterSubject) error {
	if s.CapabilityID() == uuid.Nil || s.TenantID() == uuid.Nil {
		return fmt.Errorf("%w: capability/postgres: the subject names no capability or no tenant", limes.ErrInvalidRequest)
	}
	return nil
}

// unitConflictQuery finds a counter a charge or hold in unit $2 would move
// that counts in another unit: a capability's ($1) that already holds spend
// or a hold, or the tenant's ($3) once it has a row. Summing the two would
// add two currencies as one.
const unitConflictQuery = `
SELECT 'capability ' || capability_id::text, unit_code
FROM   capability_usage
WHERE  capability_id = ANY($1::uuid[]) AND unit_code <> $2 AND (spent_usd <> 0 OR reserved_usd <> 0)
UNION ALL
SELECT 'tenant ' || tenant_id::text, unit_code
FROM   tenant_budgets
WHERE  tenant_id = $3 AND unit_code <> $2
LIMIT  1;
`

// claimUnitQuery moves the capability counters that hold nothing yet to the
// unit of their first charge or hold; unitConflictQuery has ruled out any
// that hold something in another.
const claimUnitQuery = `UPDATE capability_usage SET unit_code = $2 WHERE capability_id = ANY($1::uuid[]) AND unit_code <> $2`

// checkUnits refuses a charge or hold in unit on a counter that counts in
// another, and gives the empty counters of the chain that unit.
func checkUnits(ctx context.Context, tx pgx.Tx, capID uuid.UUID, ancestors []ancestor, tenantID uuid.UUID, unit string) error {
	ids := append([]uuid.UUID{capID}, ancestorIDs(ancestors)...)
	var counter, have string
	err := tx.QueryRow(ctx, unitConflictQuery, ids, unit, tenantID).Scan(&counter, &have)
	if err == nil {
		return fmt.Errorf("%w: %s counts in %s, not %s", limes.ErrUnitCodeMismatch, counter, have, unit)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("capability/postgres: unit check: %w", err)
	}
	if _, err := tx.Exec(ctx, claimUnitQuery, ids, unit); err != nil {
		return fmt.Errorf("capability/postgres: unit claim: %w", err)
	}
	return nil
}

// rangeQuery finds a counter that adding $2 would take past MaxNanos ($3): a
// capability's ($1), a copy's ($4) or the tenant's ($5), spend and holds
// together.
const rangeQuery = `
SELECT EXISTS (
    SELECT 1 FROM capability_usage
    WHERE  capability_id = ANY($1::uuid[]) AND spent_usd + reserved_usd > $3::numeric - $2::numeric
    UNION ALL
    SELECT 1 FROM capability_copy_usage
    WHERE  revocation_id = ANY($4::bytea[]) AND spent + reserved > $3::numeric - $2::numeric
    UNION ALL
    SELECT 1 FROM tenant_budgets
    WHERE  tenant_id = $5 AND spent_usd + reserved_usd > $3::numeric - $2::numeric
);
`

// checkRange keeps every counter an amount would move within MaxNanos, the
// range a column holds. Unlike a ceiling it is not an overrun to record: past
// it the amount could not be counted at all. It runs before any ceiling, so a
// counter with no room is told apart from one at its ceiling.
func checkRange(ctx context.Context, tx pgx.Tx, capIDs []uuid.UUID, copies [][]byte, tenantID uuid.UUID, amount pgtype.Numeric) error {
	var overflows bool
	if err := tx.QueryRow(ctx, rangeQuery, capIDs, amount, maxNanosNumeric, copies, tenantID).Scan(&overflows); err != nil {
		return fmt.Errorf("capability/postgres: range check: %w", err)
	}
	if overflows {
		return fmt.Errorf("%w: capability/postgres: a counter has no room for the amount", limes.ErrCounterOverflow)
	}
	return nil
}

// counterError maps a counter write the money CHECKs refused (055) to
// ErrCounterOverflow — checkRange answers first, so this is the backstop —
// and wraps anything else with what was being written.
func counterError(what string, err error) error {
	if pgerr.Is(err, pgerr.CheckViolation) {
		return fmt.Errorf("%w: capability/postgres: %s: %w", limes.ErrCounterOverflow, what, err)
	}
	return fmt.Errorf("capability/postgres: %s: %w", what, err)
}

// requestCount is a capability's request count as it stands, for the receipt
// of a refused bump.
func (s *UsageStore) requestCount(ctx context.Context, capID uuid.UUID) int64 {
	var n int64
	if err := s.pool.QueryRow(ctx, requestCountQuery, capID).Scan(&n); err != nil {
		s.log.Warn("capability/postgres: read request count", zap.Error(err))
	}
	return n
}

// Bump implements limes.Meter. The capability and every ancestor are bumped
// in one transaction, copies first, then the leaf and then nearest ancestor
// first — the same order every charge takes, so two requests sharing part of
// a chain lock it in the same order and cannot deadlock.
func (s *UsageStore) Bump(ctx context.Context, req limes.BumpRequest) (limes.BumpReceipt, error) {
	if err := checkSubject(req.Subject); err != nil {
		return limes.BumpReceipt{}, err
	}
	capID := req.Subject.CapabilityID()
	count, err := s.bump(ctx, req.Subject)
	if errors.Is(err, limes.ErrRequestLimitExceeded) {
		// A refusal moved nothing; the receipt says where the count stands.
		return limes.BumpReceipt{RequestCount: s.requestCount(ctx, capID)}, err
	}
	if err != nil {
		return limes.BumpReceipt{}, err
	}
	return limes.BumpReceipt{RequestCount: count}, nil
}

func (s *UsageStore) bump(ctx context.Context, subject limes.MeterSubject) (int64, error) {
	capID := subject.CapabilityID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: bump begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	qtx := s.q.WithTx(tx)

	ancestors, err := lineage(ctx, tx, capID)
	if err != nil {
		return 0, err
	}
	if err := bumpCopies(ctx, tx, capID, subject.Copies()); err != nil {
		return 0, err
	}
	count, err := qtx.BumpCapabilityRequestCount(ctx, pgtype.UUID{Bytes: capID, Valid: true},
		subject.UnitCode(), requestCeilingColumn(subject.Requests()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, limes.ErrRequestLimitExceeded
		}
		return 0, fmt.Errorf("capability/postgres: bump: %w", err)
	}
	for _, a := range ancestors {
		if _, err := qtx.BumpCapabilityRequestCount(ctx, pgtype.UUID{Bytes: a.id, Valid: true}, subject.UnitCode(), a.maxRequests); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, fmt.Errorf("%w: ancestor %s", limes.ErrRequestLimitExceeded, a.id)
			}
			return 0, fmt.Errorf("capability/postgres: bump ancestor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("capability/postgres: bump commit: %w", err)
	}
	return count, nil
}

// Charge implements limes.Meter.
//
// Everything runs in ONE transaction so the charge is atomic
// (ADR-0003 transactional outbox — no dual-write window):
//
//  1. Charge each Biscuit copy's counter, against the copy's own budget.
//  2. Charge the capability counter (against the token's ceiling).
//  3. Charge each ancestor's counter (against the ceiling on its record),
//     so a parent's budget bounds everything delegated from it.
//  4. Charge the tenant aggregate (against the tenant ceiling).
//  5. Write the charges-ledger row and run onCharged so the event producer
//     enqueues its outbox rows on the same tx.
//  6. Commit. Any rejection or error rolls the whole thing back, so the
//     counters can never drift and the ledger row + fan-out rows are never
//     orphaned from the spend they describe.
func (s *UsageStore) Charge(
	ctx context.Context,
	req limes.ChargeRequest,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
) (limes.ChargeReceipt, error) {
	if err := req.Amount.Validate(); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := req.Overrun.Validate(); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := limes.ValidateExternalRef(req.ExternalRef); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := checkSubject(req.Subject); err != nil {
		return limes.ChargeReceipt{}, err
	}
	unit, err := limes.NormalizeUnitCode(req.Subject.UnitCode())
	if err != nil {
		return limes.ChargeReceipt{}, err
	}
	capID, tenantID := req.Subject.CapabilityID(), req.Subject.TenantID()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit

	// A replay is answered before the lineage is read: it moves nothing, so
	// it needs no record, and a capability purged since still gets its
	// receipt, as a settled hold does.
	if req.ExternalRef != "" {
		// Serialise charges of one (capability, ref): the second waits, then
		// finds the first's row. The unique index (052) is the backstop.
		if _, err := tx.Exec(ctx, advisoryLockQuery, capID.String()+":"+req.ExternalRef); err != nil {
			return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge lock: %w", err)
		}
		var (
			chargeID, chargedTenant uuid.UUID
			charged                 pgtype.Numeric
			chargedUnit             string
		)
		err := tx.QueryRow(ctx, chargeByRefQuery, capID, req.ExternalRef).Scan(&chargeID, &charged, &chargedUnit, &chargedTenant)
		if err == nil {
			chargedNanos, err := pgmoney.NanosFromNumeric(charged)
			if err != nil {
				return limes.ChargeReceipt{}, err
			}
			if chargedNanos != req.Amount || chargedUnit != unit || chargedTenant != tenantID {
				return limes.ChargeReceipt{}, fmt.Errorf("%w: %q took %v %s for tenant %s as charge %s",
					limes.ErrExternalRefConflict, req.ExternalRef, chargedNanos, chargedUnit, chargedTenant, chargeID)
			}
			return replay(ctx, tx, chargeID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge read ref: %w", err)
		}
	}

	ancestors, err := lineage(ctx, tx, capID)
	if err != nil {
		return limes.ChargeReceipt{}, err
	}
	receipt, err := s.chargeInTx(ctx, tx, chargeArgs{
		subject: req.Subject, unit: unit, amount: req.Amount, op: req.Op, actor: req.Actor,
		externalRef: req.ExternalRef, overrun: req.Overrun, ancestors: ancestors,
	}, onCharged)
	if err != nil {
		// Give the connection back before reading the spend on another: a
		// burst of refusals each holding one while waiting for a second would
		// drain the pool and wait on itself.
		_ = tx.Rollback(ctx)
		return s.refusedReceipt(ctx, capID, tenantID, unit), err
	}
	if err := tx.Commit(ctx); err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge commit: %w", err)
	}
	return receipt, nil
}

// refusedReceipt is the receipt of a refused charge: whose charge it
// refused, in what unit, and the spend as it stands, which the refusal left
// alone.
func (s *UsageStore) refusedReceipt(ctx context.Context, capID, tenantID uuid.UUID, unit string) limes.ChargeReceipt {
	spent, err := readSpent(ctx, s.pool, capID)
	if err != nil {
		s.log.Warn("capability/postgres: read spend of a refused charge", zap.Error(err))
	}
	return limes.ChargeReceipt{SpentAmount: spent, TenantID: tenantID, UnitCode: unit}
}

// chargeArgs is one charge chargeInTx makes.
type chargeArgs struct {
	subject     limes.MeterSubject
	unit        string // normalised
	amount      limes.Nanos
	op          limes.Op
	actor       string
	externalRef string
	overrun     limes.OverrunPolicy
	// ancestors is the lineage the charge is spent on, as it stands.
	ancestors []ancestor
	// settling is the hold the charge settles; nil for a direct charge.
	settling *openReservation
	// reservationID is the hold settled; uuid.Nil for a direct charge.
	reservationID uuid.UUID
}

// chargeInTx is Charge's body on a transaction the caller owns: the copy,
// capability, ancestor and tenant counters, the ledger row and onCharged.
// The caller commits. Settle runs it after releasing the hold on the same tx.
//
// Each counter query refuses an amount past its ceiling by returning no row.
// Under OverrunRecord a refused one runs again with unlimitedCeiling and the
// receipt reports the overrun; a refusal returns no error from Postgres, so
// the transaction is still usable for the second run.
//
// A settle at or below its hold adds nothing to a counter that held it, so no
// ceiling there can refuse it — not even one crossed since the hold was made.
// Only the excess must fit. An ancestor that did not hold it takes the whole
// amount against its ceiling.
func (s *UsageStore) chargeInTx(
	ctx context.Context,
	tx pgx.Tx,
	args chargeArgs,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
) (limes.ChargeReceipt, error) {
	qtx := s.q.WithTx(tx)
	record := args.overrun == limes.OverrunRecord
	capID, tenantID := args.subject.CapabilityID(), args.subject.TenantID()
	copies := args.subject.Copies()
	amount := pgmoney.NumericFromNanos(args.amount)
	now := pgtype.Timestamptz{Time: s.now(), Valid: true}

	withinHold := args.settling != nil && args.amount <= args.settling.amountNanos
	heldBy := map[uuid.UUID]bool{}
	if withinHold {
		for _, id := range args.settling.ancestorIDs {
			heldBy[id] = true
		}
	}

	if err := checkUnits(ctx, tx, capID, args.ancestors, tenantID, args.unit); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := checkRange(ctx, tx, append([]uuid.UUID{capID}, ancestorIDs(args.ancestors)...),
		copyIDs(copies), tenantID, amount); err != nil {
		return limes.ChargeReceipt{}, err
	}
	overrun, err := addCopySpend(ctx, tx, copySpendArgs{
		capID: capID, copies: copies, amount: amount, counter: copySpent,
		overrun: args.overrun, ceilingsLifted: withinHold,
	})
	if err != nil {
		return limes.ChargeReceipt{}, err
	}

	leafCeiling := pgmoney.NumericFromCeiling(args.subject.Budget())
	if withinHold {
		leafCeiling = unlimitedCeiling
	}
	capUUID := pgtype.UUID{Bytes: capID, Valid: true}
	spent, err := qtx.ChargeCapability(ctx, capUUID, amount, args.unit, leafCeiling)
	if errors.Is(err, pgx.ErrNoRows) && record {
		overrun = true
		spent, err = qtx.ChargeCapability(ctx, capUUID, amount, args.unit, unlimitedCeiling)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.ChargeReceipt{}, limes.ErrBudgetExceeded
		}
		return limes.ChargeReceipt{}, counterError("charge", err)
	}

	for _, a := range args.ancestors {
		ceiling := a.maxBudget
		if heldBy[a.id] {
			ceiling = unlimitedCeiling
		}
		aUUID := pgtype.UUID{Bytes: a.id, Valid: true}
		_, err := qtx.ChargeCapability(ctx, aUUID, amount, args.unit, ceiling)
		if errors.Is(err, pgx.ErrNoRows) && record {
			overrun = true
			_, err = qtx.ChargeCapability(ctx, aUUID, amount, args.unit, unlimitedCeiling)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return limes.ChargeReceipt{}, fmt.Errorf("%w: ancestor %s", limes.ErrBudgetExceeded, a.id)
			}
			return limes.ChargeReceipt{}, counterError("charge ancestor", err)
		}
	}

	var tenantSpent pgtype.Numeric
	var tErr error
	if withinHold {
		tErr = tx.QueryRow(ctx, chargeTenantPastCeilingQuery, tenantID, amount, args.unit, now).Scan(&tenantSpent)
	} else {
		_, tErr = qtx.ChargeTenantBudget(ctx, pgtype.UUID{Bytes: tenantID, Valid: true}, amount, args.unit, now)
		if errors.Is(tErr, pgx.ErrNoRows) && record {
			overrun = true
			tErr = tx.QueryRow(ctx, chargeTenantPastCeilingQuery, tenantID, amount, args.unit, now).Scan(&tenantSpent)
		}
	}
	if tErr != nil {
		// Rollback (deferred) compensates every bump above — no explicit
		// refund needed now that all of them live on one tx.
		if errors.Is(tErr, pgx.ErrNoRows) {
			return limes.ChargeReceipt{}, limes.ErrTenantBudgetExceeded
		}
		return limes.ChargeReceipt{}, counterError("charge tenant", tErr)
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
		                      external_ref, reservation_id, overrun, ancestor_ids, occurred_at,
		                      tenant_period)
		 SELECT $1, $2, t.slug, $3, $4::numeric, $5, $6, $7, $8::bytea[],
		        NULLIF($9::text, ''), $10::uuid, $11, $12::uuid[], $13::timestamptz,
		        (SELECT tb.period FROM tenant_budgets tb WHERE tb.tenant_id = $2)
		   FROM tenants t WHERE t.id = $2`,
		ledgerID, tenantID, capID, amount, args.unit, args.op, args.actor, copyIDs(copies),
		args.externalRef, nullableUUID(args.reservationID), overrun, ancestorIDs(args.ancestors), now,
	)
	if lErr != nil {
		return limes.ChargeReceipt{}, counterError("charge ledger insert", lErr)
	}
	if tag.RowsAffected() != 1 {
		// The INSERT ... SELECT found no tenant row. Committing would record
		// spend with no ledger entry — and hand back a charge ID no refund
		// could ever find.
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge ledger: tenant %s not found", tenantID)
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
				zap.String("capability_id", capID.String()),
				zap.String("tenant_id", tenantID.String()),
				zap.Stringer("amount", args.amount),
				zap.Error(fErr),
			)
			return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: charge fan-out: %w", fErr)
		}
	}

	spentNanos, err := pgmoney.NanosFromNumeric(spent)
	if err != nil {
		return limes.ChargeReceipt{}, err
	}
	return limes.ChargeReceipt{
		ChargeID: ledgerID, SpentAmount: spentNanos, Overrun: overrun,
		TenantID: tenantID, UnitCode: args.unit,
	}, nil
}

// nullableUUID is id for a uuid column, with uuid.Nil as NULL.
func nullableUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

// chargeRecordSelect reads charges as the Meter contract reads one back,
// with what has been refunded from each; a WHERE clause picks which.
const chargeRecordSelect = `
SELECT ch.id, ch.capability_id, ch.tenant_id, ch.amount,
       COALESCE((SELECT sum(cr.amount) FROM charge_refunds cr WHERE cr.charge_id = ch.id), 0),
       ch.unit_code, COALESCE(ch.external_ref, ''), ch.overrun, ch.op, ch.actor_subject, ch.occurred_at
FROM   charges ch
`

// readCharge runs chargeRecordSelect with where and its args.
func (s *UsageStore) readCharge(ctx context.Context, where string, args ...any) (limes.ChargeRecord, error) {
	var (
		rec              limes.ChargeRecord
		amount, refunded pgtype.Numeric
	)
	err := s.pool.QueryRow(ctx, chargeRecordSelect+where, args...).
		Scan(&rec.ID, &rec.CapabilityID, &rec.TenantID, &amount, &refunded, &rec.UnitCode, &rec.ExternalRef,
			&rec.Overrun, &rec.Op, &rec.Actor, &rec.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return limes.ChargeRecord{}, limes.ErrChargeNotFound
	}
	if err != nil {
		return limes.ChargeRecord{}, fmt.Errorf("capability/postgres: read charge: %w", err)
	}
	rec.At = rec.At.UTC()
	if rec.Amount, err = pgmoney.NanosFromNumeric(amount); err != nil {
		return limes.ChargeRecord{}, err
	}
	if rec.RefundedAmount, err = pgmoney.NanosFromNumeric(refunded); err != nil {
		return limes.ChargeRecord{}, err
	}
	return rec, nil
}

// GetCharge implements limes.MeterReader.
func (s *UsageStore) GetCharge(ctx context.Context, chargeID uuid.UUID) (limes.ChargeRecord, error) {
	return s.readCharge(ctx, `WHERE ch.id = $1`, chargeID)
}

// ChargeByRef implements limes.MeterReader.
func (s *UsageStore) ChargeByRef(ctx context.Context, req limes.ChargeByRefRequest) (limes.ChargeRecord, error) {
	if req.ExternalRef == "" {
		return limes.ChargeRecord{}, limes.ErrChargeNotFound
	}
	return s.readCharge(ctx, `WHERE ch.capability_id = $1 AND ch.external_ref = $2`, req.CapabilityID, req.ExternalRef)
}

// refundableQuery reads what is left of a charge and resolves the amount to
// refund — the rest when $3 asks for it, $2 otherwise — all in numeric so a
// "refund the rest" is exact.
const refundableQuery = `
SELECT ch.capability_id,
       ch.tenant_id,
       ch.copy_ids,
       ch.ancestor_ids,
       ch.unit_code,
       r.refund,
       r.refund > r.remaining AS exceeds,
       -- Counted in the tenant's current period: the period it recorded,
       -- or, for a charge from before periods were counted (069), charged
       -- since the period's start. A charge of a period since closed is not
       -- in today's spend.
       CASE WHEN ch.tenant_period IS NOT NULL THEN ch.tenant_period = tb.period
            ELSE tb.period_start IS NULL OR ch.occurred_at >= tb.period_start
       END AS in_period
FROM   charges ch
LEFT   JOIN tenant_budgets tb ON tb.tenant_id = ch.tenant_id
CROSS  JOIN LATERAL (
    SELECT rem.remaining,
           CASE WHEN $3::boolean THEN rem.remaining ELSE $2::numeric END AS refund
    FROM (
        SELECT ch.amount - COALESCE(
                   (SELECT sum(cr.amount) FROM charge_refunds cr WHERE cr.charge_id = ch.id), 0
               ) AS remaining
    ) rem
) r
WHERE  ch.id = $1;
`

// returnSpendQuery takes a refund off the spend of the capabilities named
// that are still on record, never below zero. A capability no longer on
// record has nothing to give back: its counter only waits for PurgeOrphans.
const returnSpendQuery = `
UPDATE capability_usage
SET    spent_usd = GREATEST(0, spent_usd - $2::numeric), updated_at = now()
WHERE  capability_id = ANY($1::uuid[])
  AND  capability_id IN (SELECT id FROM capability_records WHERE id = ANY($1::uuid[]))
`

// Refund implements limes.Meter.
//
// Concurrent refunds of one charge are serialised on a transaction-scoped
// advisory lock keyed by the charge: charges is append-only by policy, so a
// row lock (SELECT ... FOR UPDATE, which needs an UPDATE policy) is not
// available, and without serialisation two refunds could each see the full
// remainder.
func (s *UsageStore) Refund(ctx context.Context, req limes.RefundRequest) (limes.RefundReceipt, error) {
	if err := req.Validate(); err != nil {
		return limes.RefundReceipt{}, err
	}
	amountNumeric := pgmoney.NumericFromNanos(req.Amount)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.q.WithTx(tx)

	if _, err := tx.Exec(ctx, advisoryLockQuery, req.ChargeID.String()); err != nil {
		return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund lock: %w", err)
	}

	var (
		capID, tenantID uuid.UUID
		copies          [][]byte
		ancestors       []uuid.UUID
		unitCode        string
		refund          pgtype.Numeric
		exceeds         bool
		inPeriod        bool
	)
	if err := tx.QueryRow(ctx, refundableQuery, req.ChargeID, amountNumeric, req.Remaining).
		Scan(&capID, &tenantID, &copies, &ancestors, &unitCode, &refund, &exceeds, &inPeriod); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.RefundReceipt{}, limes.ErrChargeNotFound
		}
		return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund read: %w", err)
	}
	if exceeds {
		return limes.RefundReceipt{}, fmt.Errorf("%w: charge %s", limes.ErrRefundExceedsCharge, req.ChargeID)
	}
	refunded, err := pgmoney.NanosFromNumeric(refund)
	if err != nil {
		return limes.RefundReceipt{}, err
	}
	receipt := limes.RefundReceipt{ChargeID: req.ChargeID, TenantID: tenantID, UnitCode: unitCode}
	if refunded == 0 {
		return receipt, nil // nothing left: a repeated full refund is a no-op
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO charge_refunds (charge_id, tenant_id, amount) VALUES ($1, $2, $3::numeric)`,
		req.ChargeID, tenantID, refund); err != nil {
		return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund record: %w", err)
	}

	if err := subtractCopySpend(ctx, tx, copies, refund, copySpent); err != nil {
		return limes.RefundReceipt{}, err
	}
	if ancestors == nil {
		// A charge written before 067 names no ancestors; its lineage is read
		// from the records, as far as they still reach.
		if ancestors, err = recordedAncestors(ctx, tx, capID); err != nil {
			return limes.RefundReceipt{}, err
		}
	}
	if _, err := tx.Exec(ctx, returnSpendQuery, append([]uuid.UUID{capID}, ancestors...), refund); err != nil {
		return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund capability: %w", err)
	}
	if inPeriod {
		if err := qtx.RefundTenantBudget(ctx, pgtype.UUID{Bytes: tenantID, Valid: true}, refund); err != nil {
			return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund tenant: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return limes.RefundReceipt{}, fmt.Errorf("capability/postgres: refund commit: %w", err)
	}
	receipt.Amount = refunded
	return receipt, nil
}

// recordedAncestors is capID's lineage as the records still hold it: none
// once capID itself is gone.
func recordedAncestors(ctx context.Context, tx pgx.Tx, capID uuid.UUID) ([]uuid.UUID, error) {
	ancestors, err := lineage(ctx, tx, capID)
	if errors.Is(err, limes.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ancestorIDs(ancestors), nil
}

// checkTenantUnit refuses to re-denominate a tenant that still counts
// something in its unit: holds, always, and spend unless the call resets it.
// The version guard on the write catches a change in between.
func (s *UsageStore) checkTenantUnit(ctx context.Context, args limes.SetTenantBudgetRequest, unit string) error {
	if unit == "" {
		return nil // keeps the row's unit
	}
	current, err := s.GetTenantBudget(ctx, args.TenantID)
	if errors.Is(err, limes.ErrTenantBudgetNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.UnitCode != unit &&
		(current.ReservedAmount > 0 || (current.SpentAmount > 0 && !args.ResetSpend)) {
		return fmt.Errorf("%w: tenant %s has %v counted in %s",
			limes.ErrUnitCodeMismatch, args.TenantID, current.SpentAmount+current.ReservedAmount, current.UnitCode)
	}
	return nil
}

// GetTenantBudget implements limes.TenantBudgets.
func (s *UsageStore) GetTenantBudget(ctx context.Context, tenantID uuid.UUID) (limes.TenantBudget, error) {
	row, err := s.q.GetTenantBudget(ctx, pgtype.UUID{Bytes: tenantID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.TenantBudget{}, limes.ErrTenantBudgetNotFound
		}
		return limes.TenantBudget{}, fmt.Errorf("capability/postgres: get tenant budget: %w", err)
	}
	return tenantBudgetFromRow(row.TenantID, row.MaxBudgetUsd, row.SpentUsd, row.ReservedUsd, row.UnitCode,
		row.PeriodStart, row.PeriodEnd, row.UpdatedAt, row.ResourceVersion)
}

// SetTenantBudget implements limes.TenantBudgets.
func (s *UsageStore) SetTenantBudget(ctx context.Context, args limes.SetTenantBudgetRequest) (limes.TenantBudget, error) {
	if err := args.Validate(); err != nil {
		return limes.TenantBudget{}, err
	}
	// Empty UnitCode passed through verbatim — the SQL preserves the existing
	// row's unit_code on conflict in that case, and writes the platform's
	// currency (defaultUnit) on the first row. A non-empty one is normalised;
	// Validate refused one that is not a unit.
	unit := args.UnitCode
	if unit != "" {
		normalised, err := limes.NormalizeUnitCode(unit)
		if err != nil {
			return limes.TenantBudget{}, err
		}
		unit = normalised
	}
	if err := s.checkTenantUnit(ctx, args, unit); err != nil {
		return limes.TenantBudget{}, err
	}
	var periodEnd pgtype.Timestamptz
	if !args.PeriodEnd.IsZero() {
		periodEnd = pgtype.Timestamptz{Time: args.PeriodEnd, Valid: true}
	}
	row, err := s.q.SetTenantBudget(
		ctx,
		pgtype.UUID{Bytes: args.TenantID, Valid: true},
		pgmoney.NumericFromCeiling(args.Ceiling),
		unit,
		s.defaultUnit,
		pgtype.Timestamptz{Time: s.now(), Valid: true},
		periodEnd,
		args.ExpectedVersion,
		args.ResetSpend,
	)
	if err != nil {
		// No row back means neither the guarded INSERT nor the DO UPDATE's
		// version guard held: the version is not the one the caller read —
		// a missing row is version 0.
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.TenantBudget{}, limes.ErrTenantBudgetVersionMismatch
		}
		return limes.TenantBudget{}, fmt.Errorf("capability/postgres: set tenant budget: %w", err)
	}
	return tenantBudgetFromRow(row.TenantID, row.MaxBudgetUsd, row.SpentUsd, row.ReservedUsd, row.UnitCode,
		row.PeriodStart, row.PeriodEnd, row.UpdatedAt, row.ResourceVersion)
}

// ListTenantBudgets implements limes.TenantBudgets with the ListTenantBudgets
// query, which orders and filters in Postgres, exactly.
func (s *UsageStore) ListTenantBudgets(
	ctx context.Context,
	args limes.ListTenantBudgetsRequest,
) (limes.Page[limes.TenantBudget], error) {
	if err := args.Validate(); err != nil {
		return limes.Page[limes.TenantBudget]{}, err
	}
	limit := limes.PageLimit(args.Limit)
	threshold, err := numericFromFloat(args.ThresholdPct)
	if err != nil {
		return limes.Page[limes.TenantBudget]{}, fmt.Errorf("capability/postgres: threshold_pct: %w", err)
	}
	var (
		afterClass  *int32
		afterKey    pgtype.Numeric
		afterTenant pgtype.UUID
	)
	if args.Cursor != "" {
		c, err := decodeTenantBudgetCursor(args.Cursor)
		if err != nil {
			return limes.Page[limes.TenantBudget]{}, err
		}
		if afterKey, err = c.keyNumeric(); err != nil {
			return limes.Page[limes.TenantBudget]{}, err
		}
		afterClass, afterTenant = &c.class, pgtype.UUID{Bytes: c.tenantID, Valid: true}
	}
	rows, err := s.q.ListTenantBudgets(ctx,
		args.Filter == limes.TenantBudgetsAtOrPast,
		threshold,
		args.Filter == limes.TenantBudgetsUnlimited,
		afterClass,
		afterKey,
		afterTenant,
		int32(limit+1), // one past the page says whether another follows
	)
	if err != nil {
		return limes.Page[limes.TenantBudget]{}, fmt.Errorf("capability/postgres: list tenant budgets: %w", err)
	}
	page := limes.Page[limes.TenantBudget]{Items: make([]limes.TenantBudget, 0, min(len(rows), limit))}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		page.Next = tenantBudgetCursor{class: last.CeilingClass, key: last.RankKey, tenantID: uuid.UUID(last.TenantID.Bytes)}.encode()
	}
	for _, r := range rows {
		budget, err := tenantBudgetFromRow(
			r.TenantID, r.MaxBudgetUsd, r.SpentUsd, r.ReservedUsd, r.UnitCode,
			r.PeriodStart, r.PeriodEnd, r.UpdatedAt, r.ResourceVersion,
		)
		if err != nil {
			return limes.Page[limes.TenantBudget]{}, err
		}
		page.Items = append(page.Items, budget)
	}
	return page, nil
}

// tenantBudgetFromRow normalises sqlc row types into the public shape.
func tenantBudgetFromRow(
	tenantID pgtype.UUID,
	maxBudget, spent, reserved pgtype.Numeric,
	unitCode string,
	periodStart, periodEnd, updatedAt pgtype.Timestamptz,
	resourceVersion int64,
) (limes.TenantBudget, error) {
	out := limes.TenantBudget{
		TenantID:        uuid.UUID(tenantID.Bytes),
		UnitCode:        unitCode,
		ResourceVersion: resourceVersion,
	}
	var err error
	if out.Ceiling, err = pgmoney.CeilingFromNumeric(maxBudget); err != nil {
		return limes.TenantBudget{}, err
	}
	if out.SpentAmount, err = pgmoney.NanosFromNumeric(spent); err != nil {
		return limes.TenantBudget{}, err
	}
	if out.ReservedAmount, err = pgmoney.NanosFromNumeric(reserved); err != nil {
		return limes.TenantBudget{}, err
	}
	if periodStart.Valid {
		out.PeriodStart = periodStart.Time.UTC()
	}
	if periodEnd.Valid {
		out.PeriodEnd = periodEnd.Time.UTC()
	}
	if updatedAt.Valid {
		out.UpdatedAt = updatedAt.Time.UTC()
	}
	return out, nil
}

// GetUsage implements limes.MeterReader.
func (s *UsageStore) GetUsage(ctx context.Context, capID uuid.UUID) (limes.Usage, error) {
	row, err := s.q.GetCapabilityUsage(ctx, pgtype.UUID{Bytes: capID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.Usage{}, limes.ErrUsageNotFound
		}
		return limes.Usage{}, fmt.Errorf("capability/postgres: get usage: %w", err)
	}
	spent, err := pgmoney.NanosFromNumeric(row.SpentUsd)
	if err != nil {
		return limes.Usage{}, err
	}
	reserved, err := pgmoney.NanosFromNumeric(row.ReservedUsd)
	if err != nil {
		return limes.Usage{}, err
	}
	return limes.Usage{
		CapabilityID:   uuid.UUID(row.CapabilityID.Bytes),
		RequestCount:   row.RequestCount,
		SpentAmount:    spent,
		ReservedAmount: reserved,
		UnitCode:       row.UnitCode,
	}, nil
}

// numericFromFloat converts a float64 into pgtype.Numeric. Only a percentage
// travels as a float — ListTenantBudgetsRequest.ThresholdPct; money goes
// through pgmoney. strconv.FormatFloat with 'f' / -1 writes the shortest
// decimal that reads back as the same float64, which is the decimal
// limes.ExactPercent reads the threshold as: 1.1 is eleven tenths, not the
// binary fraction a hair above it.
//
// NaN and ±Inf are refused here rather than "rejected upstream". Both encode
// cleanly into a numeric, and both are silently wrong once compared: Postgres
// orders NaN above every other numeric.
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

// Compile-time conformance, instantiated over Paladin's actual transaction type.
// This is also the assertion that keeps FR-014 honest: Paladin satisfies the same
// published contract a third party would, with no privileged access.
var _ limes.UsageStore[pgx.Tx] = (*UsageStore)(nil)

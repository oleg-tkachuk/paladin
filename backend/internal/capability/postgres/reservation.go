package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgmoney"
)

// holdCapabilityQuery adds a hold to one capability's counter, refusing it
// when spend plus holds would pass the ceiling ($4; NULL = unlimited). Both
// the insert and the update path check the ceiling, as ChargeCapability does.
const holdCapabilityQuery = `
INSERT INTO capability_usage (capability_id, request_count, spent_usd, reserved_usd, unit_code, updated_at)
SELECT $1, 0, 0, $2::numeric, $3, now()
WHERE $4::numeric IS NULL OR $2::numeric <= $4::numeric
ON CONFLICT (capability_id) DO UPDATE
SET reserved_usd = capability_usage.reserved_usd + $2::numeric,
    updated_at   = now()
WHERE $4::numeric IS NULL
   OR capability_usage.spent_usd + capability_usage.reserved_usd + $2::numeric <= $4::numeric
RETURNING reserved_usd;
`

// holdTenantQuery adds a hold to the tenant aggregate, against the ceiling on
// the tenant's own row; a tenant with no row has no ceiling.
const holdTenantQuery = `
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, reserved_usd, unit_code, period_start, updated_at)
VALUES ($1, NULL, 0, $2::numeric, COALESCE(NULLIF($3::text, ''), 'USD'), $4::timestamptz, now())
ON CONFLICT (tenant_id) DO UPDATE
SET reserved_usd = tenant_budgets.reserved_usd + $2::numeric,
    updated_at   = now()
WHERE tenant_budgets.max_budget_usd IS NULL
   OR tenant_budgets.spent_usd + tenant_budgets.reserved_usd + $2::numeric <= tenant_budgets.max_budget_usd
RETURNING reserved_usd;
`

// Reserve implements limes.Meter. The copies, the capability, each ancestor
// and the tenant take the hold in one transaction, in the same leaf-to-root
// order as Charge, so concurrent charges and reservations on one chain lock
// it in one order.
func (s *UsageStore) Reserve(ctx context.Context, req limes.ReserveRequest) (limes.Reservation, error) {
	if err := req.Amount.Validate(); err != nil {
		return limes.Reservation{}, err
	}
	if err := checkSubject(req.Subject); err != nil {
		return limes.Reservation{}, err
	}
	unit, err := limes.NormalizeUnitCode(req.Subject.UnitCode())
	if err != nil {
		return limes.Reservation{}, err
	}
	ttl := req.TTL
	if ttl < 0 {
		return limes.Reservation{}, fmt.Errorf("%w: capability/postgres: reservation TTL %s is negative", limes.ErrInvalidRequest, ttl)
	}
	if ttl == 0 {
		ttl = limes.DefaultReservationTTL
	}
	capID, tenantID := req.Subject.CapabilityID(), req.Subject.TenantID()
	copies := req.Subject.Copies()
	amount := pgmoney.NumericFromNanos(req.Amount)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return limes.Reservation{}, fmt.Errorf("capability/postgres: reserve begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ancestors, err := lineage(ctx, tx, capID)
	if err != nil {
		return limes.Reservation{}, err
	}
	if err := checkUnits(ctx, tx, capID, ancestors, tenantID, unit); err != nil {
		return limes.Reservation{}, err
	}
	if err := checkRange(ctx, tx, append([]uuid.UUID{capID}, ancestorIDs(ancestors)...),
		copyIDs(copies), tenantID, amount); err != nil {
		return limes.Reservation{}, err
	}
	if _, err := addCopySpend(ctx, tx, copySpendArgs{
		capID: capID, copies: copies, amount: amount, counter: copyReserved, overrun: limes.OverrunReject,
	}); err != nil {
		return limes.Reservation{}, err
	}
	if err := holdCapability(ctx, tx, capID, amount, unit, pgmoney.NumericFromCeiling(req.Subject.Budget())); err != nil {
		return limes.Reservation{}, err
	}
	for _, a := range ancestors {
		if err := holdCapability(ctx, tx, a.id, amount, unit, a.maxBudget); err != nil {
			if errors.Is(err, limes.ErrBudgetExceeded) {
				return limes.Reservation{}, fmt.Errorf("%w: ancestor %s", limes.ErrBudgetExceeded, a.id)
			}
			return limes.Reservation{}, err
		}
	}
	var held pgtype.Numeric
	if err := tx.QueryRow(ctx, holdTenantQuery, tenantID, amount, unit, s.now()).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.Reservation{}, limes.ErrTenantBudgetExceeded
		}
		return limes.Reservation{}, counterError("reserve tenant", err)
	}

	id := uuid.New()
	var expires time.Time
	if err := tx.QueryRow(ctx, `
INSERT INTO capability_reservations (id, tenant_id, capability_id, amount, unit_code, op, actor_subject, expires_at,
                                     copy_ids, copy_max_budget_nanos, ancestor_ids)
VALUES ($1, $2, $3, $4::numeric, $5, $6, $7, $8, $9::bytea[], $10::bigint[], $11::uuid[])
RETURNING expires_at`,
		id, tenantID, capID, amount, unit, req.Op, req.Actor, s.now().Add(ttl),
		copyIDs(copies), copyBudgets(copies), ancestorIDs(ancestors),
	).Scan(&expires); err != nil {
		return limes.Reservation{}, fmt.Errorf("capability/postgres: reserve record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return limes.Reservation{}, fmt.Errorf("capability/postgres: reserve commit: %w", err)
	}
	heldCopies, err := heldCopiesOf(copyIDs(copies), copyBudgets(copies))
	if err != nil {
		return limes.Reservation{}, err
	}
	return limes.Reservation{
		ID: id, CapabilityID: capID, TenantID: tenantID, Amount: req.Amount,
		UnitCode: unit, Op: req.Op, Actor: req.Actor, Copies: heldCopies, ExpiresAt: expires.UTC(),
	}, nil
}

// reservationSelect reads reservation rows as the Meter contract reads one
// back; a WHERE clause picks which. Under the caller's tenant scoping, as
// every reservation path runs.
const reservationSelect = `
SELECT id, capability_id, tenant_id, amount, unit_code, op, actor_subject,
       copy_ids, copy_max_budget_nanos, expires_at
FROM   capability_reservations
`

func scanReservation(row pgx.Row) (limes.Reservation, error) {
	var (
		r       limes.Reservation
		amount  pgtype.Numeric
		ids     [][]byte
		budgets []*int64
	)
	if err := row.Scan(&r.ID, &r.CapabilityID, &r.TenantID, &amount, &r.UnitCode, &r.Op, &r.Actor,
		&ids, &budgets, &r.ExpiresAt); err != nil {
		return limes.Reservation{}, err
	}
	copies, err := heldCopiesOf(ids, budgets)
	if err != nil {
		return limes.Reservation{}, err
	}
	if r.Amount, err = pgmoney.NanosFromNumeric(amount); err != nil {
		return limes.Reservation{}, err
	}
	r.Copies, r.ExpiresAt = copies, r.ExpiresAt.UTC()
	return r, nil
}

// GetReservation implements limes.MeterReader.
func (s *UsageStore) GetReservation(ctx context.Context, reservationID uuid.UUID) (limes.Reservation, error) {
	r, err := scanReservation(s.pool.QueryRow(ctx, reservationSelect+`WHERE id = $1`, reservationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return limes.Reservation{}, limes.ErrReservationNotFound
	}
	if err != nil {
		return limes.Reservation{}, fmt.Errorf("capability/postgres: get reservation: %w", err)
	}
	return r, nil
}

// ListReservations implements limes.MeterReader: soonest to expire first,
// ties by id — uuid compares bytewise in Postgres, as the contract orders —
// a page at a time, resuming after the cursor's place.
func (s *UsageStore) ListReservations(ctx context.Context, req limes.ListReservationsRequest) (limes.Page[limes.Reservation], error) {
	if err := req.Validate(); err != nil {
		return limes.Page[limes.Reservation]{}, err
	}
	limit := limes.PageLimit(req.Limit)
	var (
		afterExpires pgtype.Timestamptz
		afterID      pgtype.UUID
	)
	if req.Cursor != "" {
		p, err := parseReservationPlace(req.Cursor)
		if err != nil {
			return limes.Page[limes.Reservation]{}, err
		}
		afterExpires = pgtype.Timestamptz{Time: p.expires, Valid: true}
		afterID = pgtype.UUID{Bytes: p.id, Valid: true}
	}
	rows, err := s.pool.Query(ctx, reservationSelect+`
WHERE capability_id = $1
  AND ($2::timestamptz IS NULL OR (expires_at, id) > ($2::timestamptz, $3::uuid))
ORDER BY expires_at, id
LIMIT $4`, req.CapabilityID, afterExpires, afterID, limit+1)
	if err != nil {
		return limes.Page[limes.Reservation]{}, fmt.Errorf("capability/postgres: list reservations: %w", err)
	}
	defer rows.Close()
	out := []limes.Reservation{}
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return limes.Page[limes.Reservation]{}, fmt.Errorf("capability/postgres: list reservations: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return limes.Page[limes.Reservation]{}, fmt.Errorf("capability/postgres: list reservations: %w", err)
	}
	page := limes.Page[limes.Reservation]{Items: out}
	if len(out) > limit {
		page.Items = out[:limit]
		last := page.Items[limit-1]
		page.Next = reservationPlace{expires: last.ExpiresAt, id: last.ID}.String()
	}
	return page, nil
}

func holdCapability(ctx context.Context, tx pgx.Tx, capID uuid.UUID, amount pgtype.Numeric, unit string, maxBudget pgtype.Numeric) error {
	var held pgtype.Numeric
	if err := tx.QueryRow(ctx, holdCapabilityQuery, capID, amount, unit, maxBudget).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limes.ErrBudgetExceeded
		}
		return counterError("reserve capability", err)
	}
	return nil
}

// openReservation is a reservation row taken out of the table.
type openReservation struct {
	capID, tenantID uuid.UUID
	amount          pgtype.Numeric
	amountNanos     limes.Nanos
	unit, op, actor string
	// copyIDs and copyBudgets are the Biscuit copies the hold is on, and
	// their own budgets in nanos.
	copyIDs     [][]byte
	copyBudgets []*int64
	// ancestorIDs are the ancestors the hold was taken on, nearest first;
	// nil for a hold made before 067, whose ancestors are read from the
	// records when it is released.
	ancestorIDs []uuid.UUID
}

// takeReservationQuery deletes one reservation and returns it. $2, when set,
// restricts it to one that has not expired by then; $3, when set, to one of
// that capability made under exactly the copies $4 — NULL for none.
const takeReservationQuery = `
DELETE FROM capability_reservations
WHERE id = $1
  AND ($2::timestamptz IS NULL OR expires_at > $2::timestamptz)
  AND ($3::uuid IS NULL OR (capability_id = $3::uuid AND copy_ids IS NOT DISTINCT FROM $4::bytea[]))
RETURNING capability_id, tenant_id, amount, unit_code, op, actor_subject,
          copy_ids, copy_max_budget_nanos, ancestor_ids
`

// takeFilter restricts takeReservation to a live reservation of one subject.
type takeFilter struct {
	// liveAt, when set, takes only a reservation not expired by then.
	liveAt pgtype.Timestamptz
	// subject, when it names a capability, takes only a reservation of it
	// made under exactly its copies.
	subject limes.MeterSubject
}

// takeReservation deletes a reservation and returns it. pgx.ErrNoRows when
// there is none the filter admits.
func takeReservation(ctx context.Context, tx pgx.Tx, id uuid.UUID, f takeFilter) (openReservation, error) {
	var (
		r   openReservation
		cap pgtype.UUID
	)
	if f.subject.CapabilityID() != uuid.Nil {
		cap = pgtype.UUID{Bytes: f.subject.CapabilityID(), Valid: true}
	}
	err := tx.QueryRow(ctx, takeReservationQuery, id, f.liveAt, cap, copyIDs(f.subject.Copies())).
		Scan(&r.capID, &r.tenantID, &r.amount, &r.unit, &r.op, &r.actor, &r.copyIDs, &r.copyBudgets, &r.ancestorIDs)
	if err != nil {
		return openReservation{}, err
	}
	if r.amountNanos, err = pgmoney.NanosFromNumeric(r.amount); err != nil {
		return openReservation{}, err
	}
	return r, nil
}

// releaseCapabilityHoldsQuery takes a released hold off the counters of the
// capabilities named that are still on record, never below zero. One no
// longer on record keeps its row only until PurgeOrphans drops it.
const releaseCapabilityHoldsQuery = `
UPDATE capability_usage
SET    reserved_usd = GREATEST(0, reserved_usd - $2::numeric), updated_at = now()
WHERE  capability_id = ANY($1::uuid[])
  AND  capability_id IN (SELECT id FROM capability_records WHERE id = ANY($1::uuid[]))
`

// releaseHold subtracts a reservation's amount from every counter it was
// added to: each copy's, the capability's, each ancestor's and the tenant's.
func releaseHold(ctx context.Context, tx pgx.Tx, r openReservation) error {
	if err := subtractCopySpend(ctx, tx, r.copyIDs, r.amount, copyReserved); err != nil {
		return err
	}
	holders := r.ancestorIDs
	if holders == nil {
		var err error
		if holders, err = recordedAncestors(ctx, tx, r.capID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, releaseCapabilityHoldsQuery, append([]uuid.UUID{r.capID}, holders...), r.amount); err != nil {
		return fmt.Errorf("capability/postgres: release capability holds: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE tenant_budgets
SET reserved_usd = GREATEST(0, reserved_usd - $2::numeric), updated_at = now()
WHERE tenant_id = $1`, r.tenantID, r.amount); err != nil {
		return fmt.Errorf("capability/postgres: release tenant hold: %w", err)
	}
	return nil
}

// Settle implements limes.Meter: on one transaction the reservation
// row is deleted, its holds released, and the actual cost charged. Any
// rejection rolls all three back, so the reservation is still there to be
// settled lower or released. Deleting the row is what serialises two
// settles of one reservation: the second waits on the row lock, then finds
// nothing, and returns the first's charge as a replay.
//
// Only the subject that made the hold settles it: a reservation of another
// capability, or one made under other Biscuit copies, is
// ErrReservationNotFound, so a copy cannot settle a sibling's hold past its
// own limits — and its receipt is not the subject's to read back either.
func (s *UsageStore) Settle(
	ctx context.Context,
	req limes.SettleRequest,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
) (limes.ChargeReceipt, error) {
	if err := checkSubject(req.Subject); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := req.Amount.Validate(); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := req.Overrun.Validate(); err != nil {
		return limes.ChargeReceipt{}, err
	}
	// A direct charge is filed under no reservation, so the zero id must not
	// reach the replay lookup below.
	if req.ReservationID == uuid.Nil {
		return limes.ChargeReceipt{}, limes.ErrReservationNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	r, err := takeReservation(ctx, tx, req.ReservationID, takeFilter{
		liveAt: pgtype.Timestamptz{Time: s.now(), Valid: true}, subject: req.Subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.settledBefore(ctx, tx, req)
	}
	if err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle read: %w", err)
	}
	ancestors, err := lineage(ctx, tx, r.capID)
	if err != nil {
		return limes.ChargeReceipt{}, err
	}
	if err := releaseHold(ctx, tx, r); err != nil {
		return limes.ChargeReceipt{}, err
	}
	if r.ancestorIDs == nil {
		r.ancestorIDs = ancestorIDs(ancestors)
	}
	// The charge is the reservation's: its tenant, unit and the copies it was
	// made under — the subject's, which takeReservation matched — against
	// the subject's own ceiling.
	receipt, err := s.chargeInTx(ctx, tx, chargeArgs{
		subject: req.Subject, unit: r.unit, amount: req.Amount, op: limes.Op(r.op), actor: r.actor,
		overrun: req.Overrun, ancestors: ancestors, settling: &r, reservationID: req.ReservationID,
	}, onCharged)
	if err != nil {
		return limes.ChargeReceipt{TenantID: r.tenantID, UnitCode: r.unit}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle commit: %w", err)
	}
	return receipt, nil
}

// settledBefore answers a settle that found no live reservation of its
// subject: the replay of the charge that settled it, when this subject did —
// a second settle waited on the first's row lock, so that charge is visible
// now — and ErrReservationNotFound otherwise.
func (s *UsageStore) settledBefore(ctx context.Context, tx pgx.Tx, req limes.SettleRequest) (limes.ChargeReceipt, error) {
	var (
		chargeID, capID uuid.UUID
		copies          [][]byte
	)
	err := tx.QueryRow(ctx, chargeByReservationQuery, req.ReservationID).Scan(&chargeID, &capID, &copies)
	if errors.Is(err, pgx.ErrNoRows) {
		return limes.ChargeReceipt{}, limes.ErrReservationNotFound
	}
	if err != nil {
		return limes.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle read charge: %w", err)
	}
	if capID != req.Subject.CapabilityID() || !sameCopyIDs(copies, copyIDs(req.Subject.Copies())) {
		return limes.ChargeReceipt{}, limes.ErrReservationNotFound
	}
	return replay(ctx, tx, chargeID)
}

// sameCopyIDs reports whether two lists of revocation ids are the same, in
// order.
func sameCopyIDs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

// Release implements limes.Meter. A reservation that is gone —
// settled, released, or released by the expiry sweep — is a no-op.
func (s *UsageStore) Release(ctx context.Context, req limes.ReleaseRequest) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("capability/postgres: release begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	r, err := takeReservation(ctx, tx, req.ReservationID, takeFilter{})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("capability/postgres: release read: %w", err)
	}
	if err := releaseHold(ctx, tx, r); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: release commit: %w", err)
	}
	return nil
}

// ReleaseExpired implements limes.UsageHousekeeping. The sweep runs without a
// request tenant, so it lists expired reservations under the cross-tenant
// read flag, then releases each on its own transaction scoped to that
// reservation's tenant — the counter rows are RLS-isolated, and the
// cross-tenant flag widens reads only, never writes.
func (s *UsageStore) ReleaseExpired(ctx context.Context, req limes.ReleaseExpiredRequest) (limes.ReleaseExpiredResult, error) {
	limit, err := housekeepingLimit(req.Limit)
	if err != nil {
		return limes.ReleaseExpiredResult{}, err
	}
	due, err := s.listAcrossTenants(ctx, `
SELECT id, tenant_id FROM capability_reservations
WHERE expires_at <= $1
ORDER BY expires_at, id
LIMIT $2`, s.now(), limit)
	if err != nil {
		return limes.ReleaseExpiredResult{}, fmt.Errorf("capability/postgres: list expired reservations: %w", err)
	}
	var released int64
	for _, e := range due {
		ok, err := s.releaseAsTenant(ctx, e.id, e.tenantID)
		if err != nil {
			return limes.ReleaseExpiredResult{Released: released}, err
		}
		if ok {
			released++
		}
	}
	return limes.ReleaseExpiredResult{Released: released}, nil
}

// tenantRow is a row id and the tenant it belongs to.
type tenantRow struct{ id, tenantID uuid.UUID }

// listAcrossTenants runs a read of (id, tenant_id) rows under the
// cross-tenant read flag, SET LOCAL to its own read-only transaction:
// housekeeping reads every tenant's rows, whatever tenant the caller's
// connection is scoped to.
func (s *UsageStore) listAcrossTenants(ctx context.Context, query string, args ...any) ([]tenantRow, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit
	if _, err := tx.Exec(ctx, setCrossTenantQuery); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (tenantRow, error) {
		var r tenantRow
		err := row.Scan(&r.id, &r.tenantID)
		return r, err
	})
}

// setCrossTenantQuery widens the reads of the current transaction to every
// tenant. The policies admit the flag in USING only, so it cannot let
// anything write into another tenant.
const setCrossTenantQuery = `SELECT set_config('paladin.cross_tenant', 'on', true)`

// setTenantQuery scopes the current transaction to one tenant.
const setTenantQuery = `SELECT set_config('paladin.tenant_id', $1, true)`

// releaseAsTenant releases one reservation on a transaction scoped to its
// tenant, and reports whether it was still there to release.
func (s *UsageStore) releaseAsTenant(ctx context.Context, id, tenantID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("capability/postgres: release begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, setTenantQuery, tenantID.String()); err != nil {
		return false, fmt.Errorf("capability/postgres: release scope: %w", err)
	}
	r, err := takeReservation(ctx, tx, id, takeFilter{})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // settled or released since it was listed
	}
	if err != nil {
		return false, fmt.Errorf("capability/postgres: release read: %w", err)
	}
	if err := releaseHold(ctx, tx, r); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("capability/postgres: release commit: %w", err)
	}
	return true, nil
}

// housekeepingLimit resolves a batch limit, refusing a negative one.
func housekeepingLimit(limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("%w: capability/postgres: housekeeping limit %d is negative", limes.ErrInvalidRequest, limit)
	}
	if limit == 0 {
		return limes.DefaultHousekeepingLimit, nil
	}
	return limit, nil
}

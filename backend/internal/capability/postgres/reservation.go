package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/capability"
)

// holdCapabilityQuery adds a hold to one capability's counter, refusing it
// when spend plus holds would pass the ceiling ($4; 0 = unlimited). Both the
// insert and the update path check the ceiling, as ChargeCapability does.
const holdCapabilityQuery = `
INSERT INTO capability_usage (capability_id, request_count, spent_usd, reserved_usd, unit_code, updated_at)
SELECT $1, 0, 0, $2::numeric, $3, now()
WHERE $4::numeric = 0 OR $2::numeric <= $4::numeric
ON CONFLICT (capability_id) DO UPDATE
SET reserved_usd = capability_usage.reserved_usd + $2::numeric,
    updated_at   = now()
WHERE $4::numeric = 0
   OR capability_usage.spent_usd + capability_usage.reserved_usd + $2::numeric <= $4::numeric
RETURNING reserved_usd;
`

// holdTenantQuery adds a hold to the tenant aggregate, against the ceiling on
// the tenant's own row; a tenant with no row has no ceiling.
const holdTenantQuery = `
INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, reserved_usd, unit_code, period_start, updated_at)
VALUES ($1, 0, 0, $2::numeric, COALESCE(NULLIF($3::text, ''), 'USD'), now(), now())
ON CONFLICT (tenant_id) DO UPDATE
SET reserved_usd = tenant_budgets.reserved_usd + $2::numeric,
    updated_at   = now()
WHERE tenant_budgets.max_budget_usd = 0
   OR tenant_budgets.spent_usd + tenant_budgets.reserved_usd + $2::numeric <= tenant_budgets.max_budget_usd
RETURNING reserved_usd;
`

// Reserve implements capability.Meter. The capability, each ancestor and
// the tenant take the hold in one transaction, in the same leaf-to-root
// order as Charge, so concurrent charges and reservations on one chain lock
// it in one order.
func (s *UsageStore) Reserve(ctx context.Context, req capability.ReserveRequest) (capability.Reservation, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return capability.Reservation{}, err
	}
	if req.TenantID == uuid.Nil {
		return capability.Reservation{}, errors.New("capability/postgres: reserve requires a tenant")
	}
	unit, err := capability.NormaliseUnitCode(req.UnitCode)
	if err != nil {
		return capability.Reservation{}, fmt.Errorf("capability/postgres: reserve: %w", err)
	}
	amount, err := numericFromFloat(req.Amount)
	if err != nil {
		return capability.Reservation{}, err
	}
	maxBudget, err := numericFromFloat(req.MaxBudget)
	if err != nil {
		return capability.Reservation{}, err
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = capability.DefaultReservationTTL
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return capability.Reservation{}, fmt.Errorf("capability/postgres: reserve begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := holdCapability(ctx, tx, req.CapabilityID, amount, unit, maxBudget); err != nil {
		return capability.Reservation{}, err
	}
	ancestors, err := ancestorsOf(ctx, tx, req.CapabilityID)
	if err != nil {
		return capability.Reservation{}, err
	}
	for _, a := range ancestors {
		if err := holdCapability(ctx, tx, a.id, amount, unit, a.maxBudget); err != nil {
			if errors.Is(err, capability.ErrBudgetExceeded) {
				return capability.Reservation{}, fmt.Errorf("%w: ancestor %s", capability.ErrBudgetExceeded, a.id)
			}
			return capability.Reservation{}, err
		}
	}
	var held pgtype.Numeric
	if err := tx.QueryRow(ctx, holdTenantQuery, req.TenantID, amount, unit).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.Reservation{}, capability.ErrTenantBudgetExceeded
		}
		return capability.Reservation{}, fmt.Errorf("capability/postgres: reserve tenant: %w", err)
	}

	id := uuid.New()
	var expires time.Time
	if err := tx.QueryRow(ctx, `
INSERT INTO capability_reservations (id, tenant_id, capability_id, amount, unit_code, op, actor_subject, expires_at)
VALUES ($1, $2, $3, $4::numeric, $5, $6, $7, now() + ($8::bigint * interval '1 microsecond'))
RETURNING expires_at`,
		id, req.TenantID, req.CapabilityID, amount, unit, req.Op, req.Actor, ttl.Microseconds(),
	).Scan(&expires); err != nil {
		return capability.Reservation{}, fmt.Errorf("capability/postgres: reserve record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return capability.Reservation{}, fmt.Errorf("capability/postgres: reserve commit: %w", err)
	}
	return capability.Reservation{ID: id, ExpiresAt: expires}, nil
}

func holdCapability(ctx context.Context, tx pgx.Tx, capID uuid.UUID, amount pgtype.Numeric, unit string, maxBudget pgtype.Numeric) error {
	var held pgtype.Numeric
	if err := tx.QueryRow(ctx, holdCapabilityQuery, capID, amount, unit, maxBudget).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.ErrBudgetExceeded
		}
		return fmt.Errorf("capability/postgres: reserve capability: %w", err)
	}
	return nil
}

// openReservation is a reservation row taken out of the table.
type openReservation struct {
	capID, tenantID uuid.UUID
	amount          pgtype.Numeric
	unit, op, actor string
}

// takeReservation deletes a reservation and returns it. live restricts it to
// one that has not expired. pgx.ErrNoRows when there is none.
func takeReservation(ctx context.Context, tx pgx.Tx, id uuid.UUID, live bool) (openReservation, error) {
	stmt := `DELETE FROM capability_reservations WHERE id = $1`
	if live {
		stmt += ` AND expires_at > now()`
	}
	stmt += ` RETURNING capability_id, tenant_id, amount, unit_code, op, actor_subject`
	var r openReservation
	err := tx.QueryRow(ctx, stmt, id).Scan(&r.capID, &r.tenantID, &r.amount, &r.unit, &r.op, &r.actor)
	return r, err
}

// releaseHold subtracts a reservation's amount from every counter it was
// added to: the capability's, each ancestor's and the tenant's.
func (s *UsageStore) releaseHold(ctx context.Context, tx pgx.Tx, r openReservation) error {
	ids := []uuid.UUID{r.capID}
	ancestors, err := ancestorsOf(ctx, tx, r.capID)
	if err != nil {
		return err
	}
	for _, a := range ancestors {
		ids = append(ids, a.id)
	}
	if _, err := tx.Exec(ctx, `
UPDATE capability_usage
SET reserved_usd = GREATEST(0, reserved_usd - $2::numeric), updated_at = now()
WHERE capability_id = ANY($1)`, ids, r.amount); err != nil {
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

// Settle implements capability.Meter: on one transaction the reservation
// row is deleted, its holds released, and the actual cost charged. Any
// rejection rolls all three back, so the reservation is still there to be
// settled lower or released. Deleting the row is what serialises two
// settles of one reservation: the second waits on the row lock, then finds
// nothing.
func (s *UsageStore) Settle(
	ctx context.Context,
	req capability.SettleRequest,
	onCharged func(ctx context.Context, tx pgx.Tx) error,
) (capability.ChargeReceipt, error) {
	if err := capability.ValidateAmount(req.Amount); err != nil {
		return capability.ChargeReceipt{}, err
	}
	amount, err := numericFromFloat(req.Amount)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	maxBudget, err := numericFromFloat(req.MaxBudget)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	r, err := takeReservation(ctx, tx, req.ReservationID, true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return capability.ChargeReceipt{}, capability.ErrReservationNotFound
		}
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle read: %w", err)
	}
	if err := s.releaseHold(ctx, tx, r); err != nil {
		return capability.ChargeReceipt{}, err
	}
	receipt, err := s.chargeInTx(ctx, tx, capability.ChargeRequest{
		CapabilityID: r.capID,
		TenantID:     r.tenantID,
		Amount:       req.Amount,
		MaxBudget:    req.MaxBudget,
		UnitCode:     r.unit,
		Op:           r.op,
		Actor:        r.actor,
	}, r.unit, amount, maxBudget, onCharged)
	if err != nil {
		return capability.ChargeReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return capability.ChargeReceipt{}, fmt.Errorf("capability/postgres: settle commit: %w", err)
	}
	return receipt, nil
}

// Release implements capability.Meter. A reservation that is gone —
// settled, released, or released by the expiry sweep — is a no-op.
func (s *UsageStore) Release(ctx context.Context, reservationID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("capability/postgres: release begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	r, err := takeReservation(ctx, tx, reservationID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("capability/postgres: release read: %w", err)
	}
	if err := s.releaseHold(ctx, tx, r); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: release commit: %w", err)
	}
	return nil
}

// releaseExpiredBatch bounds one sweep, so a backlog drains over several
// calls instead of pinning one long statement.
const releaseExpiredBatch = 500

// ReleaseExpired implements capability.Meter. The sweep runs without a
// request tenant, so it lists expired reservations under the cross-tenant
// read flag the caller sets, then releases each on its own transaction
// scoped to that reservation's tenant — the counter rows are RLS-isolated,
// and the cross-tenant flag widens reads only, never writes.
func (s *UsageStore) ReleaseExpired(ctx context.Context) (int64, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, tenant_id FROM capability_reservations
WHERE expires_at <= now()
ORDER BY expires_at
LIMIT $1`, releaseExpiredBatch)
	if err != nil {
		return 0, fmt.Errorf("capability/postgres: list expired reservations: %w", err)
	}
	type expired struct{ id, tenantID uuid.UUID }
	var due []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.id, &e.tenantID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("capability/postgres: scan expired reservation: %w", err)
		}
		due = append(due, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("capability/postgres: list expired reservations: %w", err)
	}

	var released int64
	for _, e := range due {
		ok, err := s.releaseExpiredOne(ctx, e.id, e.tenantID)
		if err != nil {
			return released, err
		}
		if ok {
			released++
		}
	}
	return released, nil
}

func (s *UsageStore) releaseExpiredOne(ctx context.Context, id, tenantID uuid.UUID) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("capability/postgres: release expired begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return false, fmt.Errorf("capability/postgres: release expired scope: %w", err)
	}
	r, err := takeReservation(ctx, tx, id, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // settled or released since it was listed
	}
	if err != nil {
		return false, fmt.Errorf("capability/postgres: release expired read: %w", err)
	}
	if err := s.releaseHold(ctx, tx, r); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("capability/postgres: release expired commit: %w", err)
	}
	return true, nil
}

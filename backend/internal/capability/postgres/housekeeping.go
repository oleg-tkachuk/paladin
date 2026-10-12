package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
)

// capabilityReservationsQuery lists a capability's open holds, locked so a
// concurrent settle or release waits for the delete.
const capabilityReservationsQuery = `SELECT id FROM capability_reservations WHERE capability_id = $1 FOR UPDATE`

// deleteCopyUsageQuery drops the counters of a capability's Biscuit copies.
const deleteCopyUsageQuery = `DELETE FROM capability_copy_usage WHERE capability_id = $1`

// forgetAncestorsQuery drops the capabilities named ($1) from the ancestors
// of the holds that still list them: their counters are gone, so a hold
// settled or released later neither writes them back nor counts as held
// there — a settle within its hold meets such an ancestor's ceiling whole.
const forgetAncestorsQuery = `
UPDATE capability_reservations
SET    ancestor_ids = ARRAY(SELECT a FROM unnest(ancestor_ids) AS a WHERE a <> ALL($1::uuid[]))
WHERE  ancestor_ids && $1::uuid[]`

// DeleteUsage implements limes.UsageHousekeeping. It releases the
// capability's open holds first — nothing can settle or release them once it
// is gone, and left behind they would count against its ancestors and its
// tenant for good — then drops its counter and its copies' counters.
func (s *UsageStore) DeleteUsage(ctx context.Context, req limes.DeleteUsageRequest) error {
	capID := req.CapabilityID
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("capability/postgres: delete begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, capabilityReservationsQuery, capID)
	if err != nil {
		return fmt.Errorf("capability/postgres: delete read holds: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return fmt.Errorf("capability/postgres: delete read holds: %w", err)
	}
	for _, id := range ids {
		r, err := takeReservation(ctx, tx, id, takeFilter{})
		if err != nil {
			return fmt.Errorf("capability/postgres: delete take hold: %w", err)
		}
		if err := releaseHold(ctx, tx, r); err != nil {
			return err
		}
	}
	if _, err := s.q.WithTx(tx).DeleteCapabilityUsage(ctx, pgtype.UUID{Bytes: capID, Valid: true}); err != nil {
		return fmt.Errorf("capability/postgres: delete usage: %w", err)
	}
	if _, err := tx.Exec(ctx, deleteCopyUsageQuery, capID); err != nil {
		return fmt.Errorf("capability/postgres: delete copy usage: %w", err)
	}
	// Delegation never crosses tenants, so the holds naming it are of the
	// caller's tenant, which this transaction is scoped to.
	if _, err := tx.Exec(ctx, forgetAncestorsQuery, []uuid.UUID{capID}); err != nil {
		return fmt.Errorf("capability/postgres: forget dropped ancestor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: delete commit: %w", err)
	}
	return nil
}

// orphansQuery lists, at most $1 of them, the capabilities no longer on
// record that the meter still keeps something for — a counter, a copy's
// counter or a hold — in id order, so batches are repeatable.
const orphansQuery = `
SELECT o.capability_id
FROM (
    SELECT capability_id FROM capability_usage
    UNION
    SELECT capability_id FROM capability_copy_usage
    UNION
    SELECT capability_id FROM capability_reservations
) o
WHERE NOT EXISTS (SELECT 1 FROM capability_records r WHERE r.id = o.capability_id)
ORDER BY o.capability_id
LIMIT $1
`

// orphanHoldsQuery lists the holds of the capabilities named, with their
// tenants.
const orphanHoldsQuery = `SELECT id, tenant_id FROM capability_reservations WHERE capability_id = ANY($1::uuid[])`

// PurgeOrphans implements limes.UsageHousekeeping. It runs across tenants:
// the orphans are read and their rows dropped under the cross-tenant flag,
// which admits a read or a delete, and each hold is released on a
// transaction scoped to its tenant, since a release writes to the ancestors'
// counters and the tenant's aggregate. A released hold no longer counts
// against anything; the orphan's own counters are dropped, never written.
func (s *UsageStore) PurgeOrphans(ctx context.Context, req limes.PurgeOrphansRequest) (limes.PurgeOrphansResult, error) {
	limit, err := housekeepingLimit(req.Limit)
	if err != nil {
		return limes.PurgeOrphansResult{}, err
	}
	orphans, err := s.readAcrossTenants(ctx, func(tx pgx.Tx) ([]uuid.UUID, error) {
		rows, err := tx.Query(ctx, orphansQuery, limit)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	})
	if err != nil {
		return limes.PurgeOrphansResult{}, fmt.Errorf("capability/postgres: list usage orphans: %w", err)
	}
	if len(orphans) == 0 {
		return limes.PurgeOrphansResult{}, nil
	}

	holds, err := s.listAcrossTenants(ctx, orphanHoldsQuery, orphans)
	if err != nil {
		return limes.PurgeOrphansResult{}, fmt.Errorf("capability/postgres: list orphan holds: %w", err)
	}
	for _, h := range holds {
		if _, err := s.releaseAsTenant(ctx, h.id, h.tenantID); err != nil {
			return limes.PurgeOrphansResult{}, err
		}
	}

	if err := s.forgetAcrossTenants(ctx, orphans); err != nil {
		return limes.PurgeOrphansResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return limes.PurgeOrphansResult{}, fmt.Errorf("capability/postgres: purge orphans begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, setCrossTenantQuery); err != nil {
		return limes.PurgeOrphansResult{}, fmt.Errorf("capability/postgres: purge orphans scope: %w", err)
	}
	for _, stmt := range []string{
		`DELETE FROM capability_copy_usage WHERE capability_id = ANY($1::uuid[])`,
		`DELETE FROM capability_usage WHERE capability_id = ANY($1::uuid[])`,
	} {
		if _, err := tx.Exec(ctx, stmt, orphans); err != nil {
			return limes.PurgeOrphansResult{}, fmt.Errorf("capability/postgres: purge orphans: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return limes.PurgeOrphansResult{}, fmt.Errorf("capability/postgres: purge orphans commit: %w", err)
	}
	return limes.PurgeOrphansResult{Purged: int64(len(orphans))}, nil
}

// readAcrossTenants runs read on a read-only transaction under the
// cross-tenant read flag.
func (s *UsageStore) readAcrossTenants(ctx context.Context, read func(tx pgx.Tx) ([]uuid.UUID, error)) ([]uuid.UUID, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit
	if _, err := tx.Exec(ctx, setCrossTenantQuery); err != nil {
		return nil, err
	}
	return read(tx)
}

// holdTenantsQuery lists the tenants of the holds that name any of the
// capabilities ($1) as an ancestor.
const holdTenantsQuery = `SELECT DISTINCT tenant_id FROM capability_reservations WHERE ancestor_ids && $1::uuid[]`

// forgetAcrossTenants runs forgetAncestorsQuery for every tenant whose holds
// name the capabilities ids, each on a transaction scoped to that tenant: a
// hold's row is written as its tenant.
func (s *UsageStore) forgetAcrossTenants(ctx context.Context, ids []uuid.UUID) error {
	tenants, err := s.readAcrossTenants(ctx, func(tx pgx.Tx) ([]uuid.UUID, error) {
		rows, err := tx.Query(ctx, holdTenantsQuery, ids)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	})
	if err != nil {
		return fmt.Errorf("capability/postgres: list holds naming orphans: %w", err)
	}
	for _, tenant := range tenants {
		if err := s.forgetAsTenant(ctx, tenant, ids); err != nil {
			return err
		}
	}
	return nil
}

func (s *UsageStore) forgetAsTenant(ctx context.Context, tenant uuid.UUID, ids []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("capability/postgres: forget ancestors begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, setTenantQuery, tenant.String()); err != nil {
		return fmt.Errorf("capability/postgres: forget ancestors scope: %w", err)
	}
	if _, err := tx.Exec(ctx, forgetAncestorsQuery, ids); err != nil {
		return fmt.Errorf("capability/postgres: forget ancestors: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("capability/postgres: forget ancestors commit: %w", err)
	}
	return nil
}

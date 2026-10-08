package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgmoney"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Counters of Biscuit copies with limits of their own (migration 039). Every
// Meter call takes them before the capability's own row, innermost copy
// first, so two calls sharing part of a chain lock it in one order, leaf to
// root, as BumpRequest describes.

// bumpCopyQuery counts one request against a copy, refusing it past the
// copy's limit ($3; 0 = none). The first request always fits: a limit is
// positive.
const bumpCopyQuery = `
INSERT INTO capability_copy_usage (revocation_id, capability_id, request_count)
VALUES ($1, $2, 1)
ON CONFLICT (revocation_id) DO UPDATE
SET request_count = capability_copy_usage.request_count + 1,
    updated_at    = now()
WHERE $3::bigint = 0 OR capability_copy_usage.request_count + 1 <= $3::bigint
RETURNING request_count;
`

// addCopySpendQuery adds to a copy's spend ($4 = 'spent') or holds ($4 =
// 'reserved'), refusing it when spend plus holds would pass the copy's budget
// ($5; 0 = none). Both the insert and the update path check it.
const addCopySpendQuery = `
INSERT INTO capability_copy_usage (revocation_id, capability_id, spent, reserved)
SELECT $1, $2,
       CASE WHEN $4::text = 'spent' THEN $3::numeric ELSE 0 END,
       CASE WHEN $4::text = 'reserved' THEN $3::numeric ELSE 0 END
WHERE $5::numeric = 0 OR $3::numeric <= $5::numeric
ON CONFLICT (revocation_id) DO UPDATE
SET spent      = capability_copy_usage.spent + CASE WHEN $4::text = 'spent' THEN $3::numeric ELSE 0 END,
    reserved   = capability_copy_usage.reserved + CASE WHEN $4::text = 'reserved' THEN $3::numeric ELSE 0 END,
    updated_at = now()
WHERE $5::numeric = 0
   OR capability_copy_usage.spent + capability_copy_usage.reserved + $3::numeric <= $5::numeric
RETURNING spent;
`

// The two counters addCopySpendQuery can add to.
const (
	copySpent    = "spent"
	copyReserved = "reserved"
)

func bumpCopies(ctx context.Context, tx pgx.Tx, capID uuid.UUID, copies []capability.CopyCeiling) error {
	for _, c := range copies {
		var n int64
		if err := tx.QueryRow(ctx, bumpCopyQuery, c.RevocationID, capID, c.MaxRequests).Scan(&n); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: copy %x", capability.ErrRequestLimitExceeded, c.RevocationID)
			}
			return fmt.Errorf("capability/postgres: bump copy: %w", err)
		}
	}
	return nil
}

// addCopySpend adds amount to counter of every copy, against its budget.
func addCopySpend(ctx context.Context, tx pgx.Tx, capID uuid.UUID, copies []capability.CopyCeiling, amount pgtype.Numeric, counter string) error {
	_, err := addCopySpendPast(ctx, tx, capID, copies, amount, counter, capability.OverrunReject)
	return err
}

// addCopySpendPast is addCopySpend under an overrun policy: with
// OverrunRecord a copy whose budget the amount would cross takes it anyway,
// and crossed reports that one did.
func addCopySpendPast(ctx context.Context, tx pgx.Tx, capID uuid.UUID, copies []capability.CopyCeiling,
	amount pgtype.Numeric, counter string, overrun capability.OverrunPolicy,
) (crossed bool, err error) {
	for _, c := range copies {
		limit := pgmoney.NumericFromNanos(c.MaxBudget)
		var spent pgtype.Numeric
		err = tx.QueryRow(ctx, addCopySpendQuery, c.RevocationID, capID, amount, counter, limit).Scan(&spent)
		if errors.Is(err, pgx.ErrNoRows) && overrun == capability.OverrunRecord {
			crossed = true
			err = tx.QueryRow(ctx, addCopySpendQuery, c.RevocationID, capID, amount, counter, noCeiling).Scan(&spent)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, fmt.Errorf("%w: copy %x", capability.ErrBudgetExceeded, c.RevocationID)
			}
			return false, fmt.Errorf("capability/postgres: %s copy: %w", counter, err)
		}
	}
	return crossed, nil
}

// subtractCopySpend takes amount off counter of the copies named, never below
// zero: a refund or a released hold.
func subtractCopySpend(ctx context.Context, tx pgx.Tx, ids [][]byte, amount pgtype.Numeric, counter string) error {
	if len(ids) == 0 {
		return nil
	}
	stmt := `
UPDATE capability_copy_usage
SET ` + pgx.Identifier{counter}.Sanitize() + ` = GREATEST(0, ` + pgx.Identifier{counter}.Sanitize() + ` - $2::numeric),
    updated_at = now()
WHERE revocation_id = ANY($1::bytea[])`
	if _, err := tx.Exec(ctx, stmt, ids, amount); err != nil {
		return fmt.Errorf("capability/postgres: return %s to copies: %w", counter, err)
	}
	return nil
}

// copyIDs lists the copies' revocation ids, innermost first; nil for none, so
// a charge or reservation without copies stores NULL.
func copyIDs(copies []capability.CopyCeiling) [][]byte {
	if len(copies) == 0 {
		return nil
	}
	ids := make([][]byte, 0, len(copies))
	for _, c := range copies {
		ids = append(ids, c.RevocationID)
	}
	return ids
}

// copyBudgets lists the copies' budget limits in micros, in copyIDs' order.
func copyBudgets(copies []capability.CopyCeiling) []int64 {
	if len(copies) == 0 {
		return nil
	}
	out := make([]int64, 0, len(copies))
	for _, c := range copies {
		out = append(out, int64(c.MaxBudget))
	}
	return out
}

// ceilingsOf rebuilds a reservation's copy limits from its row. Request limits
// are not kept: a reservation holds spend only.
func ceilingsOf(ids [][]byte, budgets []int64) ([]capability.CopyCeiling, error) {
	if len(ids) != len(budgets) {
		return nil, fmt.Errorf("capability/postgres: reservation holds %d copies and %d budgets", len(ids), len(budgets))
	}
	out := make([]capability.CopyCeiling, 0, len(ids))
	for i, id := range ids {
		out = append(out, capability.CopyCeiling{RevocationID: id, MaxBudget: capability.Nanos(budgets[i])})
	}
	return out, nil
}

var _ capability.CopyUsageReader = (*UsageStore)(nil)

// CopyUsage implements capability.CopyUsageReader. It reads under the
// caller's tenant scoping: a copy of another tenant's capability is absent,
// exactly as one never used.
func (s *UsageStore) CopyUsage(ctx context.Context, revocationIDs [][]byte) ([]capability.CopyUsage, error) {
	if len(revocationIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT revocation_id, capability_id, request_count, spent, reserved
FROM   capability_copy_usage
WHERE  revocation_id = ANY($1::bytea[])`, revocationIDs)
	if err != nil {
		return nil, fmt.Errorf("capability/postgres: copy usage: %w", err)
	}
	defer rows.Close()
	var out []capability.CopyUsage
	for rows.Next() {
		var (
			u               capability.CopyUsage
			spent, reserved pgtype.Numeric
		)
		if err := rows.Scan(&u.RevocationID, &u.CapabilityID, &u.RequestCount, &spent, &reserved); err != nil {
			return nil, fmt.Errorf("capability/postgres: copy usage scan: %w", err)
		}
		if u.SpentAmount, err = pgmoney.NanosFromNumeric(spent); err != nil {
			return nil, err
		}
		if u.ReservedAmount, err = pgmoney.NanosFromNumeric(reserved); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("capability/postgres: copy usage: %w", err)
	}
	return out, nil
}

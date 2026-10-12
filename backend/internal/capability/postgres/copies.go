package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgmoney"
)

// Counters of Biscuit copies with limits of their own (migration 039). Every
// Meter call takes them before the capability's own row, innermost copy
// first, so two calls sharing part of a chain lock it in one order, leaf to
// root, as BumpRequest describes.

// bumpCopyQuery counts one request against a copy, refusing it past the
// copy's limit ($3; NULL when the copy has none of its own, 0 allowing none).
// Both the insert and the update path check it.
const bumpCopyQuery = `
INSERT INTO capability_copy_usage (revocation_id, capability_id, request_count)
SELECT $1, $2, 1
WHERE $3::bigint IS NULL OR 1 <= $3::bigint
ON CONFLICT (revocation_id) DO UPDATE
SET request_count = capability_copy_usage.request_count + 1,
    updated_at    = now()
WHERE $3::bigint IS NULL OR capability_copy_usage.request_count + 1 <= $3::bigint
RETURNING request_count;
`

// addCopySpendQuery adds to a copy's spend ($4 = 'spent') or holds ($4 =
// 'reserved'), refusing it when spend plus holds would pass the copy's budget
// ($5; NULL when the copy has none of its own). Both the insert and the
// update path check it.
const addCopySpendQuery = `
INSERT INTO capability_copy_usage (revocation_id, capability_id, spent, reserved)
SELECT $1, $2,
       CASE WHEN $4::text = 'spent' THEN $3::numeric ELSE 0 END,
       CASE WHEN $4::text = 'reserved' THEN $3::numeric ELSE 0 END
WHERE $5::numeric IS NULL OR $3::numeric <= $5::numeric
ON CONFLICT (revocation_id) DO UPDATE
SET spent      = capability_copy_usage.spent + CASE WHEN $4::text = 'spent' THEN $3::numeric ELSE 0 END,
    reserved   = capability_copy_usage.reserved + CASE WHEN $4::text = 'reserved' THEN $3::numeric ELSE 0 END,
    updated_at = now()
WHERE $5::numeric IS NULL
   OR capability_copy_usage.spent + capability_copy_usage.reserved + $3::numeric <= $5::numeric
RETURNING spent;
`

// The two counters addCopySpendQuery can add to.
const (
	copySpent    = "spent"
	copyReserved = "reserved"
)

func bumpCopies(ctx context.Context, tx pgx.Tx, capID uuid.UUID, copies []limes.CopyCeiling) error {
	for _, c := range copies {
		var limit *int64
		if c.Requests != nil {
			limit = requestCeilingColumn(*c.Requests)
		}
		var n int64
		if err := tx.QueryRow(ctx, bumpCopyQuery, c.RevocationID, capID, limit).Scan(&n); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: copy %x", limes.ErrRequestLimitExceeded, c.RevocationID)
			}
			return fmt.Errorf("capability/postgres: bump copy: %w", err)
		}
	}
	return nil
}

// copyBudgetColumn is a copy's own budget as the counter queries take it:
// NULL when the copy has none.
func copyBudgetColumn(b *limes.BudgetCeiling) pgtype.Numeric {
	if b == nil {
		return unlimitedCeiling
	}
	return pgmoney.NumericFromCeiling(*b)
}

// copySpendArgs is one amount added to the counters of a subject's copies.
type copySpendArgs struct {
	capID   uuid.UUID
	copies  []limes.CopyCeiling
	amount  pgtype.Numeric
	counter string // copySpent or copyReserved
	overrun limes.OverrunPolicy
	// ceilingsLifted adds the amount whatever the copies' budgets: a settle
	// within the hold those copies already count.
	ceilingsLifted bool
}

// addCopySpend adds the amount to the counter of every copy, against its
// budget. With OverrunRecord a copy whose budget the amount would cross
// takes it anyway, and crossed reports that one did.
func addCopySpend(ctx context.Context, tx pgx.Tx, a copySpendArgs) (crossed bool, err error) {
	for _, c := range a.copies {
		limit := copyBudgetColumn(c.Budget)
		if a.ceilingsLifted {
			limit = unlimitedCeiling
		}
		var spent pgtype.Numeric
		err = tx.QueryRow(ctx, addCopySpendQuery, c.RevocationID, a.capID, a.amount, a.counter, limit).Scan(&spent)
		if errors.Is(err, pgx.ErrNoRows) && a.overrun == limes.OverrunRecord {
			crossed = true
			err = tx.QueryRow(ctx, addCopySpendQuery, c.RevocationID, a.capID, a.amount, a.counter, unlimitedCeiling).Scan(&spent)
		}
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, fmt.Errorf("%w: copy %x", limes.ErrBudgetExceeded, c.RevocationID)
			}
			return false, counterError(a.counter+" copy", err)
		}
	}
	return crossed, nil
}

// subtractCopySpend takes amount off counter of the copies named, never below
// zero: a refund or a released hold. The copies of a capability no longer on
// record are left for PurgeOrphans, which drops them.
func subtractCopySpend(ctx context.Context, tx pgx.Tx, ids [][]byte, amount pgtype.Numeric, counter string) error {
	if len(ids) == 0 {
		return nil
	}
	stmt := `
UPDATE capability_copy_usage
SET ` + pgx.Identifier{counter}.Sanitize() + ` = GREATEST(0, ` + pgx.Identifier{counter}.Sanitize() + ` - $2::numeric),
    updated_at = now()
WHERE revocation_id = ANY($1::bytea[])
  AND capability_id IN (SELECT id FROM capability_records)`
	if _, err := tx.Exec(ctx, stmt, ids, amount); err != nil {
		return fmt.Errorf("capability/postgres: return %s to copies: %w", counter, err)
	}
	return nil
}

// copyIDs lists the copies' revocation ids, innermost first; nil for none, so
// a charge or reservation without copies stores NULL.
func copyIDs(copies []limes.CopyCeiling) [][]byte {
	if len(copies) == 0 {
		return nil
	}
	ids := make([][]byte, 0, len(copies))
	for _, c := range copies {
		ids = append(ids, c.RevocationID)
	}
	return ids
}

// copyBudgets lists the copies' own budgets in nanos, in copyIDs' order: a
// NULL element for a copy with none of its own.
func copyBudgets(copies []limes.CopyCeiling) []*int64 {
	if len(copies) == 0 {
		return nil
	}
	out := make([]*int64, 0, len(copies))
	for _, c := range copies {
		if c.Budget == nil {
			out = append(out, nil)
			continue
		}
		n := int64(c.Budget.Max)
		out = append(out, &n)
	}
	return out
}

// heldCopiesOf rebuilds a reservation's copies from its row. Request limits
// are not kept: a reservation holds spend only.
func heldCopiesOf(ids [][]byte, budgets []*int64) ([]limes.HeldCopy, error) {
	if len(ids) != len(budgets) {
		return nil, fmt.Errorf("capability/postgres: reservation holds %d copies and %d budgets", len(ids), len(budgets))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]limes.HeldCopy, 0, len(ids))
	for i, id := range ids {
		h := limes.HeldCopy{RevocationID: id}
		if budgets[i] != nil {
			h.Budget = &limes.BudgetCeiling{Max: limes.Nanos(*budgets[i])}
		}
		out = append(out, h)
	}
	return out, nil
}

var _ limes.CopyUsageReader = (*UsageStore)(nil)

// CopyUsage implements limes.CopyUsageReader. It reads under the
// caller's tenant scoping: a copy of another tenant's capability is absent,
// exactly as one never used.
func (s *UsageStore) CopyUsage(ctx context.Context, req limes.CopyUsageRequest) (limes.CopyUsageResult, error) {
	if err := req.Validate(); err != nil {
		return limes.CopyUsageResult{}, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT revocation_id, capability_id, request_count, spent, reserved
FROM   capability_copy_usage
WHERE  revocation_id = ANY($1::bytea[])`, req.RevocationIDs)
	if err != nil {
		return limes.CopyUsageResult{}, fmt.Errorf("capability/postgres: copy usage: %w", err)
	}
	defer rows.Close()
	var out []limes.CopyUsage
	for rows.Next() {
		var (
			u               limes.CopyUsage
			spent, reserved pgtype.Numeric
		)
		if err := rows.Scan(&u.RevocationID, &u.CapabilityID, &u.RequestCount, &spent, &reserved); err != nil {
			return limes.CopyUsageResult{}, fmt.Errorf("capability/postgres: copy usage scan: %w", err)
		}
		if u.SpentAmount, err = pgmoney.NanosFromNumeric(spent); err != nil {
			return limes.CopyUsageResult{}, err
		}
		if u.ReservedAmount, err = pgmoney.NanosFromNumeric(reserved); err != nil {
			return limes.CopyUsageResult{}, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return limes.CopyUsageResult{}, fmt.Errorf("capability/postgres: copy usage: %w", err)
	}
	return limes.CopyUsageResult{Copies: out}, nil
}

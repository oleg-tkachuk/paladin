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
//     a capability whose MaxRequests / MaxBudgetUSD changes mid-life
//     (delegation narrowing) is enforced against the *current* value
//     the caller passes — no stale row-stored limit to invalidate.
//     The caveats themselves live in the JWT claim set; the row
//     just tracks accumulated usage.
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

// Charge implements capability.UsageStore. amountUSD must be ≥ 0;
// negative input rejected — refunds are an explicit op (not exposed
// today; would land as a separate Refund method).
func (s *UsageStore) Charge(
	ctx context.Context,
	capID uuid.UUID,
	amountUSD, maxBudgetUSD float64,
) (float64, error) {
	if amountUSD < 0 {
		return 0, errors.New("capability/postgres: charge amount must be >= 0")
	}
	amount, err := numericFromFloat(amountUSD)
	if err != nil {
		return 0, err
	}
	maxBudget, err := numericFromFloat(maxBudgetUSD)
	if err != nil {
		return 0, err
	}
	spent, err := s.q.ChargeCapability(
		ctx,
		pgtype.UUID{Bytes: capID, Valid: true},
		amount,
		maxBudget,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, capability.ErrBudgetExceeded
		}
		return 0, fmt.Errorf("capability/postgres: charge: %w", err)
	}
	return floatFromNumeric(spent), nil
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
		SpentUSD:     floatFromNumeric(row.SpentUsd),
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

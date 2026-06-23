// Package statemachine owns the lifecycle transitions for Object.
//
// The simplified automaton:
//
//	PENDING ─(event|rpc|reconciler)→ AVAILABLE
//	PENDING ─(presign expired + no upload)→ FAILED
//	AVAILABLE ─(DeleteObject soft)→ DELETED
//	AVAILABLE ─(DeleteObject permanent)→ ∅ (row removed)
//	DELETED ─(RestoreObject)→ AVAILABLE
//
// All promotions to AVAILABLE are idempotent and race-safe via the
// `sequencer` column (monotonic opaque string from the storage backend's
// event stream). A stale event with sequencer < stored sequencer is a no-op.
package statemachine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// State mirrors the object_state enum in SQL.
type State string

const (
	StatePending   State = "PENDING"
	StateAvailable State = "AVAILABLE"
	StateFailed    State = "FAILED"
	StateDeleted   State = "DELETED"
)

// Source records which signal triggered a transition. Exported for metrics.
type Source string

const (
	SourceEvent      Source = "event"
	SourceRPC        Source = "rpc"
	SourceReconciler Source = "reconciler"
)

// Transitioner owns PENDING → AVAILABLE, PENDING → FAILED, and DELETED.
//
// All methods are safe under concurrent producers (event consumer, RPC
// handler, reconciler poll) — the SQL uses WHERE state/sequencer guards so
// the first-to-commit wins and the rest are no-ops.
type Transitioner struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Transitioner {
	return &Transitioner{pool: pool}
}

// dbExec is the subset of *pgxpool.Pool / pgx.Tx the transition queries
// use. Lets a transition run either on the pool (auto-commit) or inside
// a caller-supplied transaction (atomic with the caller's other writes,
// e.g. the event outbox — see PromoteToAvailableInTx).
type dbExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PromoteToAvailable attempts the PENDING → AVAILABLE transition for
// object_id. Safe under races: stale sequencers or state mismatches are
// silently dropped (returned changed=false) rather than raising an error.
//
// etag/size/checksum come from the trusted source (event or HEAD probe).
// RPC-driven promotions pass etag/sequencer="" and the handler first does
// a HEAD to materialize authoritative values.
func (t *Transitioner) PromoteToAvailable(
	ctx context.Context,
	objectID uuid.UUID,
	etag string,
	sizeBytes int64,
	checksum string,
	sequencer string,
	source Source,
) (changed bool, err error) {
	return t.promote(ctx, t.pool, objectID, etag, sizeBytes, checksum, sequencer, source)
}

// PromoteToAvailableInTx promotes AND runs onPromoted inside ONE
// transaction, so the caller (an event-producing handler) can write its
// outbox rows atomically with the state change — closing the dual-write
// crash window (ADR-0003). onPromoted runs ONLY when the promotion
// actually changed state (changed=true); any error from it, or from the
// commit, rolls back BOTH the state change and the outbox writes. When
// the promote is a no-op (stale event / already promoted) the tx commits
// with no side effects and onPromoted is skipped.
func (t *Transitioner) PromoteToAvailableInTx(
	ctx context.Context,
	objectID uuid.UUID,
	etag string,
	sizeBytes int64,
	checksum string,
	sequencer string,
	source Source,
	onPromoted func(ctx context.Context, tx pgx.Tx) error,
) (changed bool, err error) {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("sm: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	changed, err = t.promote(ctx, tx, objectID, etag, sizeBytes, checksum, sequencer, source)
	if err != nil {
		return false, err
	}
	if changed && onPromoted != nil {
		if err := onPromoted(ctx, tx); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("sm: commit tx: %w", err)
	}
	return changed, nil
}

// promote runs the PENDING → AVAILABLE guarded update on `exec` (pool or
// tx). Stale sequencers / state mismatches return changed=false (no-op),
// never an error.
func (t *Transitioner) promote(
	ctx context.Context,
	exec dbExec,
	objectID uuid.UUID,
	etag string,
	sizeBytes int64,
	checksum string,
	sequencer string,
	source Source,
) (changed bool, err error) {
	const q = `
        UPDATE objects
           SET state = 'AVAILABLE',
               etag = COALESCE(NULLIF($2, ''), etag),
               size_bytes = CASE WHEN $3 > 0 THEN $3 ELSE size_bytes END,
               checksum = COALESCE(NULLIF($4, ''), checksum),
               sequencer = COALESCE(NULLIF($5, ''), sequencer),
               committed_at = CASE WHEN state = 'PENDING' THEN now() ELSE committed_at END
         WHERE object_id = $1
           AND state IN ('PENDING', 'AVAILABLE')
           AND ($5 = '' OR sequencer IS NULL OR $5 > sequencer)
        RETURNING state
    `
	var newState string
	err = exec.QueryRow(ctx, q, objectID, etag, sizeBytes, checksum, sequencer).Scan(&newState)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, fmt.Errorf("sm: promote: %w", err)
	}
	metricTransitionsTotal.WithLabelValues(string(source), "AVAILABLE").Inc()
	return true, nil
}

// MarkFailed transitions PENDING → FAILED after the presign TTL has
// elapsed with no confirming event/RPC. Idempotent.
func (t *Transitioner) MarkFailed(ctx context.Context, objectID uuid.UUID, reason string) error {
	const q = `
        UPDATE objects
           SET state = 'FAILED',
               terminated_at = now()
         WHERE object_id = $1 AND state = 'PENDING'
    `
	_, err := t.pool.Exec(ctx, q, objectID)
	if err != nil {
		return fmt.Errorf("sm: mark failed: %w", err)
	}
	metricTransitionsTotal.WithLabelValues(string(SourceReconciler), "FAILED").Inc()
	return nil
}

// SoftDelete moves AVAILABLE → DELETED with optimistic concurrency.
//
// resourceVersion is the row version the caller last saw; pass 0 to skip
// the check (use with care — prefer surfacing to the client).
func (t *Transitioner) SoftDelete(ctx context.Context, objectID uuid.UUID, resourceVersion int64) error {
	return t.softDelete(ctx, t.pool, objectID, resourceVersion)
}

// SoftDeleteInTx soft-deletes AND runs onDeleted in ONE tx so the caller
// can write its outbox rows atomically with the DELETED transition
// (ADR-0003). onDeleted runs only after a successful delete; any error
// from it (or the commit) rolls back both. ErrConflict (0 rows) is
// returned before onDeleted runs.
func (t *Transitioner) SoftDeleteInTx(ctx context.Context, objectID uuid.UUID, resourceVersion int64, onDeleted func(ctx context.Context, tx pgx.Tx) error) error {
	return t.transitionInTx(ctx, func(exec dbExec) error {
		return t.softDelete(ctx, exec, objectID, resourceVersion)
	}, onDeleted)
}

func (t *Transitioner) softDelete(ctx context.Context, exec dbExec, objectID uuid.UUID, resourceVersion int64) error {
	const q = `
        UPDATE objects
           SET state = 'DELETED',
               terminated_at = now()
         WHERE object_id = $1
           AND state = 'AVAILABLE'
           AND ($2 = 0 OR resource_version = $2)
    `
	tag, err := exec.Exec(ctx, q, objectID, resourceVersion)
	if err != nil {
		return fmt.Errorf("sm: soft delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// Restore moves DELETED → AVAILABLE.
func (t *Transitioner) Restore(ctx context.Context, objectID uuid.UUID) error {
	return t.restore(ctx, t.pool, objectID)
}

// RestoreInTx restores AND runs onRestored in ONE tx (ADR-0003), same
// contract as SoftDeleteInTx. ErrNotFound (0 rows) is returned before
// onRestored runs.
func (t *Transitioner) RestoreInTx(ctx context.Context, objectID uuid.UUID, onRestored func(ctx context.Context, tx pgx.Tx) error) error {
	return t.transitionInTx(ctx, func(exec dbExec) error {
		return t.restore(ctx, exec, objectID)
	}, onRestored)
}

func (t *Transitioner) restore(ctx context.Context, exec dbExec, objectID uuid.UUID) error {
	const q = `
        UPDATE objects
           SET state = 'AVAILABLE',
               terminated_at = NULL
         WHERE object_id = $1 AND state = 'DELETED'
    `
	tag, err := exec.Exec(ctx, q, objectID)
	if err != nil {
		return fmt.Errorf("sm: restore: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// transitionInTx runs a single-statement transition `do` then `after`
// (the outbox fan-out) in one transaction. If `do` errors (incl. the
// ErrConflict / ErrNotFound 0-row sentinels) the tx rolls back and
// `after` never runs; if `after` errors, both roll back. Shared by the
// SoftDeleteInTx / RestoreInTx orchestrators.
func (t *Transitioner) transitionInTx(ctx context.Context, do func(exec dbExec) error, after func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sm: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := do(tx); err != nil {
		return err
	}
	if after != nil {
		if err := after(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sm: commit tx: %w", err)
	}
	return nil
}

// ScanPendingExpired returns up to `limit` PENDING object IDs whose presign
// TTL has elapsed by `graceBeyond`. Consumed by the Reaper worker.
func (t *Transitioner) ScanPendingExpired(
	ctx context.Context,
	graceBeyond time.Duration,
	limit int,
) ([]uuid.UUID, error) {
	const q = `
        SELECT object_id
          FROM objects
         WHERE state = 'PENDING'
           AND presign_expires_at IS NOT NULL
           AND presign_expires_at < now() - $1::interval
         ORDER BY presign_expires_at
         LIMIT $2
    `
	rows, err := t.pool.Query(ctx, q, graceBeyond.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("sm: scan: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

var (
	ErrConflict = errors.New("statemachine: version conflict")
	ErrNotFound = errors.New("statemachine: object not found or wrong state")
)

// isNoRows checks the pgx-specific no-rows error. errors.Is (not a
// string compare) so wrapped sentinels — fmt.Errorf("...: %w",
// pgx.ErrNoRows) — still classify as no-rows instead of surfacing as
// false errors that re-queue the object forever.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

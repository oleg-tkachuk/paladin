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
	pool dbPool
}

// New takes the concrete pool — callers keep passing *pgxpool.Pool — while the
// field is stored behind dbPool so the tx-orchestrating paths can be exercised
// without a database, the same way dbExec already does for the statement
// bodies.
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

// dbPool is the subset of *pgxpool.Pool the Transitioner itself needs: the
// statement surface of dbExec plus Begin (for the *InTx orchestrators) and
// Query (for ScanPendingExpired). *pgxpool.Pool satisfies it as-is.
type dbPool interface {
	dbExec
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// PromoteToAvailable attempts the PENDING → AVAILABLE transition for
// the object id. Safe under races: stale sequencers or state mismatches are
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
	// In a transaction even with nothing to run after it: the check against
	// what the object was registered with locks the row, and the lock has to
	// last until the promote that depends on it.
	return t.PromoteToAvailableInTx(ctx, objectID, etag, sizeBytes, checksum, sequencer, source, nil)
}

// ErrContentMismatch is returned by a promote whose stored bytes are not the
// ones the object was registered with: a different size, or a checksum that
// disagrees with the registered one. The row is left PENDING; the caller
// decides what happens to the bytes. Match with errors.Is.
var ErrContentMismatch = errors.New("stored object does not match its registration")

// ContentMismatchError says which registered value the stored bytes broke.
type ContentMismatchError struct {
	Field     string // "size" or "checksum"
	Want, Got string
}

func (e *ContentMismatchError) Error() string {
	return fmt.Sprintf("%v: %s is %s, registered %s", ErrContentMismatch, e.Field, e.Got, e.Want)
}

func (e *ContentMismatchError) Is(target error) bool { return target == ErrContentMismatch }

// FailedContentMismatch is the reason an object is failed with once the
// bytes that broke its registration have been deleted.
const FailedContentMismatch = "content mismatch"

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
	if err := verifyRegistration(ctx, exec, objectID, sizeBytes, checksum, sequencer); err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	// Two promote regimes, split by whether the caller carries a sequencer:
	//
	//   - sequencer == '' (HEAD-driven: CompleteObject RPC, CopyObject, the
	//     reconciler): a *first* promote only. It matches PENDING rows. An
	//     already-AVAILABLE row is left untouched (changed=false) — so a
	//     storage event that promoted the row first keeps its authoritative
	//     etag/size, and a duplicate RPC can't re-charge quota or overwrite
	//     values the nightly accounting already counted (the HEAD→promote
	//     race this closes).
	//   - sequencer != '' (storage event): may also UPDATE an AVAILABLE row,
	//     but only with a strictly newer sequencer (stale events are no-ops).
	const q = `
        UPDATE objects
           SET state = 'AVAILABLE',
               etag = COALESCE(NULLIF($2, ''), etag),
               size_bytes = CASE WHEN $3 > 0 THEN $3 ELSE size_bytes END,
               checksum = COALESCE(NULLIF($4, ''), checksum),
               sequencer = COALESCE(NULLIF($5, ''), sequencer),
               committed_at = CASE WHEN state = 'PENDING' THEN now() ELSE committed_at END
         WHERE id = $1
           AND (
                ($5 = '' AND state = 'PENDING')
                OR ($5 <> '' AND state IN ('PENDING', 'AVAILABLE')
                    AND (sequencer IS NULL OR $5 > sequencer))
           )
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
	recordTransition(ctx, string(source), "AVAILABLE")
	return true, nil
}

// verifyRegistration holds a PENDING object's first promote to what it was
// registered with. The size and checksum the upload was admitted with are
// what policy, quota and the presigned URL were checked against; promoting
// bytes that differ would make every one of those checks about something
// else. The URL already binds both, so a mismatch here means the object store
// did not enforce the binding — this is the backstop that holds whatever the
// backend does.
//
// It locks the row, so it must run inside the promote's transaction. An
// object that is not PENDING is past its first promote and is not checked.
// A storage event (sequencer set) reporting size 0 is treated as not
// reporting a size: event sources omit it, and refusing on a zero they did
// not mean would strand the object until the reconciler HEADs it.
func verifyRegistration(ctx context.Context, exec dbExec, objectID uuid.UUID, sizeBytes int64, checksum, sequencer string) error {
	const q = `
        SELECT state, size_bytes, checksum
          FROM objects
         WHERE id = $1
         FOR UPDATE
    `
	var (
		state      string
		registered *int64
		expected   *string
	)
	if err := exec.QueryRow(ctx, q, objectID).Scan(&state, &registered, &expected); err != nil {
		if isNoRows(err) {
			return err
		}
		return fmt.Errorf("sm: lock for promote: %w", err)
	}
	if State(state) != StatePending {
		return nil
	}
	sizeReported := sequencer == "" || sizeBytes > 0
	if registered != nil && sizeReported && *registered != sizeBytes {
		return &ContentMismatchError{Field: "size", Want: fmt.Sprint(*registered), Got: fmt.Sprint(sizeBytes)}
	}
	if expected != nil && *expected != "" && checksum != "" && *expected != checksum {
		return &ContentMismatchError{Field: "checksum", Want: *expected, Got: checksum}
	}
	return nil
}

// MarkFailed transitions PENDING → FAILED after the presign TTL has
// elapsed with no confirming event/RPC. Idempotent.
func (t *Transitioner) MarkFailed(ctx context.Context, objectID uuid.UUID, reason string) error {
	const q = `
        UPDATE objects
           SET state = 'FAILED',
               terminated_at = now()
         WHERE id = $1 AND state = 'PENDING'
    `
	_, err := t.pool.Exec(ctx, q, objectID)
	if err != nil {
		return fmt.Errorf("sm: mark failed: %w", err)
	}
	recordTransition(ctx, string(SourceReconciler), "FAILED")
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
         WHERE id = $1
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
         WHERE id = $1 AND state = 'DELETED'
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
        SELECT id
          FROM objects
         WHERE state = 'PENDING'
           AND presign_expires_at IS NOT NULL
           AND presign_expires_at < now() - $1::interval
           -- A tenant in the trash is frozen: its uploads are settled on restore.
           AND NOT EXISTS (SELECT 1 FROM tenants t
                            WHERE t.id = objects.tenant_id AND t.deleted_at IS NOT NULL)
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

// PendingOverdue reports the PENDING objects the reconciler should already
// have settled — their presign TTL elapsed more than graceBeyond ago — and
// how long past that deadline the oldest of them is. Zero and zero when there
// are none. ScanPendingExpired picks from the same rows, oldest first, so a
// reconciler keeping up holds the age near its poll interval; one that is not
// — every HEAD failing, a backlog past its batch size — lets it grow.
func (t *Transitioner) PendingOverdue(ctx context.Context, graceBeyond time.Duration) (count int64, oldest time.Duration, err error) {
	const q = `
        SELECT count(*),
               COALESCE(EXTRACT(EPOCH FROM (now() - $1::interval) - min(presign_expires_at)), 0)::float8
          FROM objects
         WHERE state = 'PENDING'
           AND presign_expires_at IS NOT NULL
           AND presign_expires_at < now() - $1::interval
    `
	var seconds float64
	if err := t.pool.QueryRow(ctx, q, graceBeyond.String()).Scan(&count, &seconds); err != nil {
		return 0, 0, fmt.Errorf("sm: pending overdue: %w", err)
	}
	return count, time.Duration(seconds * float64(time.Second)), nil
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

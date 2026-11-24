package statemachine

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/bucketstate"
	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
)

// LateSourceWrite is a write a storage event reported in a storage
// migration's source bucket after the migration rebound the object's
// collection to its target (ADR-0015).
//
// In an explicit-mode bucket the storage event is what records an overwrite.
// One written to the source while the migration copied, behind the copy's
// cursor, and delivered after the rebind names a bucket the collection is no
// longer bound to: the catch-up never saw it, the target keeps the older
// bytes, and the cleanup deletes the newer ones with the source. Such a write
// is moved into the target and promoted there instead.
//
// The mirror holds after a rollback: an overwrite written to the target while
// the migration rolled back, whose event is delivered after the rebind back,
// is moved into the source. "Source" below is the bucket the collection was
// moved away from, whichever way.
type LateSourceWrite struct {
	// FromBucket is the bucket the event came from.
	FromBucket string
	// To is the bucket the collection is bound to, as the caller read it;
	// the promote checks it is still bound there.
	To Placement
	// Sequencer is the event's, which orders it against the object's
	// earlier source writes.
	Sequencer string
	// WrittenAt is when the event's store reported the write (the event's
	// time); zero when it did not say, or when the time is the ingest's own
	// clock. A write moved back after a rollback must postdate the rebind by
	// LateWriteClockMargin: the target received the migration's own copies
	// before it, and their events are no tenant's write.
	WrittenAt time.Time
	// Move copies the object's bytes from the migration's source into To,
	// under the same key, and reports what To then holds. A source that no
	// longer holds them is ErrLateWriteGone.
	Move func(ctx context.Context, from, to Placement) (MovedBytes, error)
}

// MovedBytes is what the bucket a write was moved into holds of it.
type MovedBytes struct {
	ETag string
	Size int64
}

// LateWriteClockMargin is how far past the rebind a target write's reported
// time must be for it to be moved back after a rollback. The time is the
// store's clock and rebound_at the database's: within the margin, a catch-up
// copy made just before the rebind cannot be told from a tenant's write made
// just after it, and the move would put a migration's copy, perhaps the one
// its verify failed on, over the source. A tenant write that close to the
// rebind is skipped and logged instead.
const LateWriteClockMargin = time.Minute

// ErrNotMigratedFrom is a late write whose bucket is not the source of a
// storage migration that rebound the object's collection to the bucket it is
// bound to now, nor the target of one whose rollback rebound it back: the
// same path in an unrelated bucket is another object.
var ErrNotMigratedFrom = errors.New("the event's bucket is not one a migration moved the collection away from")

// ErrLateWriteGone is a late write whose bytes the migration's source no
// longer holds: its cleanup deleted them, or a later write replaced them.
var ErrLateWriteGone = errors.New("the storage migration's source no longer holds the object's bytes")

// ErrLateWriteSkipped is a late write that is not moved: the object is not
// one an overwrite applies to, or the target already holds a newer write.
// Match with errors.Is; LateWriteSkippedError says why.
var ErrLateWriteSkipped = errors.New("late source write not moved")

// The reasons a late source write is skipped.
const (
	// LateSkipNotAvailable: only an AVAILABLE object is overwritten. A
	// PENDING upload is anchored to the bucket its URL names and settled
	// from there (CompleteObject, the reconciler); a trashed one is not
	// written by an event.
	LateSkipNotAvailable = "the object is not available"
	// LateSkipNoSequencer: an event without a sequencer cannot be ordered
	// against the object's earlier writes.
	LateSkipNoSequencer = "the event carries no sequencer"
	// LateSkipNotNewer: the object already records a write the event does
	// not follow.
	LateSkipNotNewer = "the event is not newer than the object's last source write"
	// LateSkipWrittenSinceRebind: the object was written in the target
	// since the rebind, which is later than anything written to the source.
	// After a rollback: written in the source since the rebind back.
	LateSkipWrittenSinceRebind = "the object was written since the rebind"
	// LateSkipNoEventTime: a target event after a rollback whose store did
	// not say when the write was made cannot be told from the migration's
	// own copy.
	LateSkipNoEventTime = "the store did not say when the target was written"
	// LateSkipBeforeRebind: a target event after a rollback that reports a
	// write from before the rebind, or within LateWriteClockMargin after it,
	// may report the migration's own copy, perhaps the one its verify failed
	// on, rather than a tenant's write.
	LateSkipBeforeRebind = "the event reports a target write from before the rebind, or too close after it to tell"
)

// LateWriteSkippedError is ErrLateWriteSkipped with its reason.
type LateWriteSkippedError struct{ Reason string }

func (e *LateWriteSkippedError) Error() string {
	return fmt.Sprintf("%v: %s", ErrLateWriteSkipped, e.Reason)
}

func (e *LateWriteSkippedError) Is(target error) bool { return target == ErrLateWriteSkipped }

// lateMove is the storage migration a late write is moved under: the bucket
// it moved the collection away from, and when.
type lateMove struct {
	from Placement
	// movedAt is when the collection left from: the rebind, or the rebind
	// back.
	movedAt time.Time
	// reboundAt is the rebind's time, which a write moved back must follow.
	reboundAt time.Time
	// back: the rollback's rebind back moved the collection, from the target.
	back bool
}

// lateRow is the object row a late write is decided on.
type lateRow struct {
	state     State
	writtenAt *time.Time
	// follows: the event's sequencer is past the row's, compared as the
	// promote compares them, in SQL.
	follows bool
}

// PromoteLateSourceWriteInTx moves a late source write into the collection's
// bucket and promotes it there, on one transaction, running onPromoted on it
// when the promote changed the row, as PromoteToAvailableInTx does.
//
// The object's row is held (FOR UPDATE) from the decision to the promote, and
// the migration's fence (checkPlacement) with it, so no other promote of the
// object, and no rebind back, interleaves with the copy. The write is moved
// only when nothing newer is recorded: the object is AVAILABLE, the event's
// sequencer follows the row's, and the row's last write predates the rebind.
// A write recorded since the rebind went to the target, later than anything
// written to the source, and the copy would put older bytes over it. After a
// rollback the same holds of the rebind back, and the event must also report
// a write made after the rebind (lateSkipReason). Anything else is a
// *LateWriteSkippedError, and nothing changes.
//
// No row: purged since the event was matched; nothing to do.
func (t *Transitioner) PromoteLateSourceWriteInTx(
	ctx context.Context,
	objectID uuid.UUID,
	w LateSourceWrite,
	onPromoted func(ctx context.Context, tx pgx.Tx) error,
) (changed bool, err error) {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("sm: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	row, err := lockLateRow(ctx, tx, objectID, w.Sequencer)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := checkPlacement(ctx, tx, objectID, &w.To); err != nil {
		return false, err
	}
	mv, err := migratedFrom(ctx, tx, objectID, w.FromBucket, w.To)
	if err != nil {
		return false, err
	}
	if reason := lateSkipReason(row, w, mv); reason != "" {
		return false, &LateWriteSkippedError{Reason: reason}
	}
	moved, err := w.Move(ctx, mv.from, w.To)
	if err != nil {
		return false, err
	}
	changed, err = t.promote(ctx, tx, objectID, &w.To, moved.ETag, moved.Size, "", w.Sequencer, SourceEvent)
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

// lockLateRow holds the object's row for the rest of the transaction and
// reads what a late write with sequencer is decided on.
func lockLateRow(ctx context.Context, exec dbExec, objectID uuid.UUID, sequencer string) (lateRow, error) {
	const q = `
        SELECT state, bytes_written_at, (sequencer IS NULL OR $2 > sequencer)
          FROM objects
         WHERE id = $1
         FOR UPDATE
    `
	var r lateRow
	var state string
	if err := exec.QueryRow(ctx, q, objectID, sequencer).Scan(&state, &r.writtenAt, &r.follows); err != nil {
		if isNoRows(err) {
			return lateRow{}, err
		}
		return lateRow{}, fmt.Errorf("sm: lock for a late write: %w", err)
	}
	r.state = State(state)
	return r, nil
}

// migratedFrom finds the tenant's storage migration that moved the object's
// collection away from fromBucket, into the bucket it is bound to now (to),
// while fromBucket still holds what it held:
// one that rebound it from its source (migstate.ReboundToTarget), or one
// whose rollback rebound it back from its target (migstate.ReboundBackToSource,
// rebound_back_at set, migration 119) while that target is still a bucket
// that exists (bucketstate.Ready): one deleted, or being deleted, ends the
// window. None is ErrNotMigratedFrom.
func migratedFrom(ctx context.Context, exec dbExec, objectID uuid.UUID, fromBucket string, to Placement) (lateMove, error) {
	const q = `
        SELECT from_backend, from_bucket, moved_at, rebound_at, back
          FROM (
                SELECT ssb.name AS from_backend, sb.name AS from_bucket,
                       m.rebound_at AS moved_at, m.rebound_at, false AS back, m.created_at
                  FROM tenant_storage_migrations m
                  JOIN buckets sb           ON sb.id = m.source_bucket_id
                  JOIN storage_backends ssb ON ssb.id = sb.backend_id
                  JOIN buckets tb           ON tb.id = m.target_bucket_id
                  JOIN storage_backends tsb ON tsb.id = tb.backend_id
                 WHERE m.tenant_id = (SELECT o.tenant_id FROM objects o WHERE o.id = $1)
                   AND sb.name = $2 AND tsb.name = $3 AND tb.name = $4
                   AND m.state = ANY($5::text[])
                   AND m.rebound_at IS NOT NULL
                UNION ALL
                SELECT tsb.name, tb.name, m.rebound_back_at, m.rebound_at, true, m.created_at
                  FROM tenant_storage_migrations m
                  JOIN buckets sb           ON sb.id = m.source_bucket_id
                  JOIN storage_backends ssb ON ssb.id = sb.backend_id
                  JOIN buckets tb           ON tb.id = m.target_bucket_id
                  JOIN storage_backends tsb ON tsb.id = tb.backend_id
                 WHERE m.tenant_id = (SELECT o.tenant_id FROM objects o WHERE o.id = $1)
                   AND tb.name = $2 AND ssb.name = $3 AND sb.name = $4
                   AND m.state = ANY($6::text[])
                   AND m.rebound_at IS NOT NULL AND m.rebound_back_at IS NOT NULL
                   AND tb.provision_state = $7
               ) moves
         ORDER BY created_at DESC
         LIMIT 1
    `
	var mv lateMove
	err := exec.QueryRow(ctx, q, objectID, fromBucket, to.BackendID, to.Bucket,
		migstate.ReboundToTarget(), migstate.ReboundBackToSource(), bucketstate.Ready).
		Scan(&mv.from.BackendID, &mv.from.Bucket, &mv.movedAt, &mv.reboundAt, &mv.back)
	if isNoRows(err) {
		return lateMove{}, ErrNotMigratedFrom
	}
	if err != nil {
		return lateMove{}, fmt.Errorf("sm: read the storage migration: %w", err)
	}
	return mv, nil
}

// lateSkipReason is why a late write must not be moved, "" when it may: see
// PromoteLateSourceWriteInTx.
func lateSkipReason(r lateRow, w LateSourceWrite, mv lateMove) string {
	switch {
	case r.state != StateAvailable:
		return LateSkipNotAvailable
	case w.Sequencer == "":
		return LateSkipNoSequencer
	case mv.back && w.WrittenAt.IsZero():
		return LateSkipNoEventTime
	case mv.back && w.WrittenAt.Before(mv.reboundAt.Add(LateWriteClockMargin)):
		return LateSkipBeforeRebind
	case r.writtenAt != nil && !r.writtenAt.Before(mv.movedAt):
		return LateSkipWrittenSinceRebind
	case !r.follows:
		return LateSkipNotNewer
	}
	return ""
}

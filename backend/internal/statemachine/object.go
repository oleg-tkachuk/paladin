// Package statemachine owns the lifecycle transitions for Object.
//
// The simplified automaton:
//
//	PENDING ─(event|rpc|reconciler)→ AVAILABLE
//	PENDING ─(presign expired + no upload, bad bytes, failed copy, abort)→ ∅ (row removed, reason logged)
//	AVAILABLE ─(DeleteObject soft)→ DELETED
//	AVAILABLE ─(storage delete event, bytes gone)→ DELETED
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
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// State mirrors the object_state enum in SQL.
type State string

const (
	StatePending   State = "PENDING"
	StateAvailable State = "AVAILABLE"
	StateDeleted   State = "DELETED"
)

// Source records which signal triggered a transition. Exported for metrics.
type Source string

const (
	SourceEvent      Source = "event"
	SourceRPC        Source = "rpc"
	SourceReconciler Source = "reconciler"
)

// Transitioner owns PENDING → AVAILABLE, the discarding of a PENDING object
// that failed, and DELETED.
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
	placed *Placement,
	etag string,
	sizeBytes int64,
	checksum string,
	sequencer string,
	source Source,
) (changed bool, err error) {
	// In a transaction even with nothing to run after it: the check against
	// what the object was registered with locks the row, and the lock has to
	// last until the promote that depends on it.
	return t.PromoteToAvailableInTx(ctx, objectID, placed, etag, sizeBytes, checksum, sequencer, source, nil)
}

// Placement is the bucket a promote's caller confirmed the object's bytes
// in: where it HEADed them, assembled them or copied them to.
//
// An object's bytes reach a bucket only through a URL Paladin signed for it
// or a copy Paladin made, and the row records that bucket
// (objects.upload_bucket_id, its anchor). So the promote places the object
// in its anchor, and refuses bytes confirmed anywhere else: they are not the
// row's (ADR-0051). A storage migration or an operator may have pointed the
// collection at another bucket since; the binding decides only where the
// next upload is signed.
//
// A nil *Placement names no bucket: a storage event whose source reports
// none. The bytes are then taken to be in the anchor, the one bucket a URL
// of the row can write.
type Placement struct {
	BackendID string
	Bucket    string
	// Settled is a promote of bytes Paladin wrote itself, a copy: no URL of
	// the row can write the key again, so the anchor is settled with the
	// promote rather than when a URL expires.
	Settled bool
}

// ErrNotAnchored is a promote refused because the bytes it confirms are not
// in the bucket the object's upload is anchored to, or the object has no
// anchor left to place a first upload in. Nothing changed. Match with
// errors.Is.
var ErrNotAnchored = errors.New("the bytes are not in the bucket the object's upload is anchored to")

// NotAnchoredError names the bucket of an ErrNotAnchored and the anchor,
// empty when the object has none.
type NotAnchoredError struct {
	Placed Placement
	Anchor Placement
}

func (e *NotAnchoredError) Error() string {
	return fmt.Sprintf("%v: bytes in %s/%s, anchored to %q/%q",
		ErrNotAnchored, e.Placed.BackendID, e.Placed.Bucket, e.Anchor.BackendID, e.Anchor.Bucket)
}

func (e *NotAnchoredError) Is(target error) bool { return target == ErrNotAnchored }

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

// FailureReason says why an object never became AVAILABLE. It is what the log
// line and the failure metric carry once the row is gone (DiscardFailed).
type FailureReason string

const (
	// FailedPresignExpired: the upload URL expired and the reconciler found
	// no bytes at the object's path.
	FailedPresignExpired FailureReason = "presign_expired"
	// FailedContentMismatch: the bytes that arrived broke the object's
	// registration (size or checksum), and were deleted.
	FailedContentMismatch FailureReason = "content_mismatch"
	// FailedStorageCopy: a copy into the object's path failed in storage.
	FailedStorageCopy FailureReason = "storage_copy_failed"
	// FailedMultipartAborted: the multipart upload was aborted.
	FailedMultipartAborted FailureReason = "multipart_aborted"
)

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
	placed *Placement,
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

	changed, err = t.promote(ctx, tx, objectID, placed, etag, sizeBytes, checksum, sequencer, source)
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
	placed *Placement,
	etag string,
	sizeBytes int64,
	checksum string,
	sequencer string,
	source Source,
) (changed bool, err error) {
	reg, err := lockRegistration(ctx, exec, objectID)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := reg.check(sizeBytes, checksum, sequencer); err != nil {
		return false, err
	}
	if !reg.anchored() && State(reg.state) != StatePending {
		// Settled: every URL of the row has expired and the reconciler has
		// read what the last one wrote. A later report of a write is stale.
		return false, nil
	}
	if err := reg.anchoredAt(placed); err != nil {
		return false, err
	}
	settled := placed != nil && placed.Settled
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
	//     but only with a strictly newer sequencer (stale events are no-ops),
	//     and only while its anchor is unsettled — a URL of the row can still
	//     write — so the sequencer it is compared with came from the same
	//     bucket.
	//
	// Either way a promote that applies records bytes written under the key
	// (bytes_written_at), the content version a storage migration's move
	// compares before it moves the object. It places the object in its
	// anchor (placement_bucket_id, ADR-0051), and keeps the anchor until the
	// URL can no longer write — the reconciler settles it then — unless the
	// bytes are Paladin's own copy, which no URL can write again.
	const q = `
        UPDATE objects
           SET state = 'AVAILABLE',
               etag = COALESCE(NULLIF($2, ''), etag),
               size_bytes = CASE WHEN $3 > 0 THEN $3 ELSE size_bytes END,
               checksum = COALESCE(NULLIF($4, ''), checksum),
               sequencer = COALESCE(NULLIF($5, ''), sequencer),
               committed_at = CASE WHEN state = 'PENDING' THEN now() ELSE committed_at END,
               bytes_written_at = clock_timestamp(),
               placement_bucket_id = upload_bucket_id,
               upload_bucket_id = CASE WHEN $6 THEN NULL ELSE upload_bucket_id END
         WHERE id = $1
           AND upload_bucket_id IS NOT NULL
           AND (
                ($5 = '' AND state = 'PENDING')
                OR ($5 <> '' AND state IN ('PENDING', 'AVAILABLE')
                    AND (sequencer IS NULL OR $5 > sequencer))
           )
        RETURNING state
    `
	var newState string
	err = exec.QueryRow(ctx, q, objectID, etag, sizeBytes, checksum, sequencer, settled).Scan(&newState)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, fmt.Errorf("sm: promote: %w", err)
	}
	recordTransition(ctx, string(source), "AVAILABLE")
	return true, nil
}

// registration is what a promote is decided on, read under the row's lock:
// the object's state, the size and checksum it was registered with, and the
// bucket its upload is anchored to (by name; nil once settled).
type registration struct {
	state         string
	size          *int64
	checksum      *string
	anchorBackend *string
	anchorBucket  *string
}

// lockRegistration locks the object's row for the rest of the promote's
// transaction and reads its registration. It must run inside that
// transaction.
func lockRegistration(ctx context.Context, exec dbExec, objectID uuid.UUID) (registration, error) {
	const q = `
        SELECT o.state, o.size_bytes, o.checksum, sb.name, b.name
          FROM objects o
          LEFT JOIN buckets b           ON b.id = o.upload_bucket_id
          LEFT JOIN storage_backends sb ON sb.id = b.backend_id
         WHERE o.id = $1
         FOR UPDATE OF o
    `
	var r registration
	if err := exec.QueryRow(ctx, q, objectID).Scan(&r.state, &r.size, &r.checksum, &r.anchorBackend, &r.anchorBucket); err != nil {
		if isNoRows(err) {
			return registration{}, err
		}
		return registration{}, fmt.Errorf("sm: lock for promote: %w", err)
	}
	return r, nil
}

// check holds a PENDING object's first promote to what it was registered
// with. The size and checksum the upload was admitted with are what policy,
// quota and the presigned URL were checked against; promoting bytes that
// differ would make every one of those checks about something else. The URL
// already binds both, so a mismatch here means the object store did not
// enforce the binding — this is the backstop that holds whatever the backend
// does.
//
// An object that is not PENDING is past its first promote and is not
// checked. A storage event (sequencer set) reporting size 0 is treated as not
// reporting a size: event sources omit it, and refusing on a zero they did
// not mean would strand the object until the reconciler HEADs it.
func (r registration) check(sizeBytes int64, checksum, sequencer string) error {
	if State(r.state) != StatePending {
		return nil
	}
	sizeReported := sequencer == "" || sizeBytes > 0
	if r.size != nil && sizeReported && *r.size != sizeBytes {
		return &ContentMismatchError{Field: "size", Want: fmt.Sprint(*r.size), Got: fmt.Sprint(sizeBytes)}
	}
	if r.checksum != nil && *r.checksum != "" && checksum != "" && *r.checksum != checksum {
		return &ContentMismatchError{Field: "checksum", Want: *r.checksum, Got: checksum}
	}
	return nil
}

// anchored reports whether a URL of the row may still write its key.
func (r registration) anchored() bool {
	return r.anchorBackend != nil && r.anchorBucket != nil
}

// anchoredAt refuses bytes confirmed anywhere but the row's anchor, and a
// first upload with no anchor to place it in. A nil placed names no bucket
// and is taken to be the anchor.
func (r registration) anchoredAt(placed *Placement) error {
	if !r.anchored() {
		return &NotAnchoredError{Placed: derefPlacement(placed)}
	}
	if placed == nil || (placed.BackendID == *r.anchorBackend && placed.Bucket == *r.anchorBucket) {
		return nil
	}
	return &NotAnchoredError{Placed: *placed, Anchor: Placement{BackendID: *r.anchorBackend, Bucket: *r.anchorBucket}}
}

// derefPlacement is *p, or none.
func derefPlacement(p *Placement) Placement {
	if p == nil {
		return Placement{}
	}
	return *p
}

// DiscardFailed deletes a PENDING object that will never become AVAILABLE, and
// records why in the log and in paladin_object_failures_total. Idempotent: a
// row that is gone, or no longer PENDING, is left alone.
//
// The row is deleted rather than kept as FAILED. A failed row told nobody
// anything — every path that fails an object answers its caller with the error
// already — and it held its key: (collection_id, path) is unique across every
// state, so a retry at the same key was refused until someone deleted it.
//
// No bytes are left behind to purge: a size or checksum mismatch deletes them
// before calling this, a failed storage copy writes nothing, an aborted
// multipart upload removes its parts, and the reconciler fails a row only when
// HEAD found nothing at its path.
func (t *Transitioner) DiscardFailed(ctx context.Context, objectID uuid.UUID, reason FailureReason) error {
	const q = `
        DELETE FROM objects
         WHERE id = $1 AND state = 'PENDING'
     RETURNING tenant_id, path
    `
	var tenantID uuid.UUID
	var path string
	err := t.pool.QueryRow(ctx, q, objectID).Scan(&tenantID, &path)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sm: discard failed object: %w", err)
	}
	recordFailure(ctx, reason)
	logger.FromContext(ctx).Warn("object failed and was discarded",
		zap.String("object_id", objectID.String()),
		zap.String("tenant_id", tenantID.String()),
		zap.String("key", path),
		zap.String("reason", string(reason)))
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
// (recording the event) in one transaction. If `do` errors (incl. the
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

// ExpiredAnchor is an object ScanExpiredAnchors found anchored to a bucket
// whose URL has expired by the grace: a PENDING upload to settle by its
// bytes, or a promoted object whose URL could still have written after its
// promote. It carries the anchor — the bucket the reconciler HEADs — and the
// state the bucket gate decides on there.
type ExpiredAnchor struct {
	ObjectID       uuid.UUID
	State          State
	BucketID       pgtype.UUID
	BackendName    string
	BucketName     string
	Enabled        bool
	ReadOnly       bool
	ProvisionState string
}

// ScanExpiredAnchors returns up to `limit` objects anchored to a bucket
// (objects.upload_bucket_id) whose presign TTL has elapsed by `graceBeyond`,
// oldest deadline first, leaving out the objects in the buckets of deferred.
// Consumed by the reconciler, which asks the bucket gate about each row's
// bucket and scans again without the buckets it refused, so their rows never
// head every scan.
func (t *Transitioner) ScanExpiredAnchors(
	ctx context.Context,
	graceBeyond time.Duration,
	limit int,
	deferred []pgtype.UUID,
) ([]ExpiredAnchor, error) {
	const q = `
        SELECT o.id, o.state, b.id, sb.name, b.name, sb.enabled, sb.read_only, b.provision_state
          FROM objects o
          JOIN buckets b           ON b.id = o.upload_bucket_id
          JOIN storage_backends sb ON sb.id = b.backend_id
         WHERE o.upload_bucket_id IS NOT NULL
           AND o.presign_expires_at IS NOT NULL
           AND o.presign_expires_at < now() - $1::interval
           -- A tenant in the trash is frozen: its uploads are settled on restore.
           AND NOT EXISTS (SELECT 1 FROM tenants t
                            WHERE t.id = o.tenant_id AND t.deleted_at IS NOT NULL)
           -- Not the buckets the gate refused earlier in this tick.
           AND NOT (b.id = ANY($3::uuid[]))
         ORDER BY o.presign_expires_at
         LIMIT $2
    `
	if deferred == nil {
		deferred = []pgtype.UUID{}
	}
	rows, err := t.pool.Query(ctx, q, graceBeyond.String(), limit, deferred)
	if err != nil {
		return nil, fmt.Errorf("sm: scan: %w", err)
	}
	defer rows.Close()
	var out []ExpiredAnchor
	for rows.Next() {
		var p ExpiredAnchor
		var state string
		if err := rows.Scan(&p.ObjectID, &state, &p.BucketID, &p.BackendName, &p.BucketName,
			&p.Enabled, &p.ReadOnly, &p.ProvisionState); err != nil {
			return nil, err
		}
		p.State = State(state)
		out = append(out, p)
	}
	return out, rows.Err()
}

// SettleOutcome is what settling an anchor found and did (SettleAnchorInTx).
type SettleOutcome string

const (
	// SettleUnchanged: the anchor holds the bytes the row records.
	SettleUnchanged SettleOutcome = "unchanged"
	// SettleRecorded: the anchor holds other bytes, written after the last
	// promote and reported by no event; the row now records them.
	SettleRecorded SettleOutcome = "recorded"
	// SettleMissing: the anchor holds nothing under the key.
	SettleMissing SettleOutcome = "missing"
	// SettleSkipped: the row is gone, PENDING, or anchored elsewhere since
	// it was scanned; nothing changed.
	SettleSkipped SettleOutcome = "skipped"
)

// StoredWrite is what a HEAD of an object's anchor found: whether anything is
// there, and its ETag, size and checksum.
type StoredWrite struct {
	Found     bool
	ETag      string
	SizeBytes int64
	Checksum  string
}

// SettleAnchorInTx settles the anchor of a promoted object whose URL has
// expired (ADR-0051): after it, no URL of the row can write its key, so
// what the anchor holds now is the last write. found is what a HEAD of the
// anchor, named by anchor, read after the expiry.
//
// The same ETag clears the anchor. Another one is a write through the URL
// after the last promote that no storage event recorded — an event lost, or
// a store that did not enforce the URL's binding — and the last promoted
// wins: the row records those bytes, as a promote would, its content version
// moves, and onRecorded runs on the transaction (the caller's event). No
// bytes at all clear the anchor and change nothing else: a delete straight
// from the bucket is a storage delete event's to report.
//
// The row is held for the transaction, and only one still anchored to anchor
// and promoted is settled; anything else is SettleSkipped.
func (t *Transitioner) SettleAnchorInTx(
	ctx context.Context,
	objectID uuid.UUID,
	anchor Placement,
	found StoredWrite,
	onRecorded func(ctx context.Context, tx pgx.Tx) error,
) (SettleOutcome, error) {
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return SettleSkipped, fmt.Errorf("sm: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	reg, err := lockRegistration(ctx, tx, objectID)
	if isNoRows(err) {
		return SettleSkipped, nil
	}
	if err != nil {
		return SettleSkipped, err
	}
	if State(reg.state) == StatePending || !reg.anchored() ||
		*reg.anchorBackend != anchor.BackendID || *reg.anchorBucket != anchor.Bucket {
		return SettleSkipped, nil
	}
	outcome, err := settle(ctx, tx, objectID, found)
	if err != nil {
		return SettleSkipped, err
	}
	if outcome == SettleRecorded && onRecorded != nil {
		if err := onRecorded(ctx, tx); err != nil {
			return SettleSkipped, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SettleSkipped, fmt.Errorf("sm: commit tx: %w", err)
	}
	if outcome == SettleRecorded {
		recordTransition(ctx, string(SourceReconciler), string(StateAvailable))
	}
	return outcome, nil
}

// settle clears the anchor of the row held on exec, recording found first
// when it is a write the row does not record.
func settle(ctx context.Context, exec dbExec, objectID uuid.UUID, found StoredWrite) (SettleOutcome, error) {
	const clear = `UPDATE objects SET upload_bucket_id = NULL WHERE id = $1`
	if !found.Found {
		if _, err := exec.Exec(ctx, clear, objectID); err != nil {
			return SettleSkipped, fmt.Errorf("sm: settle anchor: %w", err)
		}
		return SettleMissing, nil
	}
	// ETags are compared with the ones the same bucket reported: the row's
	// was recorded from the anchor, where the object is placed until it
	// settles (a storage migration moves only settled objects).
	const record = `
        UPDATE objects
           SET upload_bucket_id = NULL,
               etag = $2,
               size_bytes = $3,
               checksum = COALESCE(NULLIF($4, ''), checksum),
               bytes_written_at = clock_timestamp(),
               placement_bucket_id = upload_bucket_id
         WHERE id = $1
           AND btrim(etag, '"') IS DISTINCT FROM $2
        RETURNING id
    `
	var id uuid.UUID
	err := exec.QueryRow(ctx, record, objectID, NormalizeETag(found.ETag), found.SizeBytes, found.Checksum).Scan(&id)
	if err == nil {
		return SettleRecorded, nil
	}
	if !isNoRows(err) {
		return SettleSkipped, fmt.Errorf("sm: record the anchor's write: %w", err)
	}
	if _, err := exec.Exec(ctx, clear, objectID); err != nil {
		return SettleSkipped, fmt.Errorf("sm: settle anchor: %w", err)
	}
	return SettleUnchanged, nil
}

// NormalizeETag is an ETag as rows record it: without the quotes S3 puts
// around it on the wire.
func NormalizeETag(etag string) string {
	return strings.Trim(etag, `"`)
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
	ErrConflict = errors.New("object version conflict")
	ErrNotFound = errors.New("object not found or not in the expected state")
)

// isNoRows checks the pgx-specific no-rows error. errors.Is (not a
// string compare) so wrapped sentinels — fmt.Errorf("...: %w",
// pgx.ErrNoRows) — still classify as no-rows instead of surfacing as
// false errors that re-queue the object forever.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

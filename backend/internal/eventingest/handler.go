package eventingest

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// ObjectLookup is the slice of *sqlc.Queries the handler uses to
// resolve (tenant, collection, key) → object_id. Decoupled into an
// interface so the handler is testable without a live DB.
type ObjectLookup interface {
	LookupObjectByKey(
		ctx context.Context,
		tenantID pgtype.UUID,
		collection, key string,
	) (sqlc.LookupObjectByKeyRow, error)
	// GetCollection resolves a collection row (for its backend_id +
	// bucket_name binding) so the emitted event carries the canonical
	// resource name (ADR-0014 Phase 1).
	GetCollection(
		ctx context.Context,
		tenantID pgtype.UUID,
		collection string,
	) (sqlc.GetCollectionRow, error)
}

// PromoteHandler is the canonical event handler: it resolves the
// referenced object and promotes PENDING → AVAILABLE on uploaded
// events. Delete events soft-delete via the same state-machine.
//
// Idempotency: PromoteToAvailable is internally guarded
// (state IN ('PENDING', 'AVAILABLE') AND newer sequencer), so a
// re-delivered event lands the same answer.
//
// Unknown / missing objects are logged at debug and skipped without
// error — the dedup row already records the event_id, so a re-delivery
// is a duplicate-skip on the worker level. This handles the race
// where a presign was issued and the client uploaded before the
// objects-row was committed; reconciler + sequencer guard win
// the race anyway.
// EventProducer is the event-recording seam. nil-safe: when the handler is
// wired without one (e.g. a deployment that doesn't deliver webhooks), the
// promote still happens — it just enqueues no `paladin.object.uploaded` rows.
// Only the tx variant is needed: the event is written on the promote tx.
type EventProducer interface {
	RecordTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) error
}

// Promoter is the slice of *statemachine.Transitioner the handler uses.
type Promoter interface {
	PromoteToAvailableInTx(ctx context.Context, objectID uuid.UUID, placed *statemachine.Placement, etag string, sizeBytes int64, checksum, sequencer string, source statemachine.Source, onPromoted func(ctx context.Context, tx pgx.Tx) error) (bool, error)
}

// StorageProbe HEADs an object's bytes where its row places them now.
// Satisfied by *adapters.ReconcilerProbe.
type StorageProbe interface {
	HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, placed statemachine.Placement, found bool, err error)
}

// LatePromoter is the slice of *statemachine.Transitioner that promotes a
// write reported in a storage migration's source after the migration
// rebound the collection (statemachine.LateSourceWrite).
type LatePromoter interface {
	PromoteLateSourceWriteInTx(ctx context.Context, objectID uuid.UUID, w statemachine.LateSourceWrite, onPromoted func(ctx context.Context, tx pgx.Tx) error) (bool, error)
}

// SourceMover copies an object's bytes between two buckets under the same
// key and reports what the second then holds; a first that holds nothing is
// statemachine.ErrLateWriteGone. Satisfied by *adapters.ReconcilerProbe.
type SourceMover interface {
	MoveObjectBytes(ctx context.Context, objectID uuid.UUID, from, to statemachine.Placement) (statemachine.MovedBytes, error)
}

// SoftDeleter is the slice of *statemachine.Transitioner a delete event uses.
type SoftDeleter interface {
	SoftDeleteInTx(ctx context.Context, objectID uuid.UUID, resourceVersion int64, onDeleted func(ctx context.Context, tx pgx.Tx) error) error
}

type PromoteHandler struct {
	Lookup       ObjectLookup
	Transitioner Promoter
	Events       EventProducer
	Logger       *zap.Logger
	// Probe and Deleter settle delete events. Without both, a delete event
	// is logged and left alone.
	Probe   StorageProbe
	Deleter SoftDeleter
	// Tenants, when set, skips the events of a tenant in the trash: it is
	// frozen, and the reconciler settles its pending uploads on restore.
	Tenants auth.TenantStateReader
	// Audit records the transitions events make. nil records none.
	Audit AuditWriter
	// Late and Mover promote a write a storage event reports in a storage
	// migration's source after the migration rebound the collection to its
	// target: the bytes are moved into the target and promoted there.
	// Without both, such an event is skipped as from another bucket.
	Late  LatePromoter
	Mover SourceMover
}

func (h *PromoteHandler) Handle(ctx context.Context, ev CloudEvent) error {
	logger := h.log().With(
		zap.String("event_id", ev.ID),
		zap.String("event_type", string(ev.Type)),
		zap.String("source", ev.Source),
	)

	// Subject fields are required for object resolution. A source
	// adapter that produced an event without them has a bug; we
	// log + skip rather than retry forever.
	if ev.SubjectFields.TenantID == "" ||
		ev.SubjectFields.Collection == "" ||
		ev.SubjectFields.Key == "" {
		logger.Warn("event subject is incomplete; skipping",
			zap.String("subject", ev.Subject),
		)
		return nil
	}

	tenantUUID, err := uuid.Parse(ev.SubjectFields.TenantID)
	if err != nil {
		logger.Warn("invalid tenant uuid in subject",
			zap.String("tenant_id", ev.SubjectFields.TenantID),
			zap.Error(err),
		)
		return nil
	}

	if h.Tenants != nil {
		state, err := h.Tenants.TenantState(ctx, tenantUUID)
		if err != nil {
			// Retried, as any other read failure.
			return fmt.Errorf("tenant state: %w", err)
		}
		if state == auth.TenantTrashed {
			logger.Debug("tenant is in the trash; skipping", zap.String("tenant_id", tenantUUID.String()))
			return nil
		}
	}

	tenantPg := pgtype.UUID{Bytes: tenantUUID, Valid: true}

	// The collection occupies one segment of the bucket path, so the source
	// adapters' split is exact (storagepath.Compose). A key no object can have
	// a path of its own under was written by something other than Paladin: no
	// object can own it. A key stored before the boundary's length and
	// control-character rules still names its object.
	collection, key := ev.SubjectFields.Collection, ev.SubjectFields.Key
	if err := storagepath.ValidateStoredKey(key); err != nil {
		logger.Warn("event names a key outside the object key contract; skipping",
			zap.String("tenant_id", ev.SubjectFields.TenantID),
			zap.String("collection", collection),
			zap.Error(err),
		)
		return nil
	}

	placed, bound, skip, err := h.fromAnotherBucket(ctx, logger, tenantPg, collection, ev.SubjectFields.Bucket)
	if skip || err != nil {
		return err
	}

	row, err := h.Lookup.LookupObjectByKey(ctx, tenantPg, collection, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Race window: event arrived before the data-plane PUT
			// committed the objects row. Skip; reconciler picks up
			// any orphans and a re-delivery would still find no row.
			logger.Debug("no matching object for event; skipping",
				zap.String("tenant_id", ev.SubjectFields.TenantID),
				zap.String("collection", ev.SubjectFields.Collection),
				zap.String("key", ev.SubjectFields.Key),
			)
			return nil
		}
		return fmt.Errorf("lookup object: %w", err)
	}

	objectID := uuid.UUID(row.Object.ID.Bytes)

	if bound != nil {
		return h.lateSourceWrite(ctx, logger, ev, tenantUUID, objectID, collection, key, *bound)
	}

	switch ev.Type {
	case EventTypeUploaded:
		// Promote + paladin.object.uploaded fan-out in one tx (ADR-0003). In
		// explicit-mode buckets the storage event is what drives the
		// promote, so without this the webhook subscribers would never see
		// the upload — Paladin is their unified notification channel. The
		// `changed` guard keeps it exactly-once across producers: an
		// implicit-mode CompleteObject already fired the event, so a later
		// storage event for the same object promotes to a no-op and emits
		// nothing. A dispatch error rolls the promote back; the worker's
		// at-least-once redelivery re-runs both.
		// Resolve the canonical resource name BEFORE the promote tx — the
		// (backend, bucket) binding read must not run on the pool inside the
		// open tx.
		resourceName := h.objectResourceName(ctx, ev.SubjectFields.TenantID, collection, key)
		changed, err := h.Transitioner.PromoteToAvailableInTx(
			ctx,
			objectID,
			placed,
			ev.SubjectFields.Etag,
			ev.SubjectFields.SizeBytes,
			"", // checksum: storage events typically don't carry it
			ev.SubjectFields.Sequencer,
			statemachine.SourceEvent,
			func(ctx context.Context, tx pgx.Tx) error {
				return h.emitUploaded(ctx, tx, ev, resourceName, collection, key, objectID)
			},
		)
		if errors.Is(err, statemachine.ErrContentMismatch) {
			// The event reports bytes that are not the ones the object was
			// registered with. Retrying cannot change that, and an event
			// alone is not grounds to delete — sources omit or round
			// fields. The row stays PENDING; the reconciler HEADs it once
			// its URL has expired and settles it from the store's answer.
			logger.Warn("event does not match the object's registration; leaving it for the reconciler",
				zap.String("object_id", objectID.String()), zap.Error(err))
			return nil
		}
		if moved, ok := errors.AsType[*statemachine.BindingMovedError](err); ok && h.movesLateWrites() {
			// A storage migration rebound the collection after the event's
			// bucket was checked: the event is a late source write.
			logger.Info("promote refused: the collection was rebound since the event's bucket was checked; moving the write",
				zap.String("object_id", objectID.String()), zap.Error(err))
			return h.lateSourceWrite(ctx, logger, ev, tenantUUID, objectID, collection, key, moved.Bound)
		}
		if errors.Is(err, statemachine.ErrBindingMoved) {
			// A storage migration rebound the collection after the event's
			// bucket was checked: the bytes it reports are in a bucket the
			// object is no longer read from. A PENDING row stays for the
			// reconciler, which moves an upload's bytes from the bucket its
			// URL names; retrying the event would only be refused again.
			logger.Warn("promote refused: the collection was rebound since the event's bucket was checked; skipping",
				zap.String("object_id", objectID.String()), zap.Error(err))
			return nil
		}
		if err != nil {
			return fmt.Errorf("promote object: %w", err)
		}
		logger.Info("promote outcome",
			zap.String("object_id", objectID.String()),
			zap.Bool("changed", changed),
		)
		// A redelivery, or an object a CompleteObject already promoted,
		// changed nothing and is not recorded again.
		if changed {
			h.record(ctx, logger, ev, actionObjectUploaded, resourceName, tenantUUID, objectID)
		}
		return nil

	case EventTypeDeleted:
		return h.settleDeleted(ctx, logger, ev, row.Object, collection, key)

	default:
		logger.Debug("unknown event type; nothing to do")
		return nil
	}
}

// eventSourceStorage marks an event Paladin emits because the storage
// backend reported a change, rather than because a client called the API.
const eventSourceStorage = "storage_event"

// settleDeleted moves an object whose bytes were deleted straight from the
// bucket to the trash. The event is a hint, not proof: the physical key
// carries no object id, so the key may belong to a newer row (a re-upload),
// to a row whose bytes have moved (a storage migration deleting its source
// copy), or to a row Paladin itself is deleting. Only an AVAILABLE row whose
// bytes a HEAD at its current placement cannot find is changed.
//
// It goes to the trash rather than away, so the audit trail keeps it and a
// restore succeeds if the bytes are put back; RestoreObject refuses while
// they are missing. A public object goes to the trash too: the rule against
// trashing one exists because a soft delete leaves its bytes served, and
// these are gone. No delete marker is written in a versioned bucket: the
// versions share the physical key, so the store's delete took them all.
func (h *PromoteHandler) settleDeleted(ctx context.Context, logger *zap.Logger, ev CloudEvent, obj sqlc.Object, collection, key string) error {
	objectID := uuid.UUID(obj.ID.Bytes)
	logger = logger.With(zap.String("object_id", objectID.String()))
	if h.Probe == nil || h.Deleter == nil {
		logger.Info("delete event noted; no storage probe wired")
		return nil
	}
	if obj.State != sqlc.ObjectStateAVAILABLE {
		// A PENDING row's bytes are Paladin's own to discard (the
		// reconciler, a refused multipart), and its delete is no news.
		logger.Debug("delete event for an object that is not available; skipping", zap.String("state", string(obj.State)))
		return nil
	}
	_, _, _, _, _, found, err := h.Probe.HeadByObjectID(ctx, objectID)
	if err != nil {
		// The store could not be asked; a guess either way is wrong.
		return fmt.Errorf("head object: %w", err)
	}
	if found {
		logger.Debug("delete event, but the object's bytes are where its row places them; skipping")
		return nil
	}
	resourceName := h.objectResourceName(ctx, ev.SubjectFields.TenantID, collection, key)
	err = h.Deleter.SoftDeleteInTx(ctx, objectID, obj.ResourceVersion, func(ctx context.Context, tx pgx.Tx) error {
		return h.emitDeleted(ctx, tx, ev, resourceName, collection, key, objectID)
	})
	if err != nil {
		// ErrConflict included: the row changed since it was read, and the
		// next attempt reads it again.
		return fmt.Errorf("soft delete object: %w", err)
	}
	logger.Info("object's bytes are gone from the store; moved to the trash")
	h.record(ctx, logger, ev, actionObjectDeleted, resourceName, uuid.UUID(obj.TenantID.Bytes), objectID)
	return nil
}

// emitDeleted enqueues paladin.object.deleted on the soft-delete tx, with the
// payload the data-plane DeleteObject sends for a soft delete plus the source
// that tells the two apart. nil-safe like emitUploaded.
func (h *PromoteHandler) emitDeleted(ctx context.Context, tx pgx.Tx, ev CloudEvent, resourceName, collection, key string, objectID uuid.UUID) error {
	if h.Events == nil {
		return nil
	}
	err := h.Events.RecordTx(ctx, tx, ev.SubjectFields.TenantID, worker.Event{
		Type:         string(EventTypeDeleted),
		At:           time.Now().UTC(),
		TenantID:     ev.SubjectFields.TenantID,
		ResourceName: resourceName,
		Payload: map[string]any{
			"tenant_id":    ev.SubjectFields.TenantID,
			"collection":   collection,
			"key":          key,
			"object_id":    objectID.String(),
			"mode":         "soft",
			"source":       eventSourceStorage,
			"event_source": ev.Source,
		},
	})
	return err
}

// emitUploaded enqueues the paladin.object.uploaded outbox rows on the promote
// tx. nil-safe: no producer wired → no-op. Mirrors the data-plane
// CompleteObject payload so subscribers can't tell which producer promoted
// the object; the `source: storage_event` discriminator is the only tell.
func (h *PromoteHandler) emitUploaded(ctx context.Context, tx pgx.Tx, ev CloudEvent, resourceName, collection, key string, objectID uuid.UUID) error {
	if h.Events == nil {
		return nil
	}
	// collection/key are the decoded collection and the key of the bucket
	// path; resourceName is those same values canonicalized to A-shape by the
	// caller — so the emitted event references the real collection in the
	// same form the data-plane handler uses.
	err := h.Events.RecordTx(ctx, tx, ev.SubjectFields.TenantID, worker.Event{
		Type:         string(EventTypeUploaded),
		At:           time.Now().UTC(),
		TenantID:     ev.SubjectFields.TenantID,
		ResourceName: resourceName,
		Payload: map[string]any{
			"tenant_id":    ev.SubjectFields.TenantID,
			"collection":   collection,
			"key":          key,
			"object_id":    objectID.String(),
			"size_bytes":   ev.SubjectFields.SizeBytes,
			"etag":         ev.SubjectFields.Etag,
			"source":       eventSourceStorage,
			"event_source": ev.Source,
		},
	})
	return err
}

// fromAnotherBucket reports whether the event's bucket is not the one the
// collection is bound to: the same path in another bucket is another
// object. An event that names no bucket is taken as the collection's. An
// unknown collection is skipped too, as an unknown object is; a failed read
// is returned, so the event is retried.
//
// placed is the bucket the event's bytes are in, which the promote checks the
// collection is still bound to; nil for an event that names no bucket.
//
// When the handler moves late source writes (Late, Mover), an event from
// another bucket is not skipped here: bound is then the collection's bucket,
// and lateSourceWrite decides whether a storage migration rebound the
// collection from the event's bucket.
func (h *PromoteHandler) fromAnotherBucket(ctx context.Context, logger *zap.Logger, tenant pgtype.UUID, collection, bucket string) (placed, bound *statemachine.Placement, skip bool, err error) {
	if bucket == "" {
		return nil, nil, false, nil
	}
	binding, err := h.Lookup.GetCollection(ctx, tenant, collection)
	if errors.Is(err, pgx.ErrNoRows) {
		logger.Debug("no such collection for event; skipping", zap.String("collection", collection))
		return nil, nil, true, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("collection binding: %w", err)
	}
	here := &statemachine.Placement{BackendID: binding.BackendName, Bucket: binding.BucketName}
	if binding.BucketName == bucket {
		return here, nil, false, nil
	}
	if h.movesLateWrites() {
		return nil, here, false, nil
	}
	logger.Warn("event is from a bucket the collection is not bound to; skipping",
		zap.String("collection", collection),
		zap.String("event_bucket", bucket),
		zap.String("collection_bucket", binding.BucketName),
	)
	return nil, nil, true, nil
}

// movesLateWrites reports whether the handler moves late source writes.
func (h *PromoteHandler) movesLateWrites() bool {
	return h.Late != nil && h.Mover != nil
}

// lateSourceWrite handles an event from a bucket the collection is not bound
// to; bound is the one it is. When a storage migration rebound the
// collection from the event's bucket and keeps its source still, an uploaded
// event reports a write that landed in the source while the migration copied
// and whose event came after the rebind: its bytes are moved into bound and
// promoted there (statemachine.LateSourceWrite), notifying subscribers as
// any promote does. The mirror holds for an event from the target of a
// migration whose rollback rebound the collection back, when the event's
// time says the write followed the rebind. Otherwise — no such migration, a delete event, or a write
// the target has since superseded — it is skipped, as from another bucket.
func (h *PromoteHandler) lateSourceWrite(ctx context.Context, logger *zap.Logger, ev CloudEvent, tenant, objectID uuid.UUID, collection, key string, bound statemachine.Placement) error {
	logger = logger.With(zap.String("object_id", objectID.String()), zap.String("collection", collection),
		zap.String("event_bucket", ev.SubjectFields.Bucket), zap.String("collection_bucket", bound.Bucket))
	if ev.Type != EventTypeUploaded {
		logger.Warn("event is from a bucket the collection is not bound to; skipping")
		return nil
	}
	resourceName := h.objectResourceName(ctx, ev.SubjectFields.TenantID, collection, key)
	w := statemachine.LateSourceWrite{
		FromBucket: ev.SubjectFields.Bucket, To: bound, Sequencer: ev.SubjectFields.Sequencer,
		WrittenAt: reportedWriteTime(ev),
		Move: func(ctx context.Context, from, to statemachine.Placement) (statemachine.MovedBytes, error) {
			return h.Mover.MoveObjectBytes(ctx, objectID, from, to)
		},
	}
	changed, err := h.Late.PromoteLateSourceWriteInTx(ctx, objectID, w, func(ctx context.Context, tx pgx.Tx) error {
		return h.emitUploaded(ctx, tx, ev, resourceName, collection, key, objectID)
	})
	switch {
	case errors.Is(err, statemachine.ErrNotMigratedFrom):
		logger.Warn("event is from a bucket the collection is not bound to; skipping")
		return nil
	case errors.Is(err, statemachine.ErrLateWriteSkipped):
		logger.Info("late source write not moved; skipping", zap.Error(err))
		return nil
	case errors.Is(err, statemachine.ErrLateWriteGone):
		// The source copy is gone before the event came: retrying cannot
		// bring it back.
		logger.Error("late source write: the migration's source no longer holds the object; skipping", zap.Error(err))
		return nil
	case err != nil:
		// A rebind back since the binding was read included: the retry
		// decides against the binding it finds then.
		logger.Warn("late source write failed; the event is retried", zap.Error(err))
		return fmt.Errorf("late source write: %w", err)
	}
	logger.Info("late source write moved into the collection's bucket", zap.Bool("changed", changed))
	if changed {
		h.record(ctx, logger, ev, actionObjectUploaded, resourceName, tenant, objectID)
	}
	return nil
}

// reportedWriteTime is when the store says the event's write was made; zero
// when the adapter fell back to its own clock, which says only when the event
// arrived (CloudEvent.TimeReported).
func reportedWriteTime(ev CloudEvent) time.Time {
	if !ev.TimeReported {
		return time.Time{}
	}
	return ev.Time
}

// objectResourceName builds the canonical (A-shape) object resource name
// `storageBackends/{b}/buckets/{bk}/tenants/{tid}/collections/{ok}/objects-by-key/{key}`
// for the emitted event (ADR-0014 Phase 1), matching what the data-plane
// object handler emits so a subscriber can't tell which producer promoted the
// object. Resolving the collection's (backend, bucket) binding needs a lookup;
// on any miss it falls back to the C-shape name so a resolve blip never blocks
// the event. tenantID is the string from the subject; on a parse failure the
// C-shape (which uses the same string) is returned.
func (h *PromoteHandler) objectResourceName(ctx context.Context, tenantID, collection, key string) string {
	cShape := fmt.Sprintf("tenants/%s/collections/%s/objects-by-key/%s", tenantID, collection, key)
	tid, err := uuid.Parse(tenantID)
	if err != nil {
		return cShape
	}
	row, err := h.Lookup.GetCollection(ctx, pgtype.UUID{Bytes: tid, Valid: true}, collection)
	if err != nil || row.BackendName == "" || row.BucketName == "" {
		return cShape
	}
	return fmt.Sprintf("storageBackends/%s/buckets/%s/tenants/%s/collections/%s/objects-by-key/%s",
		row.BackendName, row.BucketName, tenantID, collection, key)
}

func (h *PromoteHandler) log() *zap.Logger {
	if h.Logger == nil {
		return zap.NewNop()
	}
	return h.Logger
}

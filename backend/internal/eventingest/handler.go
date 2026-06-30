package eventingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// ObjectLookup is the slice of *sqlc.Queries the handler uses to
// resolve (tenant, object_key, key) → object_id. Decoupled into an
// interface so the handler is testable without a live DB.
type ObjectLookup interface {
	LookupObjectByKey(
		ctx context.Context,
		tenantID pgtype.UUID,
		objectKey, key string,
	) (sqlc.LookupObjectByKeyRow, error)
	// ResolveObjectKeyPrefix returns the longest registered object_key that
	// prefixes `tail` (the recombined "<object_key>/<key>" path) for the
	// tenant — disambiguates multi-segment object keys. pgx.ErrNoRows when no
	// object_key is a prefix.
	ResolveObjectKeyPrefix(
		ctx context.Context,
		tenantID pgtype.UUID,
		tail string,
	) (string, error)
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
// EventProducer is the outbox fan-out seam. nil-safe: when the handler is
// wired without one (e.g. a deployment that doesn't deliver webhooks), the
// promote still happens — it just enqueues no `paladin.object.uploaded` rows.
// Only the tx variant is needed: the event is written on the promote tx.
type EventProducer interface {
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

type PromoteHandler struct {
	Lookup       ObjectLookup
	Transitioner *statemachine.Transitioner
	Events       EventProducer
	Logger       *zap.Logger
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
		ev.SubjectFields.ObjectKey == "" ||
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

	tenantPg := pgtype.UUID{Bytes: tenantUUID, Valid: true}

	// Disambiguate multi-segment object keys. The source adapters split the
	// "<object_key>/<key>" tail at the first path segment, which is wrong when
	// the object_key itself is multi-segment (migration 030). Recombine the
	// tail and re-derive the real OK by longest-prefix-match so a nested OK
	// (`invoices/2026/q1`) wins over a shorter sibling (`invoices`).
	objectKey, key := ev.SubjectFields.ObjectKey, ev.SubjectFields.Key
	fullTail := objectKey + "/" + key
	realOK, perr := h.Lookup.ResolveObjectKeyPrefix(ctx, tenantPg, fullTail)
	switch {
	case perr == nil && realOK != "" && realOK != fullTail:
		objectKey = realOK
		key = strings.TrimPrefix(fullTail, realOK+"/")
	case perr == nil, errors.Is(perr, pgx.ErrNoRows):
		// No registered OK prefixes the tail (or it equals the whole tail with
		// an empty key) → keep the source's split; the lookup below skips it
		// as an unknown object.
	default:
		// Genuine DB error → return so the worker retries the event.
		return fmt.Errorf("resolve object key prefix: %w", perr)
	}

	row, err := h.Lookup.LookupObjectByKey(ctx, tenantPg, objectKey, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Race window: event arrived before the data-plane PUT
			// committed the objects row. Skip; reconciler picks up
			// any orphans and a re-delivery would still find no row.
			logger.Debug("no matching object for event; skipping",
				zap.String("tenant_id", ev.SubjectFields.TenantID),
				zap.String("object_key", ev.SubjectFields.ObjectKey),
				zap.String("key", ev.SubjectFields.Key),
			)
			return nil
		}
		return fmt.Errorf("lookup object: %w", err)
	}

	objectID := uuid.UUID(row.Object.ObjectID.Bytes)

	switch ev.Type {
	case EventTypeUploaded:
		// Promote + paladin.object.uploaded fan-out in one tx (ADR-0003). In
		// explicit-mode buckets the storage event is what drives the
		// promote, so without this the webhook subscribers would never see
		// the upload — PALADIN is their unified notification channel. The
		// `changed` guard keeps it exactly-once across producers: an
		// implicit-mode CompleteObject already fired the event, so a later
		// storage event for the same object promotes to a no-op and emits
		// nothing. A dispatch error rolls the promote back; the worker's
		// at-least-once redelivery re-runs both.
		changed, err := h.Transitioner.PromoteToAvailableInTx(
			ctx,
			objectID,
			ev.SubjectFields.Etag,
			ev.SubjectFields.SizeBytes,
			"", // checksum: storage events typically don't carry it
			ev.SubjectFields.Sequencer,
			statemachine.SourceEvent,
			func(ctx context.Context, tx pgx.Tx) error {
				return h.emitUploaded(ctx, tx, ev, objectKey, key, objectID)
			},
		)
		if err != nil {
			return fmt.Errorf("promote object: %w", err)
		}
		logger.Info("promote outcome",
			zap.String("object_id", objectID.String()),
			zap.Bool("changed", changed),
		)
		return nil

	case EventTypeDeleted:
		// Soft-delete via state machine. Idempotent — re-delivery
		// hits state='DELETED' guard and is a no-op.
		// SoftDelete signature lives next to PromoteToAvailable; the
		// concrete name varies — we don't import it directly here so
		// this branch logs intent for now and lets a follow-up wire
		// the actual call. Keeps the surface compiling without
		// stretching this commit's scope into delete-cascade design.
		logger.Info("delete event noted; soft-delete wiring deferred",
			zap.String("object_id", objectID.String()),
		)
		return nil

	default:
		logger.Debug("unknown event type; nothing to do")
		return nil
	}
}

// emitUploaded enqueues the paladin.object.uploaded outbox rows on the promote
// tx. nil-safe: no producer wired → no-op. Mirrors the data-plane
// CompleteObject payload so subscribers can't tell which producer promoted
// the object; the `source: storage_event` discriminator is the only tell.
func (h *PromoteHandler) emitUploaded(ctx context.Context, tx pgx.Tx, ev CloudEvent, objectKey, key string, objectID uuid.UUID) error {
	if h.Events == nil {
		return nil
	}
	// objectKey/key are the RESOLVED values (post longest-prefix-match), not
	// the source's naive split — so the emitted event references the real OK.
	_, err := h.Events.DispatchTx(ctx, tx, ev.SubjectFields.TenantID, worker.Event{
		Type:         string(EventTypeUploaded),
		At:           time.Now().UTC(),
		TenantID:     ev.SubjectFields.TenantID,
		ResourceName: fmt.Sprintf("tenants/%s/objectKeys/%s/objects-by-key/%s", ev.SubjectFields.TenantID, objectKey, key),
		Payload: map[string]any{
			"tenant_id":    ev.SubjectFields.TenantID,
			"object_key":   objectKey,
			"key":          key,
			"object_id":    objectID.String(),
			"size_bytes":   ev.SubjectFields.SizeBytes,
			"etag":         ev.SubjectFields.Etag,
			"source":       "storage_event",
			"event_source": ev.Source,
		},
	})
	return err
}

func (h *PromoteHandler) log() *zap.Logger {
	if h.Logger == nil {
		return zap.NewNop()
	}
	return h.Logger
}

package adapters

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type EventSubscriptionRepoV2 struct {
	q *sqlc.Queries
}

func NewEventSubscriptionRepoV2(q *sqlc.Queries) *EventSubscriptionRepoV2 {
	return &EventSubscriptionRepoV2{q: q}
}

var _ admindomain.EventSubscriptionRepository = (*EventSubscriptionRepoV2)(nil)

func (r *EventSubscriptionRepoV2) Create(ctx context.Context, s admindomain.EventSubscription) error {
	if s.SubscriptionID == uuid.Nil {
		s.SubscriptionID = uuid.Must(uuid.NewV7())
	}
	return r.q.CreateEventSubscription(ctx,
		pgUUID(s.SubscriptionID),
		pgUUID(s.TenantID),
		s.CELFilter,
		s.SinkKind,
		s.SinkConfig,
		s.Disabled,
	)
}

func (r *EventSubscriptionRepoV2) Get(ctx context.Context, id uuid.UUID) (admindomain.EventSubscription, error) {
	row, err := r.q.GetEventSubscription(ctx, pgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.EventSubscription{}, admindomain.ErrNotFound
		}
		return admindomain.EventSubscription{}, err
	}
	return eventSubFromSQLC(row), nil
}

func (r *EventSubscriptionRepoV2) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}
	var tenant pgtype.UUID
	if args.TenantID != uuid.Nil {
		tenant = pgUUIDOptional(args.TenantID)
	}
	// AfterID must always be a VALID pgtype.UUID (not NULL) — the
	// SQL is `subscription_id > $2::uuid`, and Postgres three-valued
	// logic evaluates `x > NULL` as NULL → row excluded. pgUUID(uuid.Nil)
	// returns Valid:false (NULL), which silently matches zero rows on
	// the first page when no AfterID is supplied. Force the value to
	// be valid even for the zero UUID: `subscription_id > '00000000…'`
	// is true for every real UUID, giving the intended "start from
	// the beginning" semantics. Bug surfaced in dispatcher integration
	// tests — Dispatch.Store.List was returning zero subs for any
	// AfterID-Nil call, which would have silently broken outbox
	// fan-out the moment the first real producer wired up.
	afterID := pgtype.UUID{Bytes: args.AfterID, Valid: true}
	rows, err := r.q.ListEventSubscriptions(ctx, tenant, afterID, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.EventSubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventSubFromSQLC(row))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].SubscriptionID.String()
	}
	return out, next, nil
}

func (r *EventSubscriptionRepoV2) Update(ctx context.Context, s admindomain.EventSubscription, expectedVersion int64, mask []string) error {
	has := func(f string) bool { return slices.Contains(mask, f) }
	var celFilter, sinkKind *string
	var sinkConfig []byte
	var disabled *bool
	if has("cel_filter") {
		v := s.CELFilter
		celFilter = &v
	}
	if has("sink_kind") {
		v := s.SinkKind
		sinkKind = &v
	}
	if has("sink_config") {
		sinkConfig = s.SinkConfig
	}
	if has("disabled") {
		v := s.Disabled
		disabled = &v
	}
	rows, err := r.q.UpdateEventSubscription(ctx,
		celFilter, sinkKind, sinkConfig, disabled,
		pgUUID(s.SubscriptionID), expectedVersion,
	)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *EventSubscriptionRepoV2) Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	rows, err := r.q.DeleteEventSubscription(ctx, pgUUID(id), expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func eventSubFromSQLC(row sqlc.EventSubscription) admindomain.EventSubscription {
	return admindomain.EventSubscription{
		SubscriptionID:  uuidFrom(row.SubscriptionID),
		TenantID:        uuidFrom(row.TenantID),
		CELFilter:       row.CelFilter,
		SinkKind:        row.SinkKind,
		SinkConfig:      row.SinkConfig,
		Disabled:        row.Disabled,
		ResourceVersion: row.ResourceVersion,
		CreatedAt:       timeFrom(row.CreatedAt),
		UpdatedAt:       timeFrom(row.UpdatedAt),
	}
}

package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type EventSubscriptionRepoV2 struct {
	q *sqlc.Queries
}

func NewEventSubscriptionRepoV2(q *sqlc.Queries) *EventSubscriptionRepoV2 {
	return &EventSubscriptionRepoV2{q: q}
}

var _ admindomain.EventSubscriptionRepository = (*EventSubscriptionRepoV2)(nil)

func (r *EventSubscriptionRepoV2) Create(ctx context.Context, s *admindomain.EventSubscription) error {
	if s.SubscriptionID == uuid.Nil {
		s.SubscriptionID = uuid.Must(uuid.NewV7())
	}
	return r.q.CreateEventSubscription(ctx,
		pgUUID(s.SubscriptionID),
		pgUUID(s.TenantID),
		s.CELFilter,
		sqlc.EventSinkKind(s.SinkKind),
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
	pageSize := pageSizeOrDefault(args.PageSize)
	var tenant pgtype.UUID
	if args.TenantID != uuid.Nil {
		tenant = pgUUIDOptional(args.TenantID)
	}
	// AfterID maps to a nullable pgtype.UUID: uuid.Nil → NULL → SQL
	// IS-NULL branch fires → "start from the beginning". The query
	// guards `subscription_id > $2 OR $2 IS NULL` so a NULL cursor is
	// the canonical first-page sentinel. (Earlier bandaid forced
	// Valid:true even for uuid.Nil; that worked for the first page
	// but broke once we centralised on the IS-NULL OR pattern across
	// all cursor queries in the package.)
	afterID := pgUUIDOptional(args.AfterID)
	rows, err := r.q.ListEventSubscriptions(ctx, tenant, afterID, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.EventSubscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventSubFromSQLC(row))
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
		next = out[len(out)-1].SubscriptionID.String()
	}
	return out, next, nil
}

func (r *EventSubscriptionRepoV2) Update(ctx context.Context, s admindomain.EventSubscription, expectedVersion int64, mask []string) error {
	// An empty mask replaces every field (AIP-134), as the handler's filter
	// validation already assumes.
	has := func(f string) bool { return len(mask) == 0 || slices.Contains(mask, f) }
	var celFilter, sinkKind *string
	var sinkConfig []byte
	var disabled *bool
	if has(admindomain.EventSubscriptionPathFilter) {
		v := s.CELFilter
		celFilter = &v
	}
	if has(admindomain.EventSubscriptionPathSink) {
		v := s.SinkKind
		sinkKind = &v
		sinkConfig = s.SinkConfig
	}
	if has(admindomain.EventSubscriptionPathDisabled) {
		v := s.Disabled
		disabled = &v
	}
	rows, err := r.q.UpdateEventSubscription(ctx,
		celFilter, sinkKindToSQL(sinkKind), sinkConfig, disabled,
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
		SubscriptionID:  uuidFrom(row.ID),
		TenantID:        uuidFrom(row.TenantID),
		CELFilter:       row.CelFilter,
		SinkKind:        string(row.SinkKind),
		SinkConfig:      row.SinkConfig,
		Disabled:        row.Disabled,
		ResourceVersion: row.ResourceVersion,
		CreatedAt:       timeFrom(row.CreatedAt),
		UpdatedAt:       timeFrom(row.UpdatedAt),
	}
}

// sink_kind is a Postgres enum now; a nil pointer means "leave unchanged".
func sinkKindToSQL(k *string) *sqlc.EventSinkKind {
	if k == nil || *k == "" {
		return nil
	}
	v := sqlc.EventSinkKind(*k)
	return &v
}

// RequeueFailedDeliveries queues the subscription's failed deliveries again.
func (r *EventSubscriptionRepoV2) RequeueFailedDeliveries(ctx context.Context, id uuid.UUID) (int64, error) {
	n, err := r.q.RequeueFailedEventDeliveries(ctx, pgUUID(id))
	if err != nil {
		return 0, fmt.Errorf("requeue failed deliveries: %w", err)
	}
	return n, nil
}

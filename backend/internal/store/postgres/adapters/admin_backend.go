package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type BackendRepoV2 struct {
	q *sqlc.Queries
}

func NewBackendRepoV2(q *sqlc.Queries) *BackendRepoV2 { return &BackendRepoV2{q: q} }

var _ admindomain.BackendRepository = (*BackendRepoV2)(nil)

func (r *BackendRepoV2) Upsert(ctx context.Context, b admindomain.StorageBackend) error {
	return r.q.UpsertStorageBackendV2(ctx,
		b.BackendID,
		b.Kind,
		strPtr(b.Endpoint),
		strPtr(b.Region),
		b.Events.Enabled,
		strPtr(b.Events.Target),
		strPtr(b.DisplayName),
		strPtr(b.PublicEndpoint),
		b.ForcePathStyle,
		strPtr(b.CredentialsSecretRef),
		b.SSE.Type,
		b.SSE.KeyID,
		b.Events.QueueURL,
		b.Events.PollInterval.Milliseconds(),
		b.CedarPolicy,
	)
}

func (r *BackendRepoV2) Get(ctx context.Context, backendID string) (admindomain.StorageBackend, error) {
	row, err := r.q.GetStorageBackendV2(ctx, backendID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.StorageBackend{}, admindomain.ErrNotFound
		}
		return admindomain.StorageBackend{}, fmt.Errorf("get backend: %w", err)
	}
	return admindomain.StorageBackend{
		BackendID:            row.ID,
		DisplayName:          derefStr(row.DisplayName),
		Kind:                 row.Kind,
		Endpoint:             derefStr(row.Endpoint),
		PublicEndpoint:       derefStr(row.PublicEndpoint),
		Region:               derefStr(row.Region),
		ForcePathStyle:       row.ForcePathStyle,
		CredentialsSecretRef: derefStr(row.CredentialsSecretRef),
		SSE: admindomain.ServerSideEncryption{
			Type:  row.SseType,
			KeyID: row.SseKeyID,
		},
		Events: admindomain.EventSourceConfig{
			Enabled:      row.EventsEnabled,
			Target:       derefStr(row.EventsTarget),
			QueueURL:     row.EventsQueueUrl,
			PollInterval: time.Duration(row.EventsPollIntervalMs) * time.Millisecond,
		},
		CedarPolicy:     row.CedarPolicy,
		ResourceVersion: row.ResourceVersion,
		CreatedAt:       timeFrom(row.CreatedAt),
		UpdatedAt:       timeFrom(row.UpdatedAt),
	}, nil
}

func (r *BackendRepoV2) List(ctx context.Context, pageSize int32, afterID string) ([]admindomain.StorageBackend, string, error) {
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}
	rows, err := r.q.ListStorageBackends(ctx, afterID, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.StorageBackend, 0, len(rows))
	for _, row := range rows {
		out = append(out, admindomain.StorageBackend{
			BackendID:            row.ID,
			DisplayName:          derefStr(row.DisplayName),
			Kind:                 row.Kind,
			Endpoint:             derefStr(row.Endpoint),
			PublicEndpoint:       derefStr(row.PublicEndpoint),
			Region:               derefStr(row.Region),
			ForcePathStyle:       row.ForcePathStyle,
			CredentialsSecretRef: derefStr(row.CredentialsSecretRef),
			SSE:                  admindomain.ServerSideEncryption{Type: row.SseType, KeyID: row.SseKeyID},
			Events: admindomain.EventSourceConfig{
				Enabled:      row.EventsEnabled,
				Target:       derefStr(row.EventsTarget),
				QueueURL:     row.EventsQueueUrl,
				PollInterval: time.Duration(row.EventsPollIntervalMs) * time.Millisecond,
			},
			CedarPolicy:     row.CedarPolicy,
			ResourceVersion: row.ResourceVersion,
			CreatedAt:       timeFrom(row.CreatedAt),
			UpdatedAt:       timeFrom(row.UpdatedAt),
		})
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].BackendID
	}
	return out, next, nil
}

func (r *BackendRepoV2) Update(ctx context.Context, b admindomain.StorageBackend, expectedVersion int64, mask []string) error {
	has := func(field string) bool { return slices.Contains(mask, field) }
	var displayName, endpoint, publicEndpoint, region, credSecret, sseType, sseKeyID, eventsTarget, eventsQueueURL, cedarPolicy *string
	var forcePathStyle, eventsEnabled *bool
	var eventsPollIntervalMs *int64

	if has("display_name") {
		v := b.DisplayName
		displayName = &v
	}
	if has("endpoint") {
		v := b.Endpoint
		endpoint = &v
	}
	if has("public_endpoint") {
		v := b.PublicEndpoint
		publicEndpoint = &v
	}
	if has("region") {
		v := b.Region
		region = &v
	}
	if has("force_path_style") {
		v := b.ForcePathStyle
		forcePathStyle = &v
	}
	if has("credentials_secret_ref") {
		v := b.CredentialsSecretRef
		credSecret = &v
	}
	if has("sse") {
		t := b.SSE.Type
		k := b.SSE.KeyID
		sseType = &t
		sseKeyID = &k
	}
	if has("events") {
		e := b.Events.Enabled
		t := b.Events.Target
		q := b.Events.QueueURL
		ms := b.Events.PollInterval.Milliseconds()
		eventsEnabled = &e
		eventsTarget = &t
		eventsQueueURL = &q
		eventsPollIntervalMs = &ms
	}
	if has("cedar_policy") {
		v := b.CedarPolicy
		cedarPolicy = &v
	}

	rows, err := r.q.UpdateStorageBackend(ctx,
		displayName, endpoint, publicEndpoint, region, forcePathStyle,
		credSecret, sseType, sseKeyID, eventsEnabled, eventsTarget,
		eventsQueueURL, eventsPollIntervalMs, cedarPolicy,
		b.BackendID, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("update backend: %w", err)
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

func (r *BackendRepoV2) RotateCredentials(ctx context.Context, backendID, secretRef string) error {
	rows, err := r.q.RotateStorageBackendCredentials(ctx, backendID, &secretRef)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

func (r *BackendRepoV2) Delete(ctx context.Context, backendID string, expectedVersion int64, force bool) error {
	if !force {
		count, err := r.q.CountBucketsForBackend(ctx, backendID)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("%w: %d buckets still reference backend %q", admindomain.ErrConflict, count, backendID)
		}
	}
	rows, err := r.q.DeleteStorageBackend(ctx, backendID, expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return admindomain.ErrVersionMismatch
	}
	return nil
}

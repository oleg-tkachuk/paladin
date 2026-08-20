package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type BackendRepoV2 struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool // ADR-0003 tx seam (RunInTx + *Tx mutations)
}

func NewBackendRepoV2(q *sqlc.Queries, pool *pgxpool.Pool) *BackendRepoV2 {
	return &BackendRepoV2{q: q, pool: pool}
}

var _ admindomain.BackendRepository = (*BackendRepoV2)(nil)

// RunInTx runs fn inside one transaction on the repo's pool — the ADR-0003
// seam so a mutation and its outbox fan-out commit (or roll back) atomically.
func (r *BackendRepoV2) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RotateCredentialsTx runs the grace-window dual-write on the caller's tx (see
// RotateCredentials). graceSeconds=0 clears the previous-credential window.
func (r *BackendRepoV2) RotateCredentialsTx(ctx context.Context, tx pgx.Tx, backendID, secretRef string, graceSeconds int64) error {
	ref := secretRef
	rows, err := r.q.WithTx(tx).RotateStorageBackendCredentials(ctx, backendID, ref, graceSeconds)
	if err != nil {
		return fmt.Errorf("rotate credentials: %w", err)
	}
	if rows == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

// GetTx reads a backend on the caller's tx — used to read the post-rotation
// row back for the event payload inside the same transaction.
func (r *BackendRepoV2) GetTx(ctx context.Context, tx pgx.Tx, backendID string) (admindomain.StorageBackend, error) {
	row, err := r.q.WithTx(tx).GetStorageBackendV2(ctx, backendID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.StorageBackend{}, admindomain.ErrNotFound
		}
		return admindomain.StorageBackend{}, fmt.Errorf("get backend: %w", err)
	}
	return backendFromGetRow(row), nil
}

func (r *BackendRepoV2) Upsert(ctx context.Context, b admindomain.StorageBackend) error {
	return r.q.UpsertStorageBackendV2(ctx,
		b.BackendID,
		b.Kind,
		b.Endpoint,
		b.Region,
		b.Events.Enabled,
		b.Events.Target,
		b.DisplayName,
		b.PublicEndpoint,
		b.ForcePathStyle,
		b.CredentialsSecretRef,
		b.SSE.Type,
		b.SSE.KeyID,
		b.Events.QueueURL,
		b.Events.PollInterval.Milliseconds(),
		b.CedarPolicy,
		b.Provider,
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
	return backendFromGetRow(row), nil
}

// backendFromGetRow maps a GetStorageBackendV2 row to the domain type. Shared
// by Get and GetTx so the two never drift.
func backendFromGetRow(row sqlc.GetStorageBackendV2Row) admindomain.StorageBackend {
	return admindomain.StorageBackend{
		BackendID:            row.Name,
		DisplayName:          row.DisplayName,
		Kind:                 row.Kind,
		Provider:             row.Provider,
		Endpoint:             row.Endpoint,
		PublicEndpoint:       row.PublicEndpoint,
		Region:               row.Region,
		ForcePathStyle:       row.ForcePathStyle,
		CredentialsSecretRef: row.CredentialsSecretRef,
		SSE: admindomain.ServerSideEncryption{
			Type:  row.SseType,
			KeyID: row.SseKeyID,
		},
		Events: admindomain.EventSourceConfig{
			Enabled:      row.EventsEnabled,
			Target:       row.EventsTarget,
			QueueURL:     row.EventsQueueUrl,
			PollInterval: time.Duration(row.EventsPollIntervalMs) * time.Millisecond,
		},
		CedarPolicy:                   row.CedarPolicy,
		Enabled:                       row.Enabled,
		ReadOnly:                      row.ReadOnly,
		Maintenance:                   row.Maintenance,
		HealthStatus:                  string(row.HealthStatus),
		HealthMessage:                 row.HealthMessage,
		HealthCheckedAt:               timeFrom(row.HealthCheckedAt),
		PreviousCredentialsSecretRef:  row.PreviousCredentialsSecretRef,
		PreviousCredentialsValidUntil: timeFrom(row.PreviousCredentialsValidUntil),
		ResourceVersion:               row.ResourceVersion,
		CreatedAt:                     timeFrom(row.CreatedAt),
		UpdatedAt:                     timeFrom(row.UpdatedAt),
	}
}

func (r *BackendRepoV2) List(ctx context.Context, pageSize int32, afterID string) ([]admindomain.StorageBackend, string, error) {
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}
	// Empty string ⇒ NULL cursor ⇒ "from the beginning". The SQL
	// guards `id > $1 OR $1 IS NULL` (canonical IS-NULL-OR pattern;
	// matches the rule applied to every cursor query in this
	// package). Passing `&""` would compare against an empty string
	// and silently exclude rows whose id sorts at or before empty,
	// so the sentinel is `nil`, not an empty pointer.
	var afterPtr *string
	if afterID != "" {
		afterPtr = &afterID
	}
	rows, err := r.q.ListStorageBackends(ctx, afterPtr, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]admindomain.StorageBackend, 0, len(rows))
	for _, row := range rows {
		out = append(out, admindomain.StorageBackend{
			BackendID:            row.Name,
			DisplayName:          row.DisplayName,
			Kind:                 row.Kind,
			Provider:             row.Provider,
			Endpoint:             row.Endpoint,
			PublicEndpoint:       row.PublicEndpoint,
			Region:               row.Region,
			ForcePathStyle:       row.ForcePathStyle,
			CredentialsSecretRef: row.CredentialsSecretRef,
			SSE:                  admindomain.ServerSideEncryption{Type: row.SseType, KeyID: row.SseKeyID},
			Events: admindomain.EventSourceConfig{
				Enabled:      row.EventsEnabled,
				Target:       row.EventsTarget,
				QueueURL:     row.EventsQueueUrl,
				PollInterval: time.Duration(row.EventsPollIntervalMs) * time.Millisecond,
			},
			CedarPolicy:                   row.CedarPolicy,
			Enabled:                       row.Enabled,
			ReadOnly:                      row.ReadOnly,
			PreviousCredentialsSecretRef:  row.PreviousCredentialsSecretRef,
			PreviousCredentialsValidUntil: timeFrom(row.PreviousCredentialsValidUntil),
			ResourceVersion:               row.ResourceVersion,
			CreatedAt:                     timeFrom(row.CreatedAt),
			UpdatedAt:                     timeFrom(row.UpdatedAt),
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

// SetEnabled flips the backend's enable/disable state under OCC. Returns
// ErrNotFound when the id is absent and ErrVersionMismatch when the
// resource_version no longer matches (0 rows affected).
func (r *BackendRepoV2) SetEnabled(ctx context.Context, backendID string, enabled bool, expectedVersion int64) error {
	rows, err := r.q.SetStorageBackendEnabled(ctx, enabled, backendID, expectedVersion)
	if err != nil {
		return fmt.Errorf("set backend enabled: %w", err)
	}
	if rows == 0 {
		// Distinguish "no such backend" from "version mismatch": a
		// missing row is ErrNotFound; an existing row whose version
		// moved on is ErrVersionMismatch.
		if _, getErr := r.q.GetStorageBackendV2(ctx, backendID); errors.Is(getErr, pgx.ErrNoRows) {
			return admindomain.ErrNotFound
		}
		return admindomain.ErrVersionMismatch
	}
	return nil
}

// SetReadOnly flips the backend's drain (read-only) state under OCC
// (migration 047). Same NotFound / VersionMismatch disambiguation as
// SetEnabled.
func (r *BackendRepoV2) SetReadOnly(ctx context.Context, backendID string, readOnly bool, expectedVersion int64) error {
	rows, err := r.q.SetStorageBackendReadOnly(ctx, readOnly, backendID, expectedVersion)
	if err != nil {
		return fmt.Errorf("set backend read_only: %w", err)
	}
	if rows == 0 {
		if _, getErr := r.q.GetStorageBackendV2(ctx, backendID); errors.Is(getErr, pgx.ErrNoRows) {
			return admindomain.ErrNotFound
		}
		return admindomain.ErrVersionMismatch
	}
	return nil
}

// SetMaintenance flips the operator-set maintenance flag under OCC
// (migration 049). Same NotFound / VersionMismatch disambiguation as
// SetEnabled / SetReadOnly.
func (r *BackendRepoV2) SetMaintenance(ctx context.Context, backendID string, maintenance bool, expectedVersion int64) error {
	rows, err := r.q.SetStorageBackendMaintenance(ctx, maintenance, backendID, expectedVersion)
	if err != nil {
		return fmt.Errorf("set backend maintenance: %w", err)
	}
	if rows == 0 {
		if _, getErr := r.q.GetStorageBackendV2(ctx, backendID); errors.Is(getErr, pgx.ErrNoRows) {
			return admindomain.ErrNotFound
		}
		return admindomain.ErrVersionMismatch
	}
	return nil
}

// SetHealth upserts the derived health state (migration 048). No OCC and no
// resource_version churn — it writes the separate storage_backend_health
// table. The FK is ON DELETE CASCADE, so a probe racing a backend delete
// simply no-ops (or the row is cleaned up); a missing backend surfaces as an
// FK violation, which the caller (TestBackend, best-effort) swallows.
func (r *BackendRepoV2) SetHealth(ctx context.Context, backendName, status, message string, checkedAt time.Time) error {
	if err := r.q.UpsertStorageBackendHealth(ctx,
		sqlc.BackendHealthStatus(status), &message, pgTS(checkedAt), backendName); err != nil {
		return fmt.Errorf("set backend health: %w", err)
	}
	return nil
}

func (r *BackendRepoV2) RotateCredentials(ctx context.Context, backendID, secretRef string, graceSeconds int64) error {
	rows, err := r.q.RotateStorageBackendCredentials(ctx, backendID, secretRef, graceSeconds)
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

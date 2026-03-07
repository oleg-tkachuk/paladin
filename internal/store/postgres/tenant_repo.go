package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

const (
	upsertTenantQuery = `
INSERT INTO tenants (id, tenant_id, display_name, labels, tags)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id) DO UPDATE
    SET display_name = EXCLUDED.display_name,
        labels       = EXCLUDED.labels,
        tags         = EXCLUDED.tags,
        updated_at   = now()
RETURNING id, tenant_id, display_name, labels, tags, created_at, updated_at`

	getTenantQuery = `
SELECT id, tenant_id, display_name, labels, tags, created_at, updated_at
FROM tenants
WHERE tenant_id = $1`

	deleteTenantQuery = `
DELETE FROM tenants
WHERE tenant_id = $1`

	tenantHasActiveObjectsQuery = `
SELECT EXISTS(
    SELECT 1
    FROM objects
    WHERE tenant_id = $1
      AND status NOT IN ('hard_deleted')
) AS has_active_objects`

	updateTenantMetadataQuery = `
UPDATE tenants
SET labels     = jsonb_strip_nulls(labels || $2::jsonb),
    tags       = $3::text[],
    updated_at = now()
WHERE tenant_id = $1
RETURNING id, tenant_id, display_name, labels, tags, created_at, updated_at`
)

// TenantRepo implements domain.TenantRepository backed by PostgreSQL.
type TenantRepo struct {
	db *DB
}

// NewTenantRepo creates a new TenantRepo.
func NewTenantRepo(db *DB) *TenantRepo {
	return &TenantRepo{db: db}
}

// scanTenant scans a full tenant row (id, tenant_id, display_name, labels, tags, created_at, updated_at).
func scanTenant(row pgx.Row) (*domain.Tenant, error) {
	var t domain.Tenant
	var rawID pgtype.UUID
	var displayName *string
	var labelsJSON []byte
	var tags []string

	if err := row.Scan(&rawID, &t.TenantID, &displayName, &labelsJSON, &tags, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}

	id, err := uuidFromPgtype(rawID)
	if err != nil {
		return nil, fmt.Errorf("parse tenant id: %w", err)
	}

	t.ID = id
	t.DisplayName = displayName

	if len(labelsJSON) > 0 {
		if err := json.Unmarshal(labelsJSON, &t.Labels); err != nil {
			return nil, fmt.Errorf("parse tenant labels: %w", err)
		}
	}

	if t.Labels == nil {
		t.Labels = make(map[string]string)
	}

	t.Tags = tags
	if t.Tags == nil {
		t.Tags = []string{}
	}

	return &t, nil
}

// labelsToJSON converts a map[string]string to JSON bytes for PostgreSQL JSONB.
func labelsToJSON(labels map[string]string) ([]byte, error) {
	if labels == nil {
		return []byte("{}"), nil
	}

	b, err := json.Marshal(labels)
	if err != nil {
		return nil, fmt.Errorf("marshal labels: %w", err)
	}

	return b, nil
}

// labelsPatchToJSON converts a map[string]interface{} (where values can be nil to delete keys)
// to JSON bytes for the PostgreSQL JSONB merge expression.
func labelsPatchToJSON(patch map[string]interface{}) ([]byte, error) {
	if patch == nil {
		return []byte("{}"), nil
	}

	b, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshal labels patch: %w", err)
	}

	return b, nil
}

// toTextArray converts a []string to the pgx text array format.
// A nil slice is stored as an empty array.
func toTextArray(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// Create upserts a tenant. If the tenant already exists its fields are
// updated and the resulting record is returned (idempotent).
func (r *TenantRepo) Create(ctx context.Context, rec domain.Tenant) (*domain.Tenant, error) {
	labelsJSON, err := labelsToJSON(rec.Labels)
	if err != nil {
		return nil, fmt.Errorf("upsert tenant: %w", err)
	}

	row := r.db.Pool.QueryRow(ctx, upsertTenantQuery,
		uuidToPgtype(rec.ID),
		rec.TenantID,
		rec.DisplayName,
		labelsJSON,
		toTextArray(rec.Tags),
	)

	t, err := scanTenant(row)
	if err != nil {
		return nil, fmt.Errorf("upsert tenant: %w", err)
	}

	return t, nil
}

// Get retrieves a tenant by tenant_id. Returns ErrNotFound if absent.
func (r *TenantRepo) Get(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	row := r.db.Pool.QueryRow(ctx, getTenantQuery, tenantID)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.NotFound(fmt.Sprintf("tenant %q not found", tenantID), nil)
		}

		return nil, fmt.Errorf("get tenant: %w", err)
	}

	return t, nil
}

// Delete removes the tenant. Returns (false, nil) if it did not exist.
func (r *TenantRepo) Delete(ctx context.Context, tenantID string) (bool, error) {
	result, err := r.db.Pool.Exec(ctx, deleteTenantQuery, tenantID)
	if err != nil {
		return false, fmt.Errorf("delete tenant: %w", err)
	}

	return result.RowsAffected() > 0, nil
}

// HasActiveObjects returns true when the tenant owns at least one object
// that has not been hard-deleted.
func (r *TenantRepo) HasActiveObjects(ctx context.Context, tenantID string) (bool, error) {
	row := r.db.Pool.QueryRow(ctx, tenantHasActiveObjectsQuery, tenantID)

	var hasActive bool
	if err := row.Scan(&hasActive); err != nil {
		return false, fmt.Errorf("check active objects: %w", err)
	}

	return hasActive, nil
}

// UpdateMetadata merges the label patch into existing labels (stripping null
// values) and replaces tags wholesale.
func (r *TenantRepo) UpdateMetadata(ctx context.Context, tenantID string, labelsPatch map[string]interface{}, tags []string) (*domain.Tenant, error) {
	patchJSON, err := labelsPatchToJSON(labelsPatch)
	if err != nil {
		return nil, fmt.Errorf("update tenant metadata: %w", err)
	}

	row := r.db.Pool.QueryRow(ctx, updateTenantMetadataQuery,
		tenantID,
		patchJSON,
		toTextArray(tags),
	)

	t, err := scanTenant(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.NotFound(fmt.Sprintf("tenant %q not found", tenantID), nil)
		}

		return nil, fmt.Errorf("update tenant metadata: %w", err)
	}

	return t, nil
}

// List returns a page of tenants matching filter, ordered by created_at DESC.
// cursor is an RFC3339-encoded timestamp; pass "" for the first page.
func (r *TenantRepo) List(ctx context.Context, filter domain.ListTenantsFilter) ([]domain.Tenant, string, int64, error) {
	limit := filter.Limit
	cursor := filter.Cursor
	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "ListTenants", opStatus, start) }()

	var pgCursor pgtype.Timestamptz
	if cursor != "" {
		t, err := time.Parse(time.RFC3339, cursor)
		if err != nil {
			opStatus = "error"
			return nil, "", 0, fmt.Errorf("invalid cursor: %w", err)
		}
		pgCursor = timestampToPgtype(t)
	}

	var labelFilterJSON []byte
	if len(filter.LabelSelector) > 0 {
		var err error
		labelFilterJSON, err = json.Marshal(filter.LabelSelector)
		if err != nil {
			opStatus = "error"
			return nil, "", 0, fmt.Errorf("marshal label filter: %w", err)
		}
	}

	var search string
	if filter.Search != nil {
		search = *filter.Search
	}

	rows, err := r.db.Queries.ListTenantsPaginated(ctx,
		pgCursor,
		labelFilterJSON,
		filter.TagSelector,
		search,
		filter.SortBy,
		filter.SortOrder,
		int32(limit+1),
	)
	if err != nil {
		opStatus = "error"
		return nil, "", 0, fmt.Errorf("list tenants: %w", err)
	}

	var totalCount int64
	if len(rows) > 0 {
		totalCount = rows[0].TotalCount
	}

	out := make([]domain.Tenant, 0, len(rows))
	for _, row := range rows {
		t, err := mapToDomainTenant(row)
		if err != nil {
			opStatus = "error"
			return nil, "", 0, fmt.Errorf("map tenant: %w", err)
		}
		out = append(out, t)
	}

	nextCursor := ""
	if limit > 0 && len(out) > limit {
		nextCursor = out[limit-1].CreatedAt.Format(time.RFC3339)
		out = out[:limit]
	}

	opStatus = "success"
	return out, nextCursor, totalCount, nil
}

// mapToDomainTenant converts a SQLC row to a domain Tenant.
func mapToDomainTenant(row sqlc.ListTenantsPaginatedRow) (domain.Tenant, error) {
	id, err := uuidFromPgtype(row.ID)
	if err != nil {
		return domain.Tenant{}, err
	}

	var labels map[string]string
	if len(row.Labels) > 0 {
		if err := json.Unmarshal(row.Labels, &labels); err != nil {
			return domain.Tenant{}, err
		}
	}

	return domain.Tenant{
		ID:          id,
		TenantID:    row.TenantID,
		DisplayName: row.DisplayName,
		Labels:      labels,
		Tags:        row.Tags,
		CreatedAt:   timestampFromPgtype(row.CreatedAt),
		UpdatedAt:   timestampFromPgtype(row.UpdatedAt),
	}, nil
}

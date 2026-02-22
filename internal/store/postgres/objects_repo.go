package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type ObjectsRepo struct {
	db *DB
}

func NewObjectsRepo(db *DB) *ObjectsRepo { return &ObjectsRepo{db: db} }

func (r *ObjectsRepo) Create(ctx context.Context, rec domain.Object) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "CreateObject", status, start) }()

	labels, err := marshalStringMap(rec.Labels)
	if err != nil {
		status = "error"
		return fmt.Errorf("marshal labels: %w", err)
	}

	err = r.db.Queries.CreateObject(ctx,
		uuidToPgtype(rec.ID),
		rec.TenantID,
		rec.ObjectKey,
		rec.Bucket,
		rec.ContentType,
		rec.SizeBytes,
		rec.ChecksumSHA256,
		string(rec.Status),
		timestampPtrToPgtype(rec.ExpiresAt),
		labels,
		rec.ExternalRef,
		rec.Category,
		rec.Subpath,
	)

	if err != nil {
		status = "error"
	} else {
		status = "success"
	}
	return MapPgError(err)
}

func (r *ObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "ListExpiredPendingObjects", status, start) }()

	rows, err := r.db.Queries.ListExpiredPendingObjects(ctx, timestampToPgtype(cutoff), int32(limit))
	if err != nil {
		status = "error"
		return nil, MapPgError(err)
	}

	out := make([]domain.Object, 0, len(rows))
	for _, row := range rows {
		obj, err := MapObjectToDomain(row)
		if err != nil {
			return nil, fmt.Errorf("map object: %w", err)
		}
		out = append(out, obj)
	}

	status = "success"
	return out, nil
}

func (r *ObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetObject", status, start) }()

	obj, err := r.db.Queries.GetObject(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = "error"
		if errors.Is(err, pgx.ErrNoRows) {
			status = "not_found"
			return nil, domain.ErrNotFound
		}
		return nil, MapPgError(err)
	}

	result, err := MapObjectToDomain(obj)
	if err != nil {
		status = "error"
		return nil, err
	}

	status = "success"
	return &result, nil
}

func (r *ObjectsRepo) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkObjectComplete", status, start) }()

	rows, err := r.db.Queries.MarkObjectComplete(ctx, tenantID, uuidToPgtype(id), &etag, &sizeBytes)
	if err != nil {
		status = "error"
		return false, MapPgError(err)
	}

	status = "success"
	return rows > 0, nil
}

func (r *ObjectsRepo) MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkObjectSoftDeleted", status, start) }()

	rows, err := r.db.Queries.MarkObjectSoftDeleted(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = "error"
		return false, MapPgError(err)
	}

	status = "success"
	return rows > 0, nil
}

func (r *ObjectsRepo) MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkObjectHardDeleted", status, start) }()

	rows, err := r.db.Queries.MarkObjectHardDeleted(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = "error"
		return false, MapPgError(err)
	}

	status = "success"
	return rows > 0, nil
}

func (r *ObjectsRepo) Restore(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "RestoreObject", status, start) }()

	rows, err := r.db.Queries.RestoreObject(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = "error"
		return false, MapPgError(err)
	}

	status = "success"
	return rows > 0, nil
}

func (r *ObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	return r.MarkHardDeleted(ctx, tenantID, id)
}

func (r *ObjectsRepo) UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string) (bool, error) {
	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "UpdateObjectStatus", opStatus, start) }()

	rows, err := r.db.Queries.UpdateObjectStatus(ctx, tenantID, uuidToPgtype(id), status)
	if err != nil {
		opStatus = "error"
		return false, MapPgError(err)
	}
	// UpdateObjectStatus is :execrows
	opStatus = "success"
	return rows > 0, nil
}

func (r *ObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetObjectByExternalRef", status, start) }()

	obj, err := r.db.Queries.GetObjectByExternalRef(ctx, tenantID, &externalRef)
	if err != nil {
		status = "error"
		return nil, MapPgError(err)
	}

	result, err := MapObjectToDomain(obj)
	if err != nil {
		status = "error"
		return nil, err
	}

	status = "success"
	return &result, nil
}

func (r *ObjectsRepo) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter, limit int, cursor string) ([]domain.Object, string, error) {
	var cursorTime pgtype.Timestamptz
	if cursor != "" {
		t, err := time.Parse(time.RFC3339, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		cursorTime = timestampToPgtype(t)
	}

	var statusPtr *string
	if filter.Status != nil {
		s := string(*filter.Status)
		statusPtr = &s
	}

	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "ListObjects", opStatus, start) }()

	rows, err := r.db.Queries.ListObjects(ctx,
		tenantID,
		int32(limit+1), // Fetch one extra to determine if there's a next page
		statusPtr,
		filter.ExternalRef,
		timestampPtrToPgtype(filter.CreatedAfter),
		timestampPtrToPgtype(filter.CreatedBefore),
		cursorTime,
		filter.Category,
		filter.KeyPrefix,
	)
	if err != nil {
		opStatus = "error"
		return nil, "", MapPgError(err)
	}

	out := make([]domain.Object, 0, len(rows))
	for _, row := range rows {
		obj, err := MapObjectToDomain(row)
		if err != nil {
			return nil, "", fmt.Errorf("map object: %w", err)
		}
		out = append(out, obj)
	}

	nextCursor := ""
	if len(out) > limit {
		nextCursor = out[limit-1].CreatedAt.Format(time.RFC3339)
		out = out[:limit]
	}

	opStatus = "success"
	return out, nextCursor, nil
}

func (r *ObjectsRepo) Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*domain.Object, error) {
	var obj sqlc.Object
	var err error

	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "PatchObject", status, start) }()

	// Determine which query to use based on what's being patched
	if labels != nil && externalRef != nil {
		var labelsJSON []byte
		labelsJSON, err = marshalStringMap(labels)
		if err != nil {
			status = "error"
			return nil, fmt.Errorf("marshal labels: %w", err)
		}
		obj, err = r.db.Queries.PatchObjectLabelsAndExternalRef(ctx, tenantID, uuidToPgtype(id), labelsJSON, externalRef)
	} else if labels != nil {
		var labelsJSON []byte
		labelsJSON, err = marshalStringMap(labels)
		if err != nil {
			status = "error"
			return nil, fmt.Errorf("marshal labels: %w", err)
		}
		obj, err = r.db.Queries.PatchObjectLabels(ctx, tenantID, uuidToPgtype(id), labelsJSON)
	} else if externalRef != nil {
		obj, err = r.db.Queries.PatchObjectExternalRef(ctx, tenantID, uuidToPgtype(id), externalRef)
	} else {
		// Nothing to patch, just fetch the current object
		status = "success"
		return r.Get(ctx, tenantID, id)
	}

	if err != nil {
		status = "error"
		return nil, MapPgError(err)
	}

	result, err := MapObjectToDomain(obj)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

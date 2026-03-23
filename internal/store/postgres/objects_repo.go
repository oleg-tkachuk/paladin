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
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"go.uber.org/zap"
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
		status = domain.StatusError

		return fmt.Errorf("marshal labels: %w", err)
	}

	tags, err := marshalStringMap(rec.Tags)
	if err != nil {
		status = domain.StatusError

		return fmt.Errorf("marshal tags: %w", err)
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
		tags,
	)

	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *ObjectsRepo) BulkCreate(ctx context.Context, objects []domain.Object) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "BulkCreateObject", status, start) }()

	batch := &pgx.Batch{}
	for _, rec := range objects {
		labels, err := marshalStringMap(rec.Labels)
		if err != nil {
			status = domain.StatusError

			return fmt.Errorf("marshal labels for %s: %w", rec.ID, err)
		}

		tags, err := marshalStringMap(rec.Tags)
		if err != nil {
			status = domain.StatusError

			return fmt.Errorf("marshal tags for %s: %w", rec.ID, err)
		}

		batch.Queue(`INSERT INTO objects (
			id, tenant_id, object_key, bucket, content_type, size_bytes,
			checksum_sha256, status, expires_at, labels, external_ref, category, subpath, tags
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)`,
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
			tags,
		)
	}

	br := r.db.Pool.SendBatch(ctx, batch)
	defer func() {
		if err := br.Close(); err != nil {
			r.db.log.Warn("close batch results failed", zap.Error(err))
		}
	}()

	for i := 0; i < len(objects); i++ {
		_, err := br.Exec()
		if err != nil {
			status = domain.StatusError

			return mapPgError(err)
		}
	}

	status = domain.StatusSuccess

	return nil
}

func (r *ObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "ListExpiredPendingObjects", status, start) }()

	rows, err := r.db.Queries.ListExpiredPendingObjects(ctx, timestampToPgtype(cutoff), safecast.Int32(limit))
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	out := make([]domain.Object, 0, len(rows))
	for _, row := range rows {
		obj, err := mapToDomainObject(row.Object)
		if err != nil {
			return nil, fmt.Errorf("map object: %w", err)
		}
		out = append(out, obj)
	}

	status = domain.StatusSuccess

	return out, nil
}

func (r *ObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetObject", status, start) }()

	obj, err := r.db.Queries.GetObject(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = domain.StatusError
		if errors.Is(err, pgx.ErrNoRows) {
			status = domain.StatusNotFound

			return nil, domain.ErrNotFound
		}

		return nil, mapPgError(err)
	}

	result, err := mapToDomainObject(obj.Object)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *ObjectsRepo) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkObjectComplete", status, start) }()

	rows, err := r.db.Queries.MarkObjectComplete(ctx, tenantID, uuidToPgtype(id), &etag, &sizeBytes)
	if err != nil {
		status = domain.StatusError

		return false, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows > 0, nil
}

func (r *ObjectsRepo) MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkObjectSoftDeleted", status, start) }()

	rows, err := r.db.Queries.MarkObjectSoftDeleted(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = domain.StatusError

		return false, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows > 0, nil
}

func (r *ObjectsRepo) MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkObjectHardDeleted", status, start) }()

	rows, err := r.db.Queries.MarkObjectHardDeleted(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = domain.StatusError

		return false, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows > 0, nil
}

func (r *ObjectsRepo) Restore(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "RestoreObject", status, start) }()

	rows, err := r.db.Queries.RestoreObject(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = domain.StatusError

		return false, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows > 0, nil
}

func (r *ObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	return r.MarkHardDeleted(ctx, tenantID, id)
}

func (r *ObjectsRepo) Delete(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "DeleteObject", status, start) }()

	rows, err := r.db.Queries.DeleteObject(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = domain.StatusError

		return false, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows > 0, nil
}

func (r *ObjectsRepo) UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string) (bool, error) {
	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "UpdateObjectStatus", opStatus, start) }()

	rows, err := r.db.Queries.UpdateObjectStatus(ctx, tenantID, uuidToPgtype(id), status)
	if err != nil {
		opStatus = domain.StatusError

		return false, mapPgError(err)
	}
	opStatus = domain.StatusSuccess

	return rows > 0, nil
}

func (r *ObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetObjectByExternalRef", status, start) }()

	obj, err := r.db.Queries.GetObjectByExternalRef(ctx, tenantID, &externalRef)
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	result, err := mapToDomainObject(obj.Object)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *ObjectsRepo) GetByKey(ctx context.Context, tenantID, bucket, key string) (*domain.Object, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetObjectByKey", status, start) }()

	obj, err := r.db.Queries.GetObjectByKey(ctx, tenantID, bucket, key)
	if err != nil {
		status = domain.StatusError
		if errors.Is(err, pgx.ErrNoRows) {
			status = domain.StatusNotFound

			return nil, domain.ErrNotFound
		}

		return nil, mapPgError(err)
	}

	result, err := mapToDomainObject(obj.Object)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *ObjectsRepo) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "ListObjects", opStatus, start) }()

	var status *string
	if filter.Status != nil {
		s := string(*filter.Status)
		status = &s
	}

	var cursorTime pgtype.Timestamptz
	if filter.Cursor != "" {
		t, err := time.Parse(time.RFC3339Nano, filter.Cursor)
		if err == nil {
			cursorTime = timestampToPgtype(t)
		}
	}

	rows, err := r.db.Queries.ListObjects(ctx,
		tenantID,
		status,
		filter.ExternalRef,
		timestampPtrToPgtype(filter.CreatedAfter),
		timestampPtrToPgtype(filter.CreatedBefore),
		cursorTime,
		filter.Category,
		filter.Recursive,
		filter.KeyPrefix,
		filter.SortBy,
		filter.SortOrder,
		safecast.Int32(filter.Limit+1),
	)
	if err != nil {
		opStatus = domain.StatusError

		return nil, "", 0, mapPgError(err)
	}

	out := make([]domain.Object, 0, len(rows))
	for i, row := range rows {
		if i == filter.Limit {
			break
		}
		obj, err := mapToDomainObject(row.Object)
		if err != nil {
			return nil, "", 0, fmt.Errorf("map object: %w", err)
		}
		out = append(out, obj)
	}

	var nextCursor string
	if len(rows) > filter.Limit {
		last := rows[filter.Limit-1]
		nextCursor = last.Object.CreatedAt.Time.Format(time.RFC3339Nano)
	}

	var total int64
	if len(rows) > 0 {
		total = rows[0].TotalCount
	}

	opStatus = domain.StatusSuccess

	return out, nextCursor, total, nil
}

func (r *ObjectsRepo) BulkMarkSoftDeleted(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "BulkMarkObjectSoftDeleted", status, start) }()

	pgIds := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pgIds[i] = uuidToPgtype(id)
	}

	rows, err := r.db.Queries.BulkMarkObjectSoftDeleted(ctx, tenantID, pgIds)
	if err != nil {
		status = domain.StatusError

		return 0, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows, nil
}

func (r *ObjectsRepo) BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "BulkRestoreObject", status, start) }()

	pgIds := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pgIds[i] = uuidToPgtype(id)
	}

	rows, err := r.db.Queries.BulkRestoreObject(ctx, tenantID, pgIds)
	if err != nil {
		status = domain.StatusError

		return 0, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows, nil
}

func (r *ObjectsRepo) BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "BulkDeleteObject", status, start) }()

	pgIds := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pgIds[i] = uuidToPgtype(id)
	}

	rows, err := r.db.Queries.BulkDeleteObject(ctx, tenantID, pgIds)
	if err != nil {
		status = domain.StatusError

		return 0, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows, nil
}

func (r *ObjectsRepo) GetStats(ctx context.Context, tenantID string) (*domain.ObjectStats, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetObjectStats", status, start) }()

	row, err := r.db.Queries.GetObjectStats(ctx, tenantID)
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	status = domain.StatusSuccess

	return &domain.ObjectStats{
		TotalCount:       row.TotalCount,
		TotalSize:        row.TotalSize,
		PendingCount:     row.PendingCount,
		UploadingCount:   row.UploadingCount,
		UploadedCount:    row.UploadedCount,
		CompleteCount:    row.CompleteCount,
		SoftDeletedCount: row.SoftDeletedCount,
	}, nil
}

func (r *ObjectsRepo) GetBucketStats(ctx context.Context, tenantID, bucket string) (int64, int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetBucketStats", status, start) }()

	row, err := r.db.Queries.GetBucketStats(ctx, tenantID, bucket)
	if err != nil {
		status = domain.StatusError

		return 0, 0, mapPgError(err)
	}

	status = domain.StatusSuccess

	return row.TotalObjects, row.TotalSizeBytes, nil
}

func (r *ObjectsRepo) BulkPatch(ctx context.Context, tenantID string, items []domain.BulkPatchItem) (int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "BulkPatchObject", status, start) }()

	batch := &pgx.Batch{}
	for _, item := range items {
		hasLabels := item.Labels != nil
		hasTags := item.Tags != nil
		hasRef := item.ExternalRef != nil

		switch {
		case hasLabels && hasTags && hasRef:
			lJSON, err := marshalStringMap(item.Labels)
			if err != nil {
				return 0, fmt.Errorf("marshal labels for %s: %w", item.ID, err)
			}
			tJSON, err := marshalStringMap(item.Tags)
			if err != nil {
				return 0, fmt.Errorf("marshal tags for %s: %w", item.ID, err)
			}
			batch.Queue(`UPDATE objects SET labels = labels || $3, tags = tags || $4, external_ref = $5, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), lJSON, tJSON, item.ExternalRef)
		case hasLabels && hasTags:
			lJSON, err := marshalStringMap(item.Labels)
			if err != nil {
				return 0, fmt.Errorf("marshal labels for %s: %w", item.ID, err)
			}
			tJSON, err := marshalStringMap(item.Tags)
			if err != nil {
				return 0, fmt.Errorf("marshal tags for %s: %w", item.ID, err)
			}
			batch.Queue(`UPDATE objects SET labels = labels || $3, tags = tags || $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), lJSON, tJSON)
		case hasLabels && hasRef:
			lJSON, err := marshalStringMap(item.Labels)
			if err != nil {
				return 0, fmt.Errorf("marshal labels for %s: %w", item.ID, err)
			}
			batch.Queue(`UPDATE objects SET labels = labels || $3, external_ref = $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), lJSON, item.ExternalRef)
		case hasTags && hasRef:
			tJSON, err := marshalStringMap(item.Tags)
			if err != nil {
				return 0, fmt.Errorf("marshal tags for %s: %w", item.ID, err)
			}
			batch.Queue(`UPDATE objects SET tags = tags || $3, external_ref = $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), tJSON, item.ExternalRef)
		case hasLabels:
			lJSON, err := marshalStringMap(item.Labels)
			if err != nil {
				return 0, fmt.Errorf("marshal labels for %s: %w", item.ID, err)
			}
			batch.Queue(`UPDATE objects SET labels = labels || $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), lJSON)
		case hasTags:
			tJSON, err := marshalStringMap(item.Tags)
			if err != nil {
				return 0, fmt.Errorf("marshal tags for %s: %w", item.ID, err)
			}
			batch.Queue(`UPDATE objects SET tags = tags || $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), tJSON)
		case hasRef:
			batch.Queue(`UPDATE objects SET external_ref = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenantID, uuidToPgtype(item.ID), item.ExternalRef)
		}
	}

	if batch.Len() == 0 {
		return 0, nil
	}

	br := r.db.Pool.SendBatch(ctx, batch)
	defer func() {
		if err := br.Close(); err != nil {
			r.db.log.Warn("close batch results failed", zap.Error(err))
		}
	}()

	var totalRows int64
	for i := 0; i < batch.Len(); i++ {
		ct, err := br.Exec()
		if err != nil {
			status = domain.StatusError

			return totalRows, mapPgError(err)
		}
		totalRows += ct.RowsAffected()
	}

	status = domain.StatusSuccess

	return totalRows, nil
}

func (r *ObjectsRepo) Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, tags map[string]string, externalRef *string) (*domain.Object, error) {
	var obj sqlc.Object
	var err error

	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "PatchObject", status, start) }()

	// Determine which query to use based on what's being patched
	switch {
	case labels != nil && tags != nil && externalRef != nil:
		var labelsJSON, tagsJSON []byte
		labelsJSON, err = marshalStringMap(labels)
		if err == nil {
			tagsJSON, err = marshalStringMap(tags)
		}
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
		row, errPkg := r.db.Queries.PatchObjectLabelsTagsAndExternalRef(ctx, tenantID, uuidToPgtype(id), labelsJSON, tagsJSON, externalRef)
		err = errPkg
		obj = row.Object
	case labels != nil && tags != nil:
		var labelsJSON, tagsJSON []byte
		labelsJSON, err = marshalStringMap(labels)
		if err == nil {
			tagsJSON, err = marshalStringMap(tags)
		}
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
		row, errPkg := r.db.Queries.PatchObjectLabelsAndTags(ctx, tenantID, uuidToPgtype(id), labelsJSON, tagsJSON)
		err = errPkg
		obj = row.Object
	case tags != nil && externalRef != nil:
		var tagsJSON []byte
		tagsJSON, err = marshalStringMap(tags)
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("marshal tags: %w", err)
		}
		row, errPkg := r.db.Queries.PatchObjectTagsAndExternalRef(ctx, tenantID, uuidToPgtype(id), tagsJSON, externalRef)
		err = errPkg
		obj = row.Object
	case labels != nil && externalRef != nil:
		var labelsJSON []byte
		labelsJSON, err = marshalStringMap(labels)
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("marshal labels: %w", err)
		}
		row, errPkg := r.db.Queries.PatchObjectLabelsAndExternalRef(ctx, tenantID, uuidToPgtype(id), labelsJSON, externalRef)
		err = errPkg
		obj = row.Object
	case labels != nil:
		var labelsJSON []byte
		labelsJSON, err = marshalStringMap(labels)
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("marshal labels: %w", err)
		}
		row, errPkg := r.db.Queries.PatchObjectLabels(ctx, tenantID, uuidToPgtype(id), labelsJSON)
		err = errPkg
		obj = row.Object
	case tags != nil:
		var tagsJSON []byte
		tagsJSON, err = marshalStringMap(tags)
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("marshal tags: %w", err)
		}
		row, errPkg := r.db.Queries.PatchObjectTags(ctx, tenantID, uuidToPgtype(id), tagsJSON)
		err = errPkg
		obj = row.Object
	case externalRef != nil:
		row, errPkg := r.db.Queries.PatchObjectExternalRef(ctx, tenantID, uuidToPgtype(id), externalRef)
		err = errPkg
		obj = row.Object
	default:
		// Nothing to patch, just fetch the current object
		status = domain.StatusSuccess

		return r.Get(ctx, tenantID, id)
	}

	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	result, err := mapToDomainObject(obj)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

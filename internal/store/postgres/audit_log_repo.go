package postgres

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
)

type AuditLogRepo struct {
	db *DB
}

func NewAuditLogRepo(db *DB) *AuditLogRepo {
	return &AuditLogRepo{db: db}
}

func (r *AuditLogRepo) Create(ctx context.Context, log domain.AuditLog) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "CreateAuditLog", status, start) }()

	queryParams, err := marshalJSONB(log.QueryParams)
	if err != nil {
		status = "error"
		return fmt.Errorf("marshal query params: %w", err)
	}

	requestHeaders, err := marshalJSONB(log.RequestHeaders)
	if err != nil {
		return fmt.Errorf("marshal request headers: %w", err)
	}

	var httpStatus *int32
	if log.HTTPStatus != nil {
		status := int32(*log.HTTPStatus)
		httpStatus = &status
	}

	var responseTimeMS *int32
	if log.ResponseTimeMS != nil {
		ms := int32(*log.ResponseTimeMS)
		responseTimeMS = &ms
	}

	// ClientIP is scanned as *netip.Addr — the correct pgx v5 native type for PostgreSQL inet.
	var clientIP *netip.Addr
	if log.ClientIP != nil {
		if addr, err := netip.ParseAddr(*log.ClientIP); err == nil {
			clientIP = &addr
		}
	}

	err = r.db.Queries.CreateAuditLog(ctx,
		uuidToPgtype(log.ID),
		log.TenantID,
		log.RequestID,
		log.IdempotencyKey,
		log.ActorSubject,
		string(log.ActorType),
		clientIP,
		log.UserAgent,
		log.Method,
		log.Path,
		queryParams,
		requestHeaders,
		log.RequestBodySHA256,
		log.RequestSizeBytes,
		httpStatus,
		log.ResponseCode,
		log.ResponseStatus,
		responseTimeMS,
		timestampToPgtype(log.CreatedAt),
	)

	if err != nil {
		status = "error"
		return MapPgError(err)
	}

	status = "success"
	return nil
}

func (r *AuditLogRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.AuditLog, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetAuditLog", status, start) }()

	row, err := r.db.Queries.GetAuditLog(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = "error"
		return nil, MapPgError(err)
	}

	result, err := MapGetAuditLogRowToDomain(row)
	if err != nil {
		status = "error"
		return nil, err
	}

	status = "success"
	return &result, nil
}

func (r *AuditLogRepo) List(ctx context.Context, tenantID string, filter domain.ListAuditLogsFilter, limit int, cursor string) ([]domain.AuditLog, string, int64, error) {
	var cursorTime pgtype.Timestamptz
	if cursor != "" {
		t, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", 0, fmt.Errorf("invalid cursor: %w", err)
		}
		cursorTime = timestampToPgtype(t)
	}

	var httpStatus *int32
	if filter.HTTPStatus != nil {
		status := int32(*filter.HTTPStatus)
		httpStatus = &status
	}

	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "ListAuditLogs", opStatus, start) }()

	rows, err := r.db.Queries.ListAuditLogs(ctx,
		tenantID,
		int32(limit+1),
		timestampPtrToPgtype(filter.From),
		timestampPtrToPgtype(filter.To),
		filter.Path,
		filter.PathPrefix,
		filter.Method,
		httpStatus,
		filter.RequestID,
		filter.IdempotencyKey,
		cursorTime,
	)
	if err != nil {
		opStatus = "error"
		return nil, "", 0, MapPgError(err)
	}

	var totalCount int64
	if len(rows) > 0 {
		totalCount = rows[0].TotalCount
	}

	logs := make([]domain.AuditLog, 0, len(rows))
	for _, row := range rows {
		log, err := MapListAuditLogsRowToDomain(row)
		if err != nil {
			return nil, "", 0, fmt.Errorf("map audit log: %w", err)
		}
		logs = append(logs, log)
	}

	opStatus = "success"

	nextCursor := ""
	if len(logs) > limit {
		nextCursor = logs[limit-1].CreatedAt.Format(time.RFC3339Nano)
		logs = logs[:limit]
	}

	return logs, nextCursor, totalCount, nil
}

func (r *AuditLogRepo) Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "PruneAuditLogs", status, start) }()

	rows, err := r.db.Queries.PruneAuditLogs(ctx, timestampToPgtype(cutoff), int32(limit))
	if err != nil {
		status = "error"
		return 0, MapPgError(err)
	}

	status = "success"
	return rows, nil
}

package postgres

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

type AuditLogRepo struct {
	db *DB
}

func NewAuditLogRepo(db *DB) *AuditLogRepo {
	return &AuditLogRepo{db: db}
}

func (r *AuditLogRepo) Create(ctx context.Context, log domain.AuditLog) error {
	queryParams, err := marshalJSONB(log.QueryParams)
	if err != nil {
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

	// Convert string ClientIP to *netip.Addr for database storage
	var clientIP *netip.Addr
	if log.ClientIP != nil {
		addr, err := netip.ParseAddr(*log.ClientIP)
		if err == nil {
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

	return MapPgError(err)
}

func (r *AuditLogRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.AuditLog, error) {
	log, err := r.db.Queries.GetAuditLog(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		return nil, MapPgError(err)
	}

	result, err := MapGetAuditLogRowToDomain(log)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (r *AuditLogRepo) List(ctx context.Context, tenantID string, filter domain.ListAuditLogsFilter, limit int, cursor string) ([]domain.AuditLog, string, error) {
	var cursorTime pgtype.Timestamptz
	if cursor != "" {
		t, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		cursorTime = timestampToPgtype(t)
	}

	var httpStatus *int32
	if filter.HTTPStatus != nil {
		status := int32(*filter.HTTPStatus)
		httpStatus = &status
	}

	rows, err := r.db.Queries.ListAuditLogs(ctx,
		tenantID,
		int32(limit+1), // Fetch one extra to determine if there's a next page
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
		return nil, "", MapPgError(err)
	}

	logs := make([]domain.AuditLog, 0, len(rows))
	for _, row := range rows {
		log, err := MapListAuditLogsRowToDomain(row)
		if err != nil {
			return nil, "", fmt.Errorf("map audit log: %w", err)
		}
		logs = append(logs, log)
	}

	nextCursor := ""
	if len(logs) > limit {
		nextCursor = logs[limit-1].CreatedAt.Format(time.RFC3339Nano)
		logs = logs[:limit]
	}

	return logs, nextCursor, nil
}

func (r *AuditLogRepo) Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	rows, err := r.db.Queries.PruneAuditLogs(ctx, timestampToPgtype(cutoff), int32(limit))
	if err != nil {
		return 0, MapPgError(err)
	}

	return rows, nil
}

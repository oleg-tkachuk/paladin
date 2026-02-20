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

// netipAddrToString converts a *netip.Addr to *string for the domain model.
func netipAddrToString(addr *netip.Addr) *string {
	if addr == nil || !addr.IsValid() {
		return nil
	}
	s := addr.String()
	return &s
}

// mapRowToDomain converts raw scan variables into a domain.AuditLog.
// client_ip is scanned as *netip.Addr — the correct pgx v5 native type for PostgreSQL inet.
func mapRowToDomain(
	pgID pgtype.UUID,
	tenantID string,
	requestID *string,
	idempotencyKey *string,
	actorSubject *string,
	actorType string,
	clientIP *netip.Addr,
	userAgent *string,
	method string,
	path string,
	queryParams []byte,
	requestHeaders []byte,
	requestBodySHA256 *string,
	requestSizeBytes *int64,
	httpStatus *int32,
	responseCode *string,
	responseStatus *string,
	responseTimeMS *int32,
	createdAt pgtype.Timestamptz,
) (domain.AuditLog, error) {
	domainID, err := uuidFromPgtype(pgID)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("convert audit log id: %w", err)
	}

	qp, err := unmarshalJSONB(queryParams)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("unmarshal query params: %w", err)
	}

	rh, err := unmarshalJSONB(requestHeaders)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("unmarshal request headers: %w", err)
	}

	var domainHTTPStatus *int
	if httpStatus != nil {
		s := int(*httpStatus)
		domainHTTPStatus = &s
	}

	var domainResponseTimeMS *int
	if responseTimeMS != nil {
		ms := int(*responseTimeMS)
		domainResponseTimeMS = &ms
	}

	return domain.AuditLog{
		ID:                domainID,
		TenantID:          tenantID,
		RequestID:         requestID,
		IdempotencyKey:    idempotencyKey,
		ActorSubject:      actorSubject,
		ActorType:         domain.ActorType(actorType),
		ClientIP:          netipAddrToString(clientIP),
		UserAgent:         userAgent,
		Method:            method,
		Path:              path,
		QueryParams:       qp,
		RequestHeaders:    rh,
		RequestBodySHA256: requestBodySHA256,
		RequestSizeBytes:  requestSizeBytes,
		HTTPStatus:        domainHTTPStatus,
		ResponseCode:      responseCode,
		ResponseStatus:    responseStatus,
		ResponseTimeMS:    domainResponseTimeMS,
		CreatedAt:         timestampFromPgtype(createdAt),
	}, nil
}

// getAuditLogSQL selects client_ip as the native inet type.
// pgx v5 scans inet columns into *netip.Addr natively in binary protocol.
const getAuditLogSQL = `
SELECT
    id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
    client_ip, user_agent, method, path, query_params, request_headers,
    request_body_sha256, request_size_bytes, http_status, response_code,
    response_status, response_time_ms, created_at
FROM audit_logs
WHERE tenant_id = $1 AND id = $2`

func (r *AuditLogRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.AuditLog, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetAuditLog", status, start) }()

	row := r.db.Pool.QueryRow(ctx, getAuditLogSQL, tenantID, uuidToPgtype(id))

	var (
		pgID             pgtype.UUID
		pgTenantID       string
		pgRequestID      *string
		pgIdempotencyKey *string
		pgActorSubject   *string
		pgActorType      string
		pgClientIP       *netip.Addr // correct pgx v5 scan type for inet
		pgUserAgent      *string
		pgMethod         string
		pgPath           string
		pgQueryParams    []byte
		pgRequestHeaders []byte
		pgRequestBodySHA *string
		pgRequestSize    *int64
		pgHTTPStatus     *int32
		pgResponseCode   *string
		pgResponseStatus *string
		pgResponseTimeMS *int32
		pgCreatedAt      pgtype.Timestamptz
	)

	err := row.Scan(
		&pgID, &pgTenantID, &pgRequestID, &pgIdempotencyKey, &pgActorSubject,
		&pgActorType, &pgClientIP, &pgUserAgent, &pgMethod, &pgPath,
		&pgQueryParams, &pgRequestHeaders, &pgRequestBodySHA, &pgRequestSize,
		&pgHTTPStatus, &pgResponseCode, &pgResponseStatus, &pgResponseTimeMS, &pgCreatedAt,
	)
	if err != nil {
		status = "error"
		return nil, MapPgError(err)
	}

	result, err := mapRowToDomain(
		pgID, pgTenantID, pgRequestID, pgIdempotencyKey, pgActorSubject,
		pgActorType, pgClientIP, pgUserAgent, pgMethod, pgPath,
		pgQueryParams, pgRequestHeaders, pgRequestBodySHA, pgRequestSize,
		pgHTTPStatus, pgResponseCode, pgResponseStatus, pgResponseTimeMS, pgCreatedAt,
	)
	if err != nil {
		status = "error"
		return nil, err
	}

	status = "success"
	return &result, nil
}

// listAuditLogsSQL selects client_ip as the native inet type.
const listAuditLogsSQL = `
SELECT
    id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
    client_ip, user_agent, method, path, query_params, request_headers,
    request_body_sha256, request_size_bytes, http_status, response_code,
    response_status, response_time_ms, created_at
FROM audit_logs
WHERE tenant_id = $1
  AND ($3::timestamptz IS NULL OR created_at >= $3)
  AND ($4::timestamptz IS NULL OR created_at < $4)
  AND ($5::text IS NULL OR path = $5)
  AND ($6::text IS NULL OR path LIKE $6 || '%')
  AND ($7::text IS NULL OR method = $7)
  AND ($8::int IS NULL OR http_status = $8)
  AND ($9::text IS NULL OR request_id = $9)
  AND ($10::text IS NULL OR idempotency_key = $10)
  AND ($11::timestamptz IS NULL OR created_at < $11)
ORDER BY created_at DESC, id DESC
LIMIT $2`

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

	start := time.Now()
	var opStatus string
	defer func() { metrics.RecordDbQuery(ctx, "ListAuditLogs", opStatus, start) }()

	rows, err := r.db.Pool.Query(ctx, listAuditLogsSQL,
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
		opStatus = "error"
		return nil, "", MapPgError(err)
	}
	defer rows.Close()

	logs := make([]domain.AuditLog, 0, limit+1)
	for rows.Next() {
		var (
			pgID             pgtype.UUID
			pgTenantID       string
			pgRequestID      *string
			pgIdempotencyKey *string
			pgActorSubject   *string
			pgActorType      string
			pgClientIP       *netip.Addr // correct pgx v5 scan type for inet
			pgUserAgent      *string
			pgMethod         string
			pgPath           string
			pgQueryParams    []byte
			pgRequestHeaders []byte
			pgRequestBodySHA *string
			pgRequestSize    *int64
			pgHTTPStatus     *int32
			pgResponseCode   *string
			pgResponseStatus *string
			pgResponseTimeMS *int32
			pgCreatedAt      pgtype.Timestamptz
		)

		if err := rows.Scan(
			&pgID, &pgTenantID, &pgRequestID, &pgIdempotencyKey, &pgActorSubject,
			&pgActorType, &pgClientIP, &pgUserAgent, &pgMethod, &pgPath,
			&pgQueryParams, &pgRequestHeaders, &pgRequestBodySHA, &pgRequestSize,
			&pgHTTPStatus, &pgResponseCode, &pgResponseStatus, &pgResponseTimeMS, &pgCreatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan audit log: %w", err)
		}

		log, err := mapRowToDomain(
			pgID, pgTenantID, pgRequestID, pgIdempotencyKey, pgActorSubject,
			pgActorType, pgClientIP, pgUserAgent, pgMethod, pgPath,
			pgQueryParams, pgRequestHeaders, pgRequestBodySHA, pgRequestSize,
			pgHTTPStatus, pgResponseCode, pgResponseStatus, pgResponseTimeMS, pgCreatedAt,
		)
		if err != nil {
			return nil, "", fmt.Errorf("map audit log: %w", err)
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		opStatus = "error"
		return nil, "", MapPgError(err)
	}

	opStatus = "success"

	nextCursor := ""
	if len(logs) > limit {
		nextCursor = logs[limit-1].CreatedAt.Format(time.RFC3339Nano)
		logs = logs[:limit]
	}

	return logs, nextCursor, nil
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

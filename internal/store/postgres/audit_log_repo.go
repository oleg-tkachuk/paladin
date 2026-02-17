package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

type AuditLogRepo struct {
	db *DB
}

func NewAuditLogRepo(db *DB) *AuditLogRepo {
	return &AuditLogRepo{db: db}
}

func (r *AuditLogRepo) Create(ctx context.Context, log domain.AuditLog) error {
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO audit_logs (
			id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
			client_ip, user_agent, method, path, query_params, request_headers,
			request_body_sha256, request_size_bytes, http_status, response_code,
			response_status, response_time_ms, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`,
		log.ID, log.TenantID, log.RequestID, log.IdempotencyKey, log.ActorSubject, log.ActorType,
		log.ClientIP, log.UserAgent, log.Method, log.Path, log.QueryParams, log.RequestHeaders,
		log.RequestBodySHA256, log.RequestSizeBytes, log.HTTPStatus, log.ResponseCode,
		log.ResponseStatus, log.ResponseTimeMS, log.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create audit log: %w", err)
	}
	return nil
}

func (r *AuditLogRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.AuditLog, error) {
	var log domain.AuditLog
	err := r.db.Pool.QueryRow(ctx, `
		SELECT 
			id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
			client_ip, user_agent, method, path, query_params, request_headers,
			request_body_sha256, request_size_bytes, http_status, response_code,
			response_status, response_time_ms, created_at
		FROM audit_logs
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, id).Scan(
		&log.ID, &log.TenantID, &log.RequestID, &log.IdempotencyKey, &log.ActorSubject, &log.ActorType,
		&log.ClientIP, &log.UserAgent, &log.Method, &log.Path, &log.QueryParams, &log.RequestHeaders,
		&log.RequestBodySHA256, &log.RequestSizeBytes, &log.HTTPStatus, &log.ResponseCode,
		&log.ResponseStatus, &log.ResponseTimeMS, &log.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get audit log: %w", err)
	}
	return &log, nil
}

func (r *AuditLogRepo) List(ctx context.Context, tenantID string, filter domain.ListAuditLogsFilter, limit int, cursor string) ([]domain.AuditLog, string, error) {
	query := `
		SELECT 
			id, tenant_id, request_id, idempotency_key, actor_subject, actor_type,
			client_ip, user_agent, method, path, query_params, request_headers,
			request_body_sha256, request_size_bytes, http_status, response_code,
			response_status, response_time_ms, created_at
		FROM audit_logs
		WHERE tenant_id = $1
	`
	args := []any{tenantID}
	argIdx := 2

	if filter.From != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, *filter.From)
		argIdx++
	}
	if filter.To != nil {
		query += fmt.Sprintf(" AND created_at < $%d", argIdx)
		args = append(args, *filter.To)
		argIdx++
	}
	if filter.Path != nil {
		query += fmt.Sprintf(" AND path = $%d", argIdx)
		args = append(args, *filter.Path)
		argIdx++
	}
	if filter.PathPrefix != nil {
		query += fmt.Sprintf(" AND path LIKE $%d", argIdx)
		args = append(args, *filter.PathPrefix+"%")
		argIdx++
	}
	if filter.Method != nil {
		query += fmt.Sprintf(" AND method = $%d", argIdx)
		args = append(args, *filter.Method)
		argIdx++
	}
	if filter.HTTPStatus != nil {
		query += fmt.Sprintf(" AND http_status = $%d", argIdx)
		args = append(args, *filter.HTTPStatus)
		argIdx++
	}
	if filter.RequestID != nil {
		query += fmt.Sprintf(" AND request_id = $%d", argIdx)
		args = append(args, *filter.RequestID)
		argIdx++
	}
	if filter.IdempotencyKey != nil {
		query += fmt.Sprintf(" AND idempotency_key = $%d", argIdx)
		args = append(args, *filter.IdempotencyKey)
		argIdx++
	}

	if cursor != "" {
		// Cursor format: created_at|id
		// For simplicity and matching existing pattern in objects_repo, using time-based cursor
		t, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		query += fmt.Sprintf(" AND created_at < $%d", argIdx)
		args = append(args, t)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", argIdx)
	args = append(args, limit+1)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []domain.AuditLog
	for rows.Next() {
		var log domain.AuditLog
		if err := rows.Scan(
			&log.ID, &log.TenantID, &log.RequestID, &log.IdempotencyKey, &log.ActorSubject, &log.ActorType,
			&log.ClientIP, &log.UserAgent, &log.Method, &log.Path, &log.QueryParams, &log.RequestHeaders,
			&log.RequestBodySHA256, &log.RequestSizeBytes, &log.HTTPStatus, &log.ResponseCode,
			&log.ResponseStatus, &log.ResponseTimeMS, &log.CreatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan audit log: %w", err)
		}
		logs = append(logs, log)
	}

	nextCursor := ""
	if len(logs) > limit {
		nextCursor = logs[limit-1].CreatedAt.Format(time.RFC3339Nano)
		logs = logs[:limit]
	}

	return logs, nextCursor, rows.Err()
}
func (r *AuditLogRepo) Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tag, err := r.db.Pool.Exec(ctx, `
		DELETE FROM audit_logs
		WHERE id IN (
			SELECT id FROM audit_logs
			WHERE created_at < $1
			LIMIT $2
		)
	`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("prune audit logs: %w", err)
	}
	return tag.RowsAffected(), nil
}

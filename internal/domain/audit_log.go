package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ActorType string

const (
	ActorTypeUser    ActorType = "user"
	ActorTypeService ActorType = "service"
	ActorTypeSystem  ActorType = "system"
)

type AuditLog struct {
	ID                uuid.UUID      `json:"id"`
	TenantID          string         `json:"tenant_id"`
	RequestID         *string        `json:"request_id,omitempty"`
	IdempotencyKey    *string        `json:"idempotency_key,omitempty"`
	ActorSubject      *string        `json:"actor_subject,omitempty"`
	ActorType         ActorType      `json:"actor_type"`
	ClientIP          *string        `json:"client_ip,omitempty"`
	UserAgent         *string        `json:"user_agent,omitempty"`
	Method            string         `json:"method"`
	Path              string         `json:"path"`
	QueryParams       map[string]any `json:"query_params"`
	RequestHeaders    map[string]any `json:"request_headers"`
	RequestBodySHA256 *string        `json:"request_body_sha256,omitempty"`
	RequestSizeBytes  *int64         `json:"request_size_bytes,omitempty"`
	HTTPStatus        *int           `json:"http_status,omitempty"`
	ResponseCode      *string        `json:"response_code,omitempty"`
	ResponseStatus    *string        `json:"response_status,omitempty"`
	ResponseTimeMS    *int           `json:"response_time_ms,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

type ListAuditLogsFilter struct {
	From           *time.Time
	To             *time.Time
	Path           *string
	PathPrefix     *string
	Method         *string
	HTTPStatus     *int
	RequestID      *string
	IdempotencyKey *string
}

type AuditLogRepository interface {
	Create(ctx context.Context, log AuditLog) error
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*AuditLog, error)
	List(ctx context.Context, tenantID string, filter ListAuditLogsFilter, limit int, cursor string) ([]AuditLog, string, int64, error)
	// Prune deletes logs older than the given cutoff time.
	// Returns the number of rows deleted.
	Prune(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

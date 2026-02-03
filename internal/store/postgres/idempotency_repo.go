package postgres

import (
	"context"
	"fmt"
	"time"
)

type IdempotencyRecord struct {
	TenantID     string
	Key          string
	RequestPath  string
	RequestHash  string
	ResponseCode int
	ResponseBody []byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type IdempotencyRepo struct {
	db *DB
}

func NewIdempotencyRepo(db *DB) *IdempotencyRepo { return &IdempotencyRepo{db: db} }

func (r *IdempotencyRepo) Get(ctx context.Context, tenantID string, key string) (*IdempotencyRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT tenant_id, idempotency_key, request_path, request_hash, response_code, response_body, created_at, expires_at
        FROM idempotency_keys
        WHERE tenant_id=$1 AND idempotency_key=$2
    `, tenantID, key)

	var rec IdempotencyRecord
	if err := row.Scan(&rec.TenantID, &rec.Key, &rec.RequestPath, &rec.RequestHash, &rec.ResponseCode, &rec.ResponseBody, &rec.CreatedAt, &rec.ExpiresAt); err != nil {
		return nil, fmt.Errorf("scan idempotency key: %w", err)
	}

	return &rec, nil
}

func (r *IdempotencyRepo) Save(ctx context.Context, rec IdempotencyRecord) error {
	if _, err := r.db.Pool.Exec(ctx, `
        INSERT INTO idempotency_keys (tenant_id, idempotency_key, request_path, request_hash, response_code, response_body, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)
        ON CONFLICT (tenant_id, idempotency_key) DO UPDATE
        SET request_path=EXCLUDED.request_path, request_hash=EXCLUDED.request_hash, response_code=EXCLUDED.response_code, response_body=EXCLUDED.response_body, expires_at=EXCLUDED.expires_at
    `, rec.TenantID, rec.Key, rec.RequestPath, rec.RequestHash, rec.ResponseCode, rec.ResponseBody, rec.ExpiresAt); err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}

	return nil
}

func (r *IdempotencyRepo) Delete(ctx context.Context, tenantID string, key string) error {
	if _, err := r.db.Pool.Exec(ctx, `
        DELETE FROM idempotency_keys WHERE tenant_id=$1 AND idempotency_key=$2
    `, tenantID, key); err != nil {
		return fmt.Errorf("delete idempotency key: %w", err)
	}
	return nil
}

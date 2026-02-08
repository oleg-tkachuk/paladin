package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestIdempotencyRepo_Save(t *testing.T) {
	mock, err := pgxmock.NewPool()
	assert.NoError(t, err)
	defer mock.Close()

	db := &DB{Pool: mock, log: zap.NewNop()}
	repo := NewIdempotencyRepo(db)
	ctx := context.Background()

	rec := IdempotencyRecord{
		TenantID:     "test-tenant",
		Key:          "key-123",
		RequestPath:  "/v1/test",
		RequestHash:  "hash-123",
		ResponseCode: 200,
		ResponseBody: []byte(`{"ok": true}`),
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}

	mock.ExpectExec("INSERT INTO idempotency_keys").
		WithArgs(rec.TenantID, rec.Key, rec.RequestPath, rec.RequestHash, rec.ResponseCode, rec.ResponseBody, rec.ExpiresAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Save(ctx, rec)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestIdempotencyRepo_Get(t *testing.T) {
	mock, err := pgxmock.NewPool()
	assert.NoError(t, err)
	defer mock.Close()

	db := &DB{Pool: mock, log: zap.NewNop()}
	repo := NewIdempotencyRepo(db)
	ctx := context.Background()

	tenantID := "test-tenant"
	key := "key-123"
	now := time.Now()

	columns := []string{"tenant_id", "idempotency_key", "request_path", "request_hash", "response_code", "response_body", "created_at", "expires_at"}
	mock.ExpectQuery("SELECT (.+) FROM idempotency_keys").
		WithArgs(tenantID, key).
		WillReturnRows(pgxmock.NewRows(columns).
			AddRow(tenantID, key, "/v1/test", "hash-123", 200, []byte(`{"ok": true}`), now, now.Add(1*time.Hour)))

	rec, err := repo.Get(ctx, tenantID, key)
	assert.NoError(t, err)
	assert.NotNil(t, rec)
	assert.Equal(t, key, rec.Key)
	assert.NoError(t, mock.ExpectationsWereMet())
}

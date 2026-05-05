package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type ApiKeyRepo struct {
	q *sqlc.Queries
}

func NewApiKeyRepo(q *sqlc.Queries) *ApiKeyRepo { return &ApiKeyRepo{q: q} }

var _ authstore.ApiKeyRepository = (*ApiKeyRepo)(nil)

func (r *ApiKeyRepo) Create(ctx context.Context, k authstore.ApiKey) (authstore.ApiKey, error) {
	if k.ApiKeyID == uuid.Nil {
		k.ApiKeyID = uuid.Must(uuid.NewV7())
	}
	roles, _ := json.Marshal(k.Roles)
	scopes, _ := json.Marshal(scopesToWire(k.Scopes))
	var expires pgtype.Timestamptz
	if k.ExpiresAt != nil {
		expires = pgTS(*k.ExpiresAt)
	}
	if err := r.q.CreateApiKey(ctx,
		pgUUID(k.ApiKeyID),
		pgUUID(k.TenantID),
		k.DisplayPrefix,
		k.Description,
		k.SecretHash,
		roles,
		scopes,
		expires,
	); err != nil {
		return authstore.ApiKey{}, fmt.Errorf("create api_key: %w", err)
	}
	return r.GetByID(ctx, k.ApiKeyID)
}

func (r *ApiKeyRepo) GetByID(ctx context.Context, id uuid.UUID) (authstore.ApiKey, error) {
	row, err := r.q.GetApiKeyByID(ctx, pgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authstore.ApiKey{}, authstore.ErrNotFound
		}
		return authstore.ApiKey{}, err
	}
	return apiKeyFromSQLC(row), nil
}

func (r *ApiKeyRepo) GetByPrefix(ctx context.Context, prefix string) (authstore.ApiKey, error) {
	row, err := r.q.GetApiKeyByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authstore.ApiKey{}, authstore.ErrNotFound
		}
		return authstore.ApiKey{}, err
	}
	return apiKeyFromSQLC(row), nil
}

func (r *ApiKeyRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	return r.q.RevokeApiKey(ctx, pgUUID(id))
}

func (r *ApiKeyRepo) UpdateSecretHash(ctx context.Context, id uuid.UUID, newHash []byte, oldGraceUntil time.Time) error {
	row, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return r.q.RotateApiKeySecret(ctx,
		pgUUID(id),
		newHash,
		row.SecretHash,
		pgTS(oldGraceUntil),
	)
}

func (r *ApiKeyRepo) TouchUse(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.q.TouchApiKeyUse(ctx, pgUUID(id), pgTS(at))
}

func (r *ApiKeyRepo) List(ctx context.Context, args authstore.ListApiKeysArgs) ([]authstore.ApiKey, string, error) {
	limit := int32(50)
	if args.PageSize > 0 && args.PageSize <= 1000 {
		limit = args.PageSize
	}
	var afterID uuid.UUID
	if args.PageToken != "" {
		id, err := uuid.Parse(args.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("invalid page_token: %w", err)
		}
		afterID = id
	}
	rows, err := r.q.ListApiKeysByTenant(ctx,
		pgUUID(args.TenantID),
		args.IncludeRevoked,
		pgUUID(afterID),
		limit,
	)
	if err != nil {
		return nil, "", err
	}
	out := make([]authstore.ApiKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiKeyFromSQLC(row))
	}
	var next string
	if int32(len(out)) == limit && len(out) > 0 {
		next = out[len(out)-1].ApiKeyID.String()
	}
	return out, next, nil
}

// ListExpired returns api_keys whose expires_at has passed and that are
// still active. Satisfies worker.ApiKeyExpirerRepo.
func (r *ApiKeyRepo) ListExpired(ctx context.Context, at time.Time, limit int32) ([]authstore.ApiKey, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.q.ListExpiredApiKeys(ctx, pgTS(at), limit)
	if err != nil {
		return nil, err
	}
	out := make([]authstore.ApiKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiKeyFromSQLC(row))
	}
	return out, nil
}

func apiKeyFromSQLC(k sqlc.ApiKey) authstore.ApiKey {
	var roles []string
	_ = json.Unmarshal(k.Roles, &roles)
	var scopeStrs []string
	_ = json.Unmarshal(k.Scopes, &scopeStrs)
	scopes, _ := auth.ParseScopes(scopeStrs)
	return authstore.ApiKey{
		ApiKeyID:      uuidFrom(k.ApiKeyID),
		TenantID:      uuidFrom(k.TenantID),
		DisplayPrefix: k.DisplayPrefix,
		Description:   k.Description,
		SecretHash:    k.SecretHash,
		Roles:         roles,
		Scopes:        scopes,
		Revoked:       k.Revoked,
		CreatedAt:     timeFrom(k.CreatedAt),
		ExpiresAt:     timePtr(k.ExpiresAt),
		LastUsedAt:    timePtr(k.LastUsedAt),
	}
}

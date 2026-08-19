package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// IdempotencyRepo backs the middleware.IdempotencyStore contract with
// the idempotency_keys table. The table is scoped per
// (tenant_id, method, key) and rows auto-expire via the worker's
// PurgeExpiredIdempotencyKeys loop — so the adapter never writes a
// soft-tombstone or touches a TTL column directly.
type IdempotencyRepo struct {
	q *sqlc.Queries
}

func NewIdempotencyRepo(q *sqlc.Queries) *IdempotencyRepo { return &IdempotencyRepo{q: q} }

var _ middleware.IdempotencyStore = (*IdempotencyRepo)(nil)

// Get returns (response, sha, found=true) when a non-expired row exists.
// The SQL `expires_at > now()` filter means an expired row reports
// found=false here; the middleware will re-execute the RPC and Put will
// upsert via ON CONFLICT DO NOTHING — which, after the worker purges
// the stale row, becomes a clean insert. Three-state return mirrors
// the store contract; (nil, nil, false, nil) means "not cached, run the
// RPC", and any non-nil err propagates as InternalError to the caller
// (rare — only pgx I/O failures).
func (r *IdempotencyRepo) Get(ctx context.Context, tenantID uuid.UUID, method, key string) ([]byte, []byte, bool, error) {
	row, err := r.q.GetIdempotencyKey(ctx, pgUUID(tenantID), method, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	return row.Response, row.ResponseSha, true, nil
}

// Put memoises a serialised response. The underlying SQL uses
// ON CONFLICT DO NOTHING — concurrent first-time requests for the
// same key race, the first writer wins, and the second writer is a
// silent no-op (its response is dropped, which is fine: clients see
// their own RPC's result, the next replay sees the winning one).
// Note: callers must NOT pass an empty `key` — the middleware
// upstream guards that, but the DB has no CHECK constraint so a stray
// empty would silently coalesce per-tenant. Audit the call site if
// you add a second caller in the future.
func (r *IdempotencyRepo) Put(ctx context.Context, tenantID uuid.UUID, method, key string, response, sha []byte, expiresAt time.Time) error {
	return r.q.PutIdempotencyKey(ctx, pgUUID(tenantID), method, key, response, sha, pgTS(expiresAt))
}

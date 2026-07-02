package cedar

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store fetches compiled Cedar policy text for a given (tenant, objectKey) scope
// and notifies subscribers on change.
//
// Effective policy is the concatenation of the tenant's inherited_cedar_policy
// and the objectKey's cedar_policy (objectKey-scoped rules override tenant-scoped).
type Store interface {
	// Fetch returns the effective policy text and a content hash.
	Fetch(ctx context.Context, tenantID uuid.UUID, objectKey string) (text string, hash []byte, err error)

	// Watch emits change events for invalidating compiled caches.
	// The channel is closed when ctx is cancelled.
	Watch(ctx context.Context) (<-chan ChangeEvent, error)
}

type ChangeEvent struct {
	TenantID  uuid.UUID
	ObjectKey string // empty = tenant-level change (invalidate all object_keys)
}

// PostgresStore reads policy text from tenants and object_keys and uses
// LISTEN/NOTIFY on channel "policy_changed" to stream invalidations.
// The NOTIFY side is emitted by AFTER INSERT/UPDATE/DELETE triggers on
// tenants.inherited_cedar_policy and object_keys.cedar_policy (migration
// 051_policy_changed_notify.sql), so every writer — admin plane, seed jobs,
// manual psql — invalidates without remembering to notify.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Fetch(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, []byte, error) {
	const q = `
        SELECT
            COALESCE(t.inherited_cedar_policy, '') AS tpolicy,
            COALESCE(b.cedar_policy, '')           AS bpolicy
        FROM tenants t
        LEFT JOIN object_keys b
               ON b.tenant_id = t.tenant_id AND b.object_key = $2
        WHERE t.tenant_id = $1
    `
	var tPol, bPol string
	if err := s.pool.QueryRow(ctx, q, tenantID, objectKey).Scan(&tPol, &bPol); err != nil {
		// Unknown tenant → no policy. Cedar's deny-by-default semantics
		// will then map the call to PermissionDenied at the engine layer
		// instead of leaking a SQL error as a 500 to the client.
		if errors.Is(err, pgx.ErrNoRows) {
			sum := sha256.Sum256(nil)
			return "", sum[:], nil
		}
		return "", nil, fmt.Errorf("policy fetch: %w", err)
	}
	text := tPol
	if bPol != "" {
		text += "\n// --- objectKey-scoped ---\n" + bPol
	}
	sum := sha256.Sum256([]byte(text))
	return text, sum[:], nil
}

func (s *PostgresStore) Watch(ctx context.Context) (<-chan ChangeEvent, error) {
	ch := make(chan ChangeEvent, 64)
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN policy_changed"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("listen: %w", err)
	}
	go func() {
		defer close(ch)
		defer conn.Release()
		for {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				return
			}
			// Payload format: "<tenant_uuid>:<objectKey>" (objectKey optional).
			ev := parseNotifyPayload(n.Payload)
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func parseNotifyPayload(p string) ChangeEvent {
	for i := 0; i < len(p); i++ {
		if p[i] == ':' {
			id, _ := uuid.Parse(p[:i])
			return ChangeEvent{TenantID: id, ObjectKey: p[i+1:]}
		}
	}
	id, _ := uuid.Parse(p)
	return ChangeEvent{TenantID: id}
}

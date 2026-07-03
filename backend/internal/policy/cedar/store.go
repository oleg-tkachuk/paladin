package cedar

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

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
	// Fetch returns the effective policy text, a content hash, and the
	// tenant's DB-authoritative slug. The slug is the trusted key for tenant
	// membership in Cedar (ADR-0012) — never the JWT-supplied one. Empty when
	// the tenant is unknown or has no slug (legacy); callers fall back to the
	// tenant UUID for the entity UID.
	Fetch(ctx context.Context, tenantID uuid.UUID, objectKey string) (text string, hash []byte, slug string, err error)

	// Watch emits change events for invalidating compiled caches.
	// The channel is closed when ctx is cancelled.
	Watch(ctx context.Context) (<-chan ChangeEvent, error)
}

type ChangeEvent struct {
	TenantID  uuid.UUID
	ObjectKey string // empty = tenant-level change (invalidate all object_keys)
	// ResyncAll is a control event, not a data change: the watcher lost and
	// re-established its LISTEN connection, so an unknown set of notifications
	// was missed in the gap. Consumers must drop their ENTIRE compiled cache
	// (every tenant) and re-fetch on demand. TenantID/ObjectKey are unset.
	ResyncAll bool
}

// PostgresStore reads policy text from tenants and object_keys and uses
// LISTEN/NOTIFY on channel "policy_changed" to stream invalidations.
// The NOTIFY side is emitted by AFTER INSERT/UPDATE/DELETE triggers on
// tenants.inherited_cedar_policy and object_keys.cedar_policy (migration
// 051_policy_changed_notify.sql), so every writer — admin plane, seed jobs,
// manual psql — invalidates without remembering to notify.
type PostgresStore struct {
	pool *pgxpool.Pool
	// reconnects counts how many times Watch lost its LISTEN connection and
	// re-established it. Read via WatchReconnects; a climbing value means the
	// DB link is flapping and invalidation is repeatedly falling back to the
	// TTL between drops.
	reconnects atomic.Uint64
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// WatchReconnects returns the number of times the Watch loop has re-acquired
// its LISTEN connection after a drop. Metric surface for observability /
// tests (mirrors the Engine's in-process counters pending OTEL export).
func (s *PostgresStore) WatchReconnects() uint64 { return s.reconnects.Load() }

// watchBackoffInitial / watchBackoffMax bound the re-LISTEN retry cadence
// after a connection drop. Exponential from the initial to the cap; small
// initial so a transient blip recovers fast, capped so a prolonged outage
// doesn't hot-loop the pool.
const (
	watchBackoffInitial = 100 * time.Millisecond
	watchBackoffMax     = 5 * time.Second
)

func (s *PostgresStore) Fetch(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, []byte, string, error) {
	const q = `
        SELECT
            COALESCE(t.inherited_cedar_policy, '') AS tpolicy,
            COALESCE(b.cedar_policy, '')           AS bpolicy,
            COALESCE(t.slug, '')                   AS slug
        FROM tenants t
        LEFT JOIN object_keys b
               ON b.tenant_id = t.tenant_id AND b.object_key = $2
        WHERE t.tenant_id = $1
    `
	var tPol, bPol, slug string
	if err := s.pool.QueryRow(ctx, q, tenantID, objectKey).Scan(&tPol, &bPol, &slug); err != nil {
		// Unknown tenant → no policy. Cedar's deny-by-default semantics
		// will then map the call to PermissionDenied at the engine layer
		// instead of leaking a SQL error as a 500 to the client.
		if errors.Is(err, pgx.ErrNoRows) {
			sum := sha256.Sum256(nil)
			return "", sum[:], "", nil
		}
		return "", nil, "", fmt.Errorf("policy fetch: %w", err)
	}
	text := tPol
	if bPol != "" {
		text += "\n// --- objectKey-scoped ---\n" + bPol
	}
	sum := sha256.Sum256([]byte(text))
	return text, sum[:], slug, nil
}

func (s *PostgresStore) Watch(ctx context.Context) (<-chan ChangeEvent, error) {
	ch := make(chan ChangeEvent, 64)
	// The FIRST listen is synchronous so a misconfiguration (missing GRANT,
	// unreachable DB at boot) surfaces as a Start error rather than a
	// silently-degraded engine. Drops AFTER this point are recovered in the
	// loop instead — there's no caller left to return an error to.
	conn, err := s.listen(ctx)
	if err != nil {
		return nil, err
	}
	go s.watchLoop(ctx, conn, ch)
	return ch, nil
}

// listen acquires a pooled connection and issues LISTEN policy_changed on it.
// The caller owns Release.
func (s *PostgresStore) listen(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN policy_changed"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("listen: %w", err)
	}
	return conn, nil
}

// watchLoop consumes notifications until ctx is cancelled. When the LISTEN
// connection drops (Postgres restart, failover, network blip) it re-acquires
// with backoff and emits a ResyncAll so consumers flush caches that may have
// missed events during the gap. Only ctx cancellation ends the loop.
func (s *PostgresStore) watchLoop(ctx context.Context, conn *pgxpool.Conn, ch chan ChangeEvent) {
	defer close(ch)
	for {
		err := consumeNotifications(ctx, conn, ch)
		conn.Release()
		if ctx.Err() != nil {
			return // clean shutdown
		}
		_ = err // non-nil: the connection dropped mid-watch — reconnect.

		conn = s.relisten(ctx)
		if conn == nil {
			return // ctx cancelled while backing off
		}
		s.reconnects.Add(1)
		// Re-LISTEN succeeded; notifications fired during the gap are lost, so
		// force a full resync. Any NOTIFY that races in just after the new
		// LISTEN is redundant against this flush, never lost.
		select {
		case ch <- ChangeEvent{ResyncAll: true}:
		case <-ctx.Done():
			conn.Release()
			return
		}
	}
}

// consumeNotifications blocks on the conn, forwarding parsed events until the
// connection errors (returned) or ctx is cancelled.
func consumeNotifications(ctx context.Context, conn *pgxpool.Conn, ch chan ChangeEvent) error {
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		// Payload format: "<tenant_uuid>:<objectKey>" (objectKey optional).
		ev := parseNotifyPayload(n.Payload)
		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// relisten retries listen() with exponential backoff until it succeeds or ctx
// is cancelled (returns nil).
func (s *PostgresStore) relisten(ctx context.Context) *pgxpool.Conn {
	backoff := watchBackoffInitial
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if conn, err := s.listen(ctx); err == nil {
			return conn
		}
		if backoff *= 2; backoff > watchBackoffMax {
			backoff = watchBackoffMax
		}
	}
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

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
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// Store fetches compiled Cedar policy text for a given (tenant, collection) scope
// and notifies subscribers on change.
//
// Effective policy is the concatenation of the tenant's inherited_cedar_policy
// and the collection's cedar_policy (collection-scoped rules override tenant-scoped).
// Layers are the tenant-authored policy texts that apply to one scope, kept
// APART rather than pre-joined.
//
// Joining them is the engine's business, not the store's, and the difference
// is not cosmetic: concatenating first means one layer's syntax error takes
// the others down with it. A collection whose policy will not parse used to
// void the tenant's inherited policy for that scope AND the platform's
// built-in — including the unconditional platform.admin permit, which is what
// left the entity unreadable, undeletable and unrepairable at once. Separate
// texts let the engine degrade exactly the layer that is broken.
//
// Order is fixed and meaningful: Tenant is inherited by every collection,
// Collection applies to one. Neither includes the built-in layer, which is a
// constant in the engine and belongs to no tenant.
type Layers struct {
	// Tenant is tenants.inherited_cedar_policy — platform-authored (written by
	// UpdateTenant under platform.admin, or a policy-only edit by
	// platform.tenant-provisioner). Empty is normal.
	Tenant string
	// Collection is collections.cedar_policy for the requested collection.
	// Empty when the scope is the tenant itself, or the collection has none.
	Collection string
}

type Store interface {
	// Fetch returns the effective policy text, a content hash, and the
	// tenant's DB-authoritative slug. The slug is the trusted key for tenant
	// membership in Cedar (ADR-0016) — never the JWT-supplied one. Empty when
	// the tenant is unknown or has no slug (legacy); callers fall back to the
	// tenant UUID for the entity UID.
	Fetch(ctx context.Context, tenantID uuid.UUID, collection string) (layers Layers, hash []byte, slug string, err error)

	// Watch emits change events for invalidating compiled caches.
	// The channel is closed when ctx is cancelled.
	Watch(ctx context.Context) (<-chan ChangeEvent, error)
}

type ChangeEvent struct {
	TenantID   uuid.UUID
	Collection string // empty = tenant-level change (invalidate all collections)
	// ResyncAll is a control event, not a data change: the watcher lost and
	// re-established its LISTEN connection, so an unknown set of notifications
	// was missed in the gap. Consumers must drop their ENTIRE compiled cache
	// (every tenant) and re-fetch on demand. TenantID/Collection are unset.
	ResyncAll bool
}

// PostgresStore reads policy text from tenants and collections and uses
// LISTEN/NOTIFY on channel "policy_changed" to stream invalidations.
// The NOTIFY side is emitted by AFTER INSERT/UPDATE/DELETE triggers on
// tenants.inherited_cedar_policy and collections.cedar_policy (migration
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

func (s *PostgresStore) Fetch(ctx context.Context, tenantID uuid.UUID, collection string) (Layers, []byte, string, error) {
	const q = `
        SELECT
            COALESCE(t.inherited_cedar_policy, '') AS tpolicy,
            COALESCE(b.cedar_policy, '')           AS bpolicy,
            COALESCE(t.slug, '')                   AS slug
        FROM tenants t
        LEFT JOIN collections b
               ON b.tenant_id = t.id AND b.name = $2
        WHERE t.id = $1
    `
	var tPol, bPol, slug string
	if err := s.pool.QueryRow(ctx, q, tenantID, collection).Scan(&tPol, &bPol, &slug); err != nil {
		// Unknown tenant → no policy. Cedar's deny-by-default semantics
		// will then map the call to PermissionDenied at the engine layer
		// instead of leaking a SQL error as a 500 to the client.
		if errors.Is(err, pgx.ErrNoRows) {
			sum := sha256.Sum256(nil)
			return Layers{}, sum[:], "", nil
		}
		return Layers{}, nil, "", fmt.Errorf("policy fetch: %w", err)
	}
	// Hashed over both layers with a separator, so a change that moves text
	// from one layer to the other still invalidates the cache.
	sum := sha256.Sum256([]byte(tPol + "\x00" + bPol))
	return Layers{Tenant: tPol, Collection: bPol}, sum[:], slug, nil
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
		// Expected when the connection simply dropped, and NOT expected for
		// anything else — a permission change or a missing channel would spin
		// this loop reconnecting forever. Which of the two it is can only be
		// told from the error, and until now nothing wrote it down.
		logger.FromContext(ctx).Warn("cedar policy watch dropped; reconnecting",
			zap.Error(err))

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
		// Payload format: "<tenant_uuid>:<collection>" (collection optional).
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
			return ChangeEvent{TenantID: id, Collection: p[i+1:]}
		}
	}
	id, _ := uuid.Parse(p)
	return ChangeEvent{TenantID: id}
}

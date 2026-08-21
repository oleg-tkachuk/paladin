// Package auditstream is the realtime half of the audit console: migration
// 050's trigger fires a Postgres NOTIFY ("paladin_audit") for every audit_log
// insert, the Hub fans those notifications out per tenant in-process, and
// the SSE handler (sse.go) streams them to the browser. One LISTEN
// connection per admin pod regardless of subscriber count.
package auditstream

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Channel is the NOTIFY channel the trigger baseline (003_triggers.sql)'s trigger fires on.
const Channel = "paladin_audit"

// Entry is the compact audit projection carried in the NOTIFY payload —
// everything the live console renders. before/after diffs are deliberately
// absent (NOTIFY payloads are size-capped); a client that needs the diff
// fetches the entry by id via AuditService.GetAuditLogEntry.
type Entry struct {
	EntryID       string `json:"entry_id"`
	At            string `json:"at"`
	ActorSubject  string `json:"actor_subject"`
	ActorTenantID string `json:"actor_tenant_id"`
	ActorAudience string `json:"actor_audience"`
	Action        string `json:"action"`
	ResourceName  string `json:"resource_name"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

// Hub fans audit notifications out to per-tenant subscribers. Subscribers
// are SSE connections; a slow one gets dropped events (non-blocking send on
// a buffered channel) rather than back-pressuring the LISTEN loop — the
// audit console is a live *view*, the durable record stays in audit_log.
type Hub struct {
	log *zap.Logger

	mu   sync.Mutex
	subs map[string]map[chan Entry]struct{} // tenant_id → subscriber set
}

func NewHub(log *zap.Logger) *Hub {
	if log == nil {
		log = zap.NewNop()
	}
	return &Hub{log: log, subs: map[string]map[chan Entry]struct{}{}}
}

// Subscribe registers a subscriber for one tenant's audit events. The
// returned cancel MUST be called when the consumer goes away; the channel is
// closed by cancel, never by the hub's dispatch path.
func (h *Hub) Subscribe(tenantID string) (<-chan Entry, func()) {
	ch := make(chan Entry, 64)
	h.mu.Lock()
	set, ok := h.subs[tenantID]
	if !ok {
		set = map[chan Entry]struct{}{}
		h.subs[tenantID] = set
	}
	set[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			// Remove + close under the same lock Dispatch sends under, so a
			// concurrent Dispatch can never write to the closed channel.
			h.mu.Lock()
			defer h.mu.Unlock()
			if set, ok := h.subs[tenantID]; ok {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.subs, tenantID)
				}
			}
			close(ch)
		})
	}
	return ch, cancel
}

// Dispatch decodes one NOTIFY payload and routes it to the tenant's
// subscribers. Rows without an actor tenant (platform-level events with a
// NULL actor_tenant_id) currently reach no subscriber — the stream is
// tenant-scoped by design. Non-blocking: a full subscriber buffer drops the
// event for that subscriber only.
func (h *Hub) Dispatch(payload string) {
	var e Entry
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		h.log.Warn("audit stream: undecodable NOTIFY payload", zap.Error(err))
		return
	}
	if e.ActorTenantID == "" {
		return
	}
	// Send under the lock: every send is a non-blocking select into a
	// buffered channel (cheap), and holding the lock means cancel() can never
	// close a channel a concurrent Dispatch is about to send on.
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[e.ActorTenantID] {
		select {
		case ch <- e:
		default: // slow consumer — drop, never block the LISTEN loop
		}
	}
}

// Run holds the LISTEN connection and pumps notifications into Dispatch
// until ctx is done, re-acquiring the connection with a fixed backoff after
// any failure (pod-local resilience mirrors cedar's PostgresStore.Watch,
// plus reconnect). Blocking — run it in a goroutine from the composition
// root.
func (h *Hub) Run(ctx context.Context, pool *pgxpool.Pool) {
	const retryAfter = 5 * time.Second
	for ctx.Err() == nil {
		if err := h.listenOnce(ctx, pool); err != nil && ctx.Err() == nil {
			h.log.Warn("audit stream: LISTEN loop failed; reconnecting",
				zap.Duration("retry_after", retryAfter), zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryAfter):
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.Dispatch(n.Payload)
	}
}

// Event dispatcher — durable webhook fan-out.
//
// Pre-slice-9 this lived in-process inside the admin pod and POSTed
// to customer sinks synchronously on the request path. Two problems
// with that:
//
//   - One slow customer sink stalls admin's connection pool.
//   - Admin pod crash = events lost forever (no durability).
//
// The current shape:
//
//	┌─ admin handler (producer) ─────────────────────────┐
//	│  Dispatcher.Dispatch(ctx, tenantID, evt):          │
//	│    Store.List → filter-match → INSERT one          │
//	│    event_deliveries row per matching sub.          │
//	│    Returns immediately. No HTTP I/O.               │
//	└────────────────────────────────────────────────────┘
//	                    │
//	                    ▼
//	            event_deliveries (outbox)
//	                    │
//	                    ▼
//	┌─ dispatcher pod (consumer) ───────────────────────┐
//	│  OutboxRunner.Run(ctx):                            │
//	│    SELECT ... FROM event_deliveries WHERE          │
//	│      status='pending' AND next_attempt_at<=now()   │
//	│      FOR UPDATE SKIP LOCKED LIMIT N                │
//	│    Per row: deliver(); update status/attempts.     │
//	└────────────────────────────────────────────────────┘
//
// TestSubscription RPC keeps Dispatcher.DeliverOne — operator clicked
// "Test Webhook", they want the result now, no outbox row written.
//
// Sinks: HTTP (signed POST) is wired. Kafka / SQS branch-out from
// deliver() the same way the previous in-process path did.
package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// Event is the wire payload delivered to subscribers. The shape is JSON-
// stable across releases; new fields go to the end.
type Event struct {
	Type         string         `json:"type"` // "object.created", "bucket.created", ...
	At           time.Time      `json:"at"`
	TenantID     string         `json:"tenant_id"`
	ResourceName string         `json:"resource_name"`
	ActorSubject string         `json:"actor_subject,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
}

// SubscriptionStore is the read seam the producer uses to look up subs
// at fan-out time, and the dispatcher pod uses to load a sub by ID at
// delivery time. Defined narrowly so tests can stub.
type SubscriptionStore interface {
	List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error)
	Get(ctx context.Context, id uuid.UUID) (admindomain.EventSubscription, error)
}

// OutboxWriter is the producer-side write seam. Implemented by a thin
// wrapper around *pgxpool.Pool in production; tests substitute a fake.
type OutboxWriter interface {
	Insert(ctx context.Context, row OutboxRow) error
}

// OutboxRow is the shape of a single row inserted by the producer and
// consumed by the dispatcher pod. Mirrors the migration 028 columns
// the producer is responsible for stamping.
type OutboxRow struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	SubscriptionID uuid.UUID
	EventType      string
	EventAt        time.Time
	EventPayload   []byte // raw JSON
}

// Dispatcher is the producer-side helper used by admin handlers. It
// resolves subscriptions, runs the filter-match, and writes one outbox
// row per match. It also retains DeliverOne for the synchronous test
// path bound to TestSubscription RPC.
//
// Note: the consumer-side loop lives on OutboxRunner; Dispatcher's
// only HTTP I/O today is DeliverOne. Keeping the deliver / deliverHTTP
// methods on Dispatcher means OutboxRunner can hold one and call into
// it without duplicating the sink branching.
type Dispatcher struct {
	Store      SubscriptionStore
	Outbox     OutboxWriter // nil in TestSubscription-only deployments
	HTTPClient *http.Client
	Logger     *zap.Logger
	// NATS is the optional shared connection pool used by the NATS
	// sink. nil = no NATS subs configured (deliver() will reject the
	// row with a clear error if one shows up). Owned by the
	// dispatcher pod's main; closed at shutdown.
	NATS *NatsConnPool
	// MaxAttempts caps retry per subscription on the synchronous
	// DeliverOne path. <=0 → 3. The outbox loop's retry budget is
	// driven by OutboxRunner.DefaultMaxAttempts instead.
	MaxAttempts int
	// BaseBackoff is the per-attempt backoff seed for DeliverOne.
	BaseBackoff time.Duration
}

// Dispatch enumerates every enabled subscription for tenantID whose
// filter matches `evt` and writes one row per match into the outbox.
// Returns the count of inserted rows. NO HTTP I/O happens here — the
// dispatcher pod consumes the outbox.
//
// Backwards-incompatible from the pre-slice-9 contract: the return
// value used to be "successful HTTP deliveries"; it is now "rows
// queued for the dispatcher pod". Call sites that logged the count
// for telemetry should keep doing so — the semantic shift is from
// "delivered" to "queued".
func (d *Dispatcher) Dispatch(ctx context.Context, tenantID string, evt Event) (int, error) {
	if d.Outbox == nil {
		return 0, errors.New("dispatcher: no outbox writer")
	}
	return d.dispatch(ctx, tenantID, evt, d.Outbox.Insert)
}

// DispatchTx is the transactional variant: it writes the outbox rows on
// the caller's transaction `tx` instead of the pool, so the fan-out is
// atomic with whatever state change the caller is committing (ADR-0003 —
// closes the dual-write crash window). The caller owns the tx lifecycle
// (begin/commit/rollback); DispatchTx only INSERTs.
func (d *Dispatcher) DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt Event) (int, error) {
	return d.dispatch(ctx, tenantID, evt, func(ctx context.Context, row OutboxRow) error {
		return insertOutboxRow(ctx, tx, row)
	})
}

// dispatch is the shared fan-out: resolve subscriptions, filter-match,
// and call `insert` once per match. `insert` is either the pool-backed
// OutboxWriter.Insert (Dispatch) or a tx-bound insert (DispatchTx).
func (d *Dispatcher) dispatch(ctx context.Context, tenantID string, evt Event, insert func(context.Context, OutboxRow) error) (int, error) {
	if d.Store == nil {
		return 0, errors.New("dispatcher: no subscription store")
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return 0, fmt.Errorf("dispatch: invalid tenant id %q: %w", tenantID, err)
	}
	subs, _, err := d.Store.List(ctx, admindomain.ListEventSubscriptionsArgs{
		TenantID: tenantUUID,
		PageSize: 1000,
	})
	if err != nil {
		return 0, fmt.Errorf("list subscriptions: %w", err)
	}
	if len(subs) == 1000 {
		// Single-page fan-out: a tenant at the cap means later
		// subscriptions silently miss events. Surface it loudly.
		d.log().Warn("subscription fan-out hit the single-page cap; events may be dropped for this tenant",
			zap.String("tenant_id", tenantID),
		)
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return 0, fmt.Errorf("marshal event: %w", err)
	}
	queued := 0
	for _, sub := range subs {
		if sub.Disabled {
			continue
		}
		if sub.CELFilter != "" && sub.CELFilter != evt.Type {
			continue
		}
		row := OutboxRow{
			ID:             uuid.New(),
			TenantID:       sub.TenantID,
			SubscriptionID: sub.SubscriptionID,
			EventType:      evt.Type,
			EventAt:        evt.At,
			EventPayload:   payload,
		}
		if err := insert(ctx, row); err != nil {
			d.log().Warn("failed to insert outbox row",
				zap.String("subscription_id", sub.SubscriptionID.String()),
				zap.String("event_type", evt.Type),
				zap.Error(err),
			)
			continue
		}
		queued++
	}
	return queued, nil
}

// DeliverOne delivers a synthetic test event to a single subscription.
// Used by EventSubscriptionService.TestSubscription. Bypasses store
// lookup, filter evaluation, AND the outbox — the operator clicked
// "Test Webhook" and wants the connectivity / signature result now.
func (d *Dispatcher) DeliverOne(ctx context.Context, sub admindomain.EventSubscription, eventType string) error {
	_, err := d.deliver(ctx, sub, Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     sub.TenantID.String(),
		ResourceName: fmt.Sprintf("tenants/%s/eventSubscriptions/%s", sub.TenantID, sub.SubscriptionID),
		Payload: map[string]any{
			"synthetic": true,
		},
	})
	return err
}

// deliver is the shared sink-branching path. Used by DeliverOne (sync)
// and by OutboxRunner. Returns (statusCode, err): statusCode is the
// HTTP response code for the http sink and 0 for non-HTTP sinks /
// transport errors. The runner persists statusCode on the row so the
// admin UI can render the per-sink result uniformly.
func (d *Dispatcher) deliver(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	switch sub.SinkKind {
	case "http":
		return d.deliverHTTPWithStatus(ctx, sub, evt)
	case "nats":
		return d.deliverNATS(ctx, sub, evt)
	case "kafka", "sqs":
		return 0, fmt.Errorf("sink %q delivery not yet wired", sub.SinkKind)
	default:
		return 0, fmt.Errorf("unknown sink kind %q", sub.SinkKind)
	}
}

// deliverHTTP signs and POSTs `evt` to the sub's HTTP sink. Per-attempt
// retries here are intentionally narrow (sub.MaxAttempts) — they cover
// transient flakes within a single delivery attempt. The outbox loop's
// row-level retry budget is the durable retry path; this loop just
// gives flaky-network requests a small in-process re-try window before
// the row is marked for backoff.
func (d *Dispatcher) deliverHTTP(ctx context.Context, sub admindomain.EventSubscription, evt Event) error {
	_, err := d.deliverHTTPWithStatus(ctx, sub, evt)
	return err
}

// deliverHTTPWithStatus is the same as deliverHTTP but also returns
// the HTTP status code of the last attempt (0 if no response was
// received — i.e. transport error). The outbox loop persists this
// for operator visibility.
func (d *Dispatcher) deliverHTTPWithStatus(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	var sink struct {
		URL              string `json:"url"`
		SigningSecretRef string `json:"signing_secret_ref"`
		MaxAttempts      int32  `json:"max_attempts"`
	}
	if err := json.Unmarshal(sub.SinkConfig, &sink); err != nil {
		return 0, fmt.Errorf("decode sink config: %w", err)
	}
	if sink.URL == "" {
		return 0, errors.New("http sink missing url")
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return 0, fmt.Errorf("marshal event: %w", err)
	}
	maxAttempts := int(sink.MaxAttempts)
	if maxAttempts <= 0 {
		maxAttempts = d.MaxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	backoff := d.BaseBackoff
	if backoff <= 0 {
		backoff = 250 * time.Millisecond
	}

	httpClient := d.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	var (
		lastErr    error
		lastStatus int
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, sink.URL, bytes.NewReader(body))
		if err != nil {
			return 0, fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PALADIN-Event-Type", evt.Type)
		req.Header.Set("X-PALADIN-Subscription-Id", sub.SubscriptionID.String())
		// Slice 8: resolve sink.SigningSecretRef from the secret manager
		// and sign the body. Inline-secret-by-value is reserved for
		// tests and SHOULD NOT be used in production.
		if sink.SigningSecretRef != "" {
			sig := signHMAC(body, sink.SigningSecretRef)
			req.Header.Set("X-PALADIN-Signature", "sha256="+sig)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			lastStatus = 0
		} else {
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return resp.StatusCode, nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		}
		if attempt < maxAttempts {
			// Stoppable timer, not time.After: with MaxBackoff up to
			// 1h, a ctx cancellation mid-backoff must release the timer
			// immediately instead of stranding it until it fires.
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return lastStatus, ctx.Err()
			}
			backoff *= 2
		}
	}
	return lastStatus, fmt.Errorf("delivery failed after %d attempts: %w", maxAttempts, lastErr)
}

// signHMAC produces a hex-encoded HMAC-SHA256 of body using key.
func signHMAC(body []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (d *Dispatcher) log() *zap.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return zap.NewNop()
}

// ─── OutboxWriter (production impl) ─────────────────────────────────────────

// PgxOutboxWriter is the production OutboxWriter — INSERTs straight
// into event_deliveries on the supplied pgxpool.Pool. The producer
// (admin pod) connection has the tenant GUC set per-request via the
// RLS BeforeAcquire hook; the policy WITH CHECK clause keeps the
// INSERT honest even if a future regression bypasses tenant context.
type PgxOutboxWriter struct {
	Pool *pgxpool.Pool
}

func (w PgxOutboxWriter) Insert(ctx context.Context, row OutboxRow) error {
	if w.Pool == nil {
		return errors.New("outbox writer: nil pool")
	}
	return insertOutboxRow(ctx, w.Pool, row)
}

// outboxExecer is the Exec subset shared by *pgxpool.Pool and pgx.Tx, so
// one INSERT helper serves both the pool-backed writer and the
// transactional DispatchTx path.
type outboxExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const insertOutboxSQL = `
	INSERT INTO event_deliveries (
		id, tenant_id, subscription_id, event_type, event_at, event_payload
	) VALUES ($1, $2, $3, $4, $5, $6)
`

func insertOutboxRow(ctx context.Context, exec outboxExecer, row OutboxRow) error {
	_, err := exec.Exec(ctx, insertOutboxSQL,
		row.ID, row.TenantID, row.SubscriptionID, row.EventType, row.EventAt, row.EventPayload,
	)
	return err
}

// ─── OutboxRunner (consumer-side loop) ──────────────────────────────────────

// OutboxRunner is the long-running loop that the dispatcher pod boots.
// One pod can have multiple replicas — FOR UPDATE SKIP LOCKED gives
// each row to exactly one replica per poll cycle.
type OutboxRunner struct {
	Pool       *pgxpool.Pool
	Dispatcher *Dispatcher // for sink delivery (re-uses deliver/deliverHTTP)
	Logger     *zap.Logger

	PollInterval       time.Duration // default 1s
	BatchSize          int           // default 50
	BaseBackoff        time.Duration // default 5s
	MaxBackoff         time.Duration // default 1h
	DefaultMaxAttempts int           // default 5; used when sub HttpSink.MaxAttempts==0
}

func (r *OutboxRunner) log() *zap.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return zap.NewNop()
}

// Run blocks until ctx is cancelled. On each tick it pulls up to
// BatchSize ready rows under FOR UPDATE SKIP LOCKED and delivers them.
// Returns nil on graceful shutdown (ctx.Err() after a clean drain).
func (r *OutboxRunner) Run(ctx context.Context) error {
	poll := r.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	for {
		processed, err := r.tick(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.log().Warn("outbox tick failed", zap.Error(err))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if processed == 0 {
			timer := time.NewTimer(poll)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
}

// Tick is a single batch claim+process pass. Public surface for
// integration tests that need deterministic stepping (no goroutine
// timing). Production callers use Run; the loop and the sleep
// scheduling live there. Returns the same (processed, err) tuple
// the internal tick produces.
func (r *OutboxRunner) Tick(ctx context.Context) (int, error) {
	return r.tick(ctx)
}

// tick claims and processes one batch. Returns the number of rows
// processed (delivered or marked-failed) so the caller can skip the
// idle-sleep when there's still backlog.
func (r *OutboxRunner) tick(ctx context.Context) (int, error) {
	batch := r.BatchSize
	if batch <= 0 {
		batch = 50
	}
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	const q = `
		SELECT id, tenant_id, subscription_id, event_type, event_at, event_payload, attempts
		  FROM event_deliveries
		 WHERE status = 'pending'
		   AND next_attempt_at <= now()
		 ORDER BY next_attempt_at
		 FOR UPDATE SKIP LOCKED
		 LIMIT $1
	`
	rows, err := tx.Query(ctx, q, batch)
	if err != nil {
		return 0, fmt.Errorf("scan ready: %w", err)
	}
	type pending struct {
		id, tenantID, subID uuid.UUID
		eventType           string
		eventAt             time.Time
		payload             []byte
		attempts            int
	}
	var batchRows []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.tenantID, &p.subID, &p.eventType, &p.eventAt, &p.payload, &p.attempts); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan row: %w", err)
		}
		batchRows = append(batchRows, p)
	}
	rows.Close()
	if len(batchRows) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit empty: %w", err)
		}
		committed = true
		return 0, nil
	}

	// Process each row inside the same tx. The row locks live until the
	// commit at the end — the loop's wallclock budget is bounded by
	// per-delivery HTTP timeouts.
	for _, p := range batchRows {
		evt := Event{}
		if err := json.Unmarshal(p.payload, &evt); err != nil {
			r.markFailed(ctx, tx, p.id, p.attempts, 0, fmt.Sprintf("decode payload: %v", err), true)
			continue
		}

		sub, err := r.Dispatcher.Store.Get(ctx, p.subID)
		if err != nil {
			// Subscription deleted while the row sat in the queue.
			// Permanent fail — don't retry a row we can never deliver.
			r.markFailed(ctx, tx, p.id, p.attempts, 0, "subscription deleted", true)
			continue
		}
		if sub.Disabled {
			// Operator turned the sub off after the row was queued.
			// Same shape as deletion: permanent fail rather than
			// "pending forever".
			r.markFailed(ctx, tx, p.id, p.attempts, 0, "subscription disabled", true)
			continue
		}

		status, deliverErr := r.Dispatcher.deliver(ctx, sub, evt)
		if deliverErr == nil {
			if _, err := tx.Exec(ctx,
				`UPDATE event_deliveries
				    SET status='delivered',
				        attempts=attempts+1,
				        last_attempt_at=now(),
				        last_status_code=$2,
				        delivered_at=now()
				  WHERE id=$1`,
				p.id, status,
			); err != nil {
				return 0, fmt.Errorf("mark delivered: %w", err)
			}
			continue
		}

		// Failure path: bump attempts, schedule next attempt, or mark
		// terminal failure when the sub's max-attempts budget is spent.
		max := r.maxAttemptsFor(sub)
		permanent := p.attempts+1 >= max
		r.markFailed(ctx, tx, p.id, p.attempts, status, deliverErr.Error(), permanent)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit batch: %w", err)
	}
	committed = true
	return len(batchRows), nil
}

// markFailed bumps attempts, records the error, and either schedules
// the next attempt (status='pending', next_attempt_at=now+backoff) or
// flips to status='failed' when permanent==true.
func (r *OutboxRunner) markFailed(ctx context.Context, tx pgx.Tx, id uuid.UUID, attempts, statusCode int, errMsg string, permanent bool) {
	nextStatus := "pending"
	var nextAt sql.NullTime
	if permanent {
		nextStatus = "failed"
	} else {
		nextAt.Time = time.Now().Add(r.backoffFor(attempts + 1))
		nextAt.Valid = true
	}
	const q = `
		UPDATE event_deliveries
		   SET status          = $2,
		       attempts        = attempts + 1,
		       last_error      = $3,
		       last_status_code= $4,
		       last_attempt_at = now(),
		       next_attempt_at = COALESCE($5, next_attempt_at)
		 WHERE id = $1
	`
	if _, err := tx.Exec(ctx, q, id, nextStatus, errMsg, statusCode, nextAt); err != nil {
		r.log().Warn("failed to mark delivery row",
			zap.String("id", id.String()),
			zap.Error(err),
		)
	}
}

// backoffFor returns the delay before the n-th attempt: BaseBackoff *
// 2^(n-1), capped at MaxBackoff.
func (r *OutboxRunner) backoffFor(attempt int) time.Duration {
	base := r.BaseBackoff
	if base <= 0 {
		base = 5 * time.Second
	}
	max := r.MaxBackoff
	if max <= 0 {
		max = time.Hour
	}
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	if d > max {
		d = max
	}
	return d
}

// maxAttemptsFor reads HttpSink.MaxAttempts for an HTTP sub; falls
// back to DefaultMaxAttempts when 0 / non-HTTP.
func (r *OutboxRunner) maxAttemptsFor(sub admindomain.EventSubscription) int {
	def := r.DefaultMaxAttempts
	if def <= 0 {
		def = 5
	}
	if sub.SinkKind != "http" {
		return def
	}
	var sink struct {
		MaxAttempts int32 `json:"max_attempts"`
	}
	if err := json.Unmarshal(sub.SinkConfig, &sink); err != nil {
		return def
	}
	if sink.MaxAttempts <= 0 {
		return def
	}
	return int(sink.MaxAttempts)
}

// PendingCount reports the number of rows in status='pending'. Used
// by the dispatcher pod's /system/health.json subsystem check — a
// growing backlog tells the operator the loop is wedged or its sinks
// are unreachable.
func (r *OutboxRunner) PendingCount(ctx context.Context) (int64, error) {
	var n int64
	row := r.Pool.QueryRow(ctx, `SELECT count(*) FROM event_deliveries WHERE status='pending'`)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

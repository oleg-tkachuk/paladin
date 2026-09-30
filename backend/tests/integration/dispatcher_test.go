//go:build integration

// End-to-end coverage for the durable webhook outbox: producer-side
// Dispatcher.Dispatch fans out into event_deliveries; consumer-side
// OutboxRunner picks up rows under FOR UPDATE SKIP LOCKED and delivers
// them via signed HTTP POST. Both sides exercise a real Postgres via
// pgharness so the schema, RLS GRANTs, and JSON shapes are wired
// against the actual migrations rather than a hand-rolled fake.
//
// HTTP sinks point at httptest.NewServer recorders so tests can assert
// request count, body, and headers without standing up a real
// receiver.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// recordedReq captures every request that hit the recorder server so
// tests can assert on count, body shape, headers, etc.
type recordedReq struct {
	Method string
	URL    string
	Body   []byte
	Header http.Header
}

// recorder is a configurable httptest.NewServer that records every
// request and lets tests script the response sequence.
type recorder struct {
	mu       sync.Mutex
	requests []recordedReq
	// statuses is the queue of status codes returned in order. When
	// exhausted the recorder falls back to defaultStatus.
	statuses       []int
	defaultStatus  int
	srv            *httptest.Server
	requestCounter atomic.Int64
}

func newRecorder(defaultStatus int, scripted ...int) *recorder {
	r := &recorder{statuses: scripted, defaultStatus: defaultStatus}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		r.mu.Lock()
		r.requests = append(r.requests, recordedReq{
			Method: req.Method,
			URL:    req.URL.String(),
			Body:   body,
			Header: req.Header.Clone(),
		})
		var status int
		if len(r.statuses) > 0 {
			status = r.statuses[0]
			r.statuses = r.statuses[1:]
		} else {
			status = r.defaultStatus
		}
		r.mu.Unlock()
		r.requestCounter.Add(1)
		w.WriteHeader(status)
	}))
	return r
}

func (r *recorder) snapshot() []recordedReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedReq, len(r.requests))
	copy(out, r.requests)
	return out
}

func (r *recorder) count() int { return int(r.requestCounter.Load()) }

func (r *recorder) Close() { r.srv.Close() }

// ─── fixture ────────────────────────────────────────────────────────────────

type dispatcherFixture struct {
	h *pgharness.Harness
}

func setupDispatcher(t *testing.T) *dispatcherFixture {
	t.Helper()
	h := pgharness.Setup(t)
	return &dispatcherFixture{h: h}
}

type subOpts struct {
	URL      string
	Filter   string
	Disabled bool
	SinkKind string // default "http"
}

func (f *dispatcherFixture) seedSubscription(t *testing.T, tenant uuid.UUID, opts subOpts) uuid.UUID {
	t.Helper()
	if opts.SinkKind == "" {
		opts.SinkKind = "http"
	}
	// max_attempts deliberately omitted: the inner per-tick HTTP retry
	// loop falls back to Dispatcher.MaxAttempts (=1 in tests), so each
	// tick fires exactly one POST. Per-row retry budget is controlled
	// by OutboxRunner.DefaultMaxAttempts instead — set per-test via
	// outboxRunnerWithMax.
	cfg, _ := json.Marshal(map[string]any{
		"url":                opts.URL,
		"signing_secret_ref": "test-secret",
	})
	subID := uuid.New()
	// Insert directly — the production repo's List query has a
	// pagination quirk that excludes everything when AfterID is the
	// zero UUID (the cursor maps to NULL via pgUUID, and `> NULL` is
	// never true). Tests don't need pagination; they just need rows
	// to land in the table so the dispatcher's List can read them.
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_subscriptions
		   (id, tenant_id, cel_filter, sink_kind, sink_config, disabled)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		subID, tenant, opts.Filter, opts.SinkKind, cfg, opts.Disabled,
	); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}
	return subID
}

// dispatcherFor builds a producer-side Dispatcher. The HTTPClient is
// only used by DeliverOne (which we don't call here); the outbox loop
// gets its own client via OutboxRunner -> Dispatcher.
func (f *dispatcherFixture) dispatcher() *worker.Dispatcher {
	return &worker.Dispatcher{
		Store:       directSubStore{pool: f.h.PoolMigrate},
		Outbox:      worker.PgxOutboxWriter{Pool: f.h.PoolMigrate},
		HTTPClient:  &http.Client{Timeout: 2 * time.Second},
		MaxAttempts: 1, // outbox loop owns retry; the sync per-attempt loop is a single shot
		BaseBackoff: 10 * time.Millisecond,
	}
}

// outboxRunner constructs a runner with deterministic-friendly values:
// short backoff so retry tests don't sleep for whole seconds, batch
// large enough that "claim everything" tests succeed in one tick.
func (f *dispatcherFixture) outboxRunner(d *worker.Dispatcher) *worker.OutboxRunner {
	return f.outboxRunnerWithMax(d, 5)
}

// outboxRunnerWithMax lets the per-row retry budget be overridden for
// tests that want to drive the row to permanent-failed in fewer ticks.
func (f *dispatcherFixture) outboxRunnerWithMax(d *worker.Dispatcher, maxAttempts int) *worker.OutboxRunner {
	return &worker.OutboxRunner{
		Pool:               f.h.PoolMigrate,
		Dispatcher:         d,
		PollInterval:       50 * time.Millisecond,
		BatchSize:          100,
		BaseBackoff:        50 * time.Millisecond,
		MaxBackoff:         time.Second,
		DefaultMaxAttempts: maxAttempts,
	}
}

func (f *dispatcherFixture) tickOnce(t *testing.T, r *worker.OutboxRunner) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := r.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return n
}

// fastForward sets next_attempt_at = now() so the next Tick picks the
// row up immediately (skipping the wallclock backoff window).
func (f *dispatcherFixture) fastForward(t *testing.T, deliveryID uuid.UUID) {
	t.Helper()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`UPDATE event_deliveries SET next_attempt_at = now() WHERE id = $1`, deliveryID); err != nil {
		t.Fatalf("fast-forward: %v", err)
	}
}

type deliveryRow struct {
	ID             uuid.UUID
	Status         string
	Attempts       int
	LastError      string
	LastStatusCode int
	NextAttemptAt  time.Time
	DeliveredAt    *time.Time
}

func (f *dispatcherFixture) deliveryRow(t *testing.T, id uuid.UUID) deliveryRow {
	t.Helper()
	var r deliveryRow
	r.ID = id
	if err := f.h.PoolMigrate.QueryRow(context.Background(),
		`SELECT status, attempts, COALESCE(last_error, ''),
		        COALESCE(last_status_code, 0), next_attempt_at, delivered_at
		   FROM event_deliveries WHERE id = $1`, id,
	).Scan(&r.Status, &r.Attempts, &r.LastError, &r.LastStatusCode, &r.NextAttemptAt, &r.DeliveredAt); err != nil {
		t.Fatalf("scan delivery row: %v", err)
	}
	return r
}

// allDeliveryRows lists every row for a tenant (test helper for the
// fan-out / orphan tests where the test doesn't track row IDs).
func (f *dispatcherFixture) allDeliveryRows(t *testing.T, tenant uuid.UUID) []deliveryRow {
	t.Helper()
	rows, err := f.h.PoolMigrate.Query(context.Background(),
		`SELECT id, status, attempts, COALESCE(last_error, ''),
		        COALESCE(last_status_code, 0), next_attempt_at, delivered_at
		   FROM event_deliveries WHERE tenant_id = $1 ORDER BY created_at`, tenant)
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()
	var out []deliveryRow
	for rows.Next() {
		var r deliveryRow
		if err := rows.Scan(&r.ID, &r.Status, &r.Attempts, &r.LastError, &r.LastStatusCode, &r.NextAttemptAt, &r.DeliveredAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// directSubStore is a tiny worker.SubscriptionStore impl that hits
// the table directly (no pagination, no soft-delete) — the production
// repo has a pagination cursor quirk that's irrelevant to these
// tests, and bypassing it keeps the test focused on the
// dispatcher/outbox contract rather than the repo wiring.
type directSubStore struct {
	pool *pgxpool.Pool
}

func (s directSubStore) List(ctx context.Context, _ admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, cel_filter, sink_kind, sink_config, disabled, resource_version
		   FROM event_subscriptions`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []admindomain.EventSubscription
	for rows.Next() {
		var sub admindomain.EventSubscription
		if err := rows.Scan(&sub.SubscriptionID, &sub.TenantID, &sub.CELFilter,
			&sub.SinkKind, &sub.SinkConfig, &sub.Disabled, &sub.ResourceVersion); err != nil {
			return nil, "", err
		}
		out = append(out, sub)
	}
	return out, "", rows.Err()
}

func (s directSubStore) Get(ctx context.Context, id uuid.UUID) (admindomain.EventSubscription, error) {
	var sub admindomain.EventSubscription
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, cel_filter, sink_kind, sink_config, disabled, resource_version
		   FROM event_subscriptions WHERE id = $1`, id,
	).Scan(&sub.SubscriptionID, &sub.TenantID, &sub.CELFilter,
		&sub.SinkKind, &sub.SinkConfig, &sub.Disabled, &sub.ResourceVersion)
	if err != nil {
		return admindomain.EventSubscription{}, err
	}
	return sub, nil
}

// makeEvent is the standard test event; type defaults to "paladin.object.uploaded".
func makeEvent(eventType string, tenantID uuid.UUID) worker.Event {
	if eventType == "" {
		eventType = "paladin.object.uploaded"
	}
	return worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: "tenants/" + tenantID.String() + "/objects/test",
		Payload:      map[string]any{"hello": "world"},
	}
}

// ─── tests ──────────────────────────────────────────────────────────────────

// TestDispatcher_OutboxToHTTPDelivery_HappyPath: producer writes one
// outbox row, runner picks it up and POSTs it to the recorder server,
// row flips to delivered with attempts=1 and a 200 status code.
func TestDispatcher_OutboxToHTTPDelivery_HappyPath(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-happy")
	subID := f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	d := f.dispatcher()
	queued, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}

	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 || rows[0].Status != "pending" {
		t.Fatalf("rows = %#v, want one pending", rows)
	}

	r := f.outboxRunner(d)
	if n := f.tickOnce(t, r); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}

	if got := rec.count(); got != 1 {
		t.Errorf("recorder requests = %d, want 1", got)
	}

	row := f.deliveryRow(t, rows[0].ID)
	if row.Status != "delivered" {
		t.Errorf("status = %q, want delivered", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.Attempts)
	}
	if row.LastStatusCode != 200 {
		t.Errorf("last_status_code = %d, want 200", row.LastStatusCode)
	}
	if row.LastError != "" {
		t.Errorf("last_error = %q, want empty", row.LastError)
	}
	if row.DeliveredAt == nil {
		t.Errorf("delivered_at unset")
	}

	// Body shape: contains the event type and tenant.
	reqs := rec.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("recorded req count = %d", len(reqs))
	}
	// Since the CloudEvents default flip (#96) an HTTP sink with an unset
	// format receives the CloudEvents 1.0 envelope, not the raw Event.
	var got struct {
		SpecVersion string `json:"specversion"`
		Type        string `json:"type"`
		TenantID    string `json:"tenantid"`
	}
	if err := json.Unmarshal(reqs[0].Body, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.SpecVersion != "1.0" || got.Type != "paladin.object.uploaded" || got.TenantID != tenant.String() {
		t.Errorf("event envelope = %+v", got)
	}
	if reqs[0].Header.Get("X-Paladin-Subscription-Id") != subID.String() {
		t.Errorf("missing/wrong subscription id header: %v", reqs[0].Header)
	}
	if reqs[0].Header.Get("X-Paladin-Signature") == "" {
		t.Errorf("missing signature header")
	}
}

// TestDispatcher_RetryWithBackoff_5xx: 500 first, 200 second. After
// the first tick the row is pending with attempts=1; fast-forward
// next_attempt_at and tick again, row delivered with attempts=2.
func TestDispatcher_RetryWithBackoff_5xx(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	// First request 500, second 200.
	rec := newRecorder(http.StatusOK, http.StatusInternalServerError)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-retry")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	d := f.dispatcher()
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	rowID := rows[0].ID

	r := f.outboxRunner(d)
	startedAt := time.Now()
	f.tickOnce(t, r)

	row := f.deliveryRow(t, rowID)
	if row.Status != "pending" {
		t.Fatalf("after 1st tick status = %q, want pending", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.Attempts)
	}
	if row.LastStatusCode != 500 {
		t.Errorf("last_status_code = %d, want 500", row.LastStatusCode)
	}
	if row.LastError == "" {
		t.Errorf("last_error empty, want something containing 500")
	}
	// next_attempt_at must have been pushed forward at least to a value
	// that's > started_at (the runner sets now()+backoff).
	if !row.NextAttemptAt.After(startedAt) {
		t.Errorf("next_attempt_at = %v, want > %v", row.NextAttemptAt, startedAt)
	}

	// Fast-forward and tick again.
	f.fastForward(t, rowID)
	f.tickOnce(t, r)

	row = f.deliveryRow(t, rowID)
	if row.Status != "delivered" {
		t.Errorf("after 2nd tick status = %q, want delivered", row.Status)
	}
	if row.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", row.Attempts)
	}
	if rec.count() != 2 {
		t.Errorf("recorder requests = %d, want 2", rec.count())
	}
}

// TestDispatcher_MaxAttemptsTransitionsToFailed: every delivery
// returns 500. With MaxAttempts=2 the row flips to failed after the
// second attempt and no further deliveries fire.
func TestDispatcher_MaxAttemptsTransitionsToFailed(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusInternalServerError)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-fail")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	d := f.dispatcher()
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	rows := f.allDeliveryRows(t, tenant)
	rowID := rows[0].ID

	r := f.outboxRunnerWithMax(d, 2)

	// Attempt 1 → failure, still pending (1 < 2).
	f.tickOnce(t, r)
	if got := f.deliveryRow(t, rowID); got.Status != "pending" || got.Attempts != 1 {
		t.Fatalf("after 1st tick: status=%q attempts=%d, want pending/1", got.Status, got.Attempts)
	}

	// Attempt 2 → failure, hits cap, flips to failed.
	f.fastForward(t, rowID)
	prev := f.deliveryRow(t, rowID).NextAttemptAt
	f.tickOnce(t, r)

	row := f.deliveryRow(t, rowID)
	if row.Status != "failed" {
		t.Errorf("status = %q, want failed", row.Status)
	}
	if row.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", row.Attempts)
	}
	// next_attempt_at should not have been advanced when permanent (the
	// runner passes a NULL sql.NullTime so the COALESCE keeps the old
	// value). Use ~ms tolerance for tz round-trip.
	if !row.NextAttemptAt.Equal(prev) {
		t.Errorf("next_attempt_at advanced on permanent fail: prev=%v new=%v", prev, row.NextAttemptAt)
	}
	if rec.count() != 2 {
		t.Errorf("recorder requests = %d, want 2", rec.count())
	}

	// Another tick should be a no-op — failed rows aren't picked up.
	if n := f.tickOnce(t, r); n != 0 {
		t.Errorf("tick on failed row processed %d, want 0", n)
	}
	if rec.count() != 2 {
		t.Errorf("recorder requests after no-op tick = %d, want 2", rec.count())
	}
}

// TestDispatcher_FilterMatch_OnlyMatchingSubsGetRow: the producer honours
// sub.CELFilter, a CEL predicate over the event envelope (see
// cel.EventEnvelopeSchema; the event type is the `type` variable). An empty
// filter matches every event; a predicate that evaluates false, or fails to
// compile, is fail-closed and produces no row. Only the two subs whose filter
// matches should produce rows; the third is silently skipped.
//
// Was previously written for the pre-CEL "exact event-type match" semantics
// and seeded bare type strings as filters — which, as CEL, are string
// literals rather than boolean predicates and so fail-closed to zero matches.
// The suite ran nowhere (no task, no CI), so the drift went unnoticed until
// it was wired into CI. Rewritten to current CEL semantics.
func TestDispatcher_FilterMatch_OnlyMatchingSubsGetRow(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-filter")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL}) // empty filter → match anything
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL, Filter: `type == "paladin.object.uploaded"`})
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL, Filter: `type == "paladin.tenant.created"`})

	d := f.dispatcher()
	queued, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("paladin.object.uploaded", tenant))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if queued != 2 {
		t.Errorf("queued = %d, want 2", queued)
	}
	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	r := f.outboxRunner(d)
	if n := f.tickOnce(t, r); n != 2 {
		t.Errorf("processed = %d, want 2", n)
	}
	if rec.count() != 2 {
		t.Errorf("recorder requests = %d, want 2", rec.count())
	}
	for _, row := range f.allDeliveryRows(t, tenant) {
		if row.Status != "delivered" {
			t.Errorf("row %s status = %q, want delivered", row.ID, row.Status)
		}
	}
}

// TestDispatcher_DisabledSubsSkipped: disabled subs produce no outbox
// rows even when their filter matches.
func TestDispatcher_DisabledSubsSkipped(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-disabled")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL, Disabled: true})

	d := f.dispatcher()
	queued, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if queued != 0 {
		t.Errorf("queued = %d, want 0", queued)
	}
	if rows := f.allDeliveryRows(t, tenant); len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

// TestDispatcher_DeletedSubscription_TakesItsQueueWithIt: the guarantee
// that used to be the dispatcher's job is now the schema's.
// event_deliveries.subscription_id is a FK with ON DELETE CASCADE, so a row
// pointing at a subscription that does not exist cannot be written at all,
// and deleting a subscription removes whatever it still had queued.
//
// That is strictly stronger than the old behaviour (dispatcher notices the
// orphan on its next tick and marks it failed), and it is what this test
// asserts now: the FK rejects the orphan, and the cascade cleans up.
func TestDispatcher_DeletedSubscription_TakesItsQueueWithIt(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	ctx := context.Background()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-orphan")
	payload, _ := json.Marshal(makeEvent("", tenant))

	// 1. An orphan cannot be created.
	_, err := f.h.PoolMigrate.Exec(ctx,
		`INSERT INTO event_deliveries
		   (id, tenant_id, subscription_id, event_type, event_at, event_payload)
		 VALUES ($1, $2, $3, $4, now(), $5)`,
		uuid.New(), tenant, uuid.New(), "paladin.object.uploaded", payload,
	)
	if err == nil {
		t.Fatal("insert with a nonexistent subscription_id succeeded; the FK is missing")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" { // foreign_key_violation
		t.Fatalf("orphan insert error = %v, want foreign_key_violation", err)
	}

	// 2. Deleting a subscription takes its queued rows with it, so no row
	//    is ever left addressing a sink that is gone.
	subID := seedSubscriptionRow(t, f.h.PoolMigrate, tenant)
	if _, err := f.h.PoolMigrate.Exec(ctx,
		`INSERT INTO event_deliveries
		   (id, tenant_id, subscription_id, event_type, event_at, event_payload)
		 VALUES ($1, $2, $3, $4, now(), $5)`,
		uuid.New(), tenant, subID, "paladin.object.uploaded", payload,
	); err != nil {
		t.Fatalf("seed queued row: %v", err)
	}
	if _, err := f.h.PoolMigrate.Exec(ctx,
		`DELETE FROM event_subscriptions WHERE id = $1`, subID); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}
	var n int
	if err := f.h.PoolMigrate.QueryRow(ctx,
		`SELECT count(*) FROM event_deliveries WHERE subscription_id = $1`, subID,
	).Scan(&n); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if n != 0 {
		t.Errorf("deliveries left after subscription delete = %d, want 0", n)
	}
}

// TestDispatcher_ConcurrentRunnersUseSkipLocked: 50 pending rows fed
// to 3 concurrent runners. SKIP LOCKED must give every row to exactly
// one runner — the recorder server should see exactly 50 POSTs and
// every row must end as delivered with attempts=1.
func TestDispatcher_ConcurrentRunnersUseSkipLocked(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-concurrent")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	d := f.dispatcher()
	const total = 50
	for i := 0; i < total; i++ {
		if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
			t.Fatalf("dispatch %d: %v", i, err)
		}
	}
	if rows := f.allDeliveryRows(t, tenant); len(rows) != total {
		t.Fatalf("seeded rows = %d, want %d", len(rows), total)
	}

	const runners = 3
	var wg sync.WaitGroup
	start := time.Now()
	wg.Add(runners)
	for i := 0; i < runners; i++ {
		go func() {
			defer wg.Done()
			r := f.outboxRunner(d)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Loop ticks until there's nothing left, so any rows the
			// other runners didn't claim get picked up too. The
			// recorder counter doubles as our liveness check.
			for {
				n, err := r.Tick(ctx)
				if err != nil || n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("3 runners took %v to drain 50 rows, expected < 5s", elapsed)
	}

	if got := rec.count(); got != total {
		t.Errorf("recorder requests = %d, want %d (SKIP LOCKED double-deliver?)", got, total)
	}
	delivered := 0
	for _, row := range f.allDeliveryRows(t, tenant) {
		if row.Status == "delivered" && row.Attempts == 1 {
			delivered++
		} else {
			t.Errorf("row %s status=%q attempts=%d", row.ID, row.Status, row.Attempts)
		}
	}
	if delivered != total {
		t.Errorf("delivered rows = %d, want %d", delivered, total)
	}
}

// containsCI is a small substring helper so the orphan test's error
// match isn't case-sensitive.
func containsCI(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			a, b := haystack[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// seedNATSSubscription mirrors seedSubscription but writes a nats-sink
// row with the supplied URL + subject. Kept distinct so the HTTP path
// helper stays narrow (its sink_config is HTTP-shaped).
func (f *dispatcherFixture) seedNATSSubscription(t *testing.T, tenant uuid.UUID, url, subject string) uuid.UUID {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"url": url, "subject": subject})
	subID := uuid.New()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_subscriptions
		   (id, tenant_id, cel_filter, sink_kind, sink_config, disabled)
		 VALUES ($1, $2, '', 'nats', $3, false)`,
		subID, tenant, cfg,
	); err != nil {
		t.Fatalf("seed nats sub: %v", err)
	}
	return subID
}

// TestDispatcher_OutboxToNATSDelivery_HappyPath: producer writes one
// outbox row, runner picks it up and Publishes a CloudEvents envelope
// to the in-process NATS subject. Row flips to delivered with
// attempts=1 and last_status_code=0 (NATS publish is fire-and-forget).
func TestDispatcher_OutboxToNATSDelivery_HappyPath(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)

	url := runEmbeddedNATSForIntegration(t)
	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	defer pc.Close()
	got := make(chan *nats.Msg, 1)
	if _, err := pc.Subscribe("paladin.events.>", func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "disp-nats")
	subject := "paladin.events." + tenant.String() + ".object.uploaded"
	_ = f.seedNATSSubscription(t, tenant, url, subject)

	pool := worker.NewNatsConnPool(nil)
	defer pool.Close()
	d := f.dispatcher()
	d.NATS = pool

	queued, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}

	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 || rows[0].Status != "pending" {
		t.Fatalf("rows = %#v, want one pending", rows)
	}

	r := f.outboxRunner(d)
	if n := f.tickOnce(t, r); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}

	row := f.deliveryRow(t, rows[0].ID)
	if row.Status != "delivered" {
		t.Errorf("status = %q, want delivered (last_error=%q)", row.Status, row.LastError)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.Attempts)
	}
	if row.LastStatusCode != 0 {
		t.Errorf("last_status_code = %d, want 0 (nats has no http status)", row.LastStatusCode)
	}

	select {
	case m := <-got:
		if m.Subject != subject {
			t.Errorf("subject: got %q want %q", m.Subject, subject)
		}
		var env struct {
			SpecVersion string `json:"specversion"`
			Type        string `json:"type"`
			TenantID    string `json:"tenantid"`
		}
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("envelope: %v", err)
		}
		if env.SpecVersion != "1.0" || env.Type != "paladin.object.uploaded" || env.TenantID != tenant.String() {
			t.Errorf("envelope mismatch: %+v", env)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nats: no message within 2s")
	}
}

// runEmbeddedNATSForIntegration boots an in-process nats-server on a
// random port. Distinct name from the unit-test helper since
// integration tests live in a separate package and the helper is
// duplicated rather than exported (test packages don't share helpers).
func runEmbeddedNATSForIntegration(t *testing.T) string {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	srv := natstest.RunServer(&opts)
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	if !srv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("embedded nats: not ready")
	}
	return srv.ClientURL()
}

// ─── the subscription store's pool is load-bearing ──────────────────────────
//
// The OutboxRunner drains `event_deliveries` cross-tenant on the BYPASSRLS
// dispatcher pool, then per row calls Store.Get(subID) to read the sink
// config. That store was once wired to the RLS-scoped runtime pool, and the
// drain loop sets no tenant GUC — so `paladin_session_tenant_id()` was NULL,
// event_subscriptions' policy matched zero rows, and every delivery was marked
// "subscription deleted". The whole sink path was dead whenever any real
// subscription existed.
//
// Nothing caught it. Every other test in this file wires its store to
// PoolMigrate, which is BYPASSRLS and therefore cannot reproduce the
// condition, and the feature was off by default. It took an end-to-end
// delivery test to notice.
//
// Both cases use the REAL store — RepoSubscriptionStore over the sqlc adapter,
// the same construction serve_dispatcher.go makes — rather than this file's
// direct-SQL stand-in. The wiring IS the subject, so a stand-in would test the
// wrong object.

func repoSubStore(pool *pgxpool.Pool) worker.SubscriptionStore {
	return worker.NewRepoSubscriptionStore(
		adapters.NewEventSubscriptionRepoV2(sqlc.New(pool)))
}

func TestDispatcher_SubscriptionStoreOnBypassPool_Delivers(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "sub-store-bypass")
	_ = f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	// The production wiring: the store reads on the same BYPASSRLS pool the
	// runner drains with.
	d := f.dispatcher()
	d.Store = repoSubStore(f.h.PoolMigrate)

	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if n := f.tickOnce(t, f.outboxRunner(d)); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("recorder saw %d requests, want 1", got)
	}
	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 || rows[0].Status != "delivered" {
		t.Fatalf("delivery = %#v, want one delivered", rows)
	}
}

func TestDispatcher_SubscriptionStoreOnRLSPoolWithNoGUC_LosesEveryDelivery(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "sub-store-rls")
	_ = f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})

	// The two halves run in different processes and only one of them has a
	// tenant. ENQUEUE happens on an API request, which carries a principal, so
	// it sees the subscription and writes the delivery row.
	d := f.dispatcher()
	d.Store = repoSubStore(f.h.PoolMigrate)
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("enqueued %d rows, want 1 — the setup must produce a row to lose", len(rows))
	}

	// DRAIN happens in the dispatcher pod, which has no request and therefore
	// no tenant GUC. This is the regression: `paladin_app` with no hook, so
	// event_subscriptions' policy matches nothing and the row the enqueue side
	// just wrote can no longer find its own subscription.
	d.Store = repoSubStore(f.h.PoolAppNoGUC)
	if n := f.tickOnce(t, f.outboxRunner(d)); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}

	// The sink is never reached and the row is burned permanently. Nothing
	// errors and nothing retries, which is why this survived so long.
	if got := rec.count(); got != 0 {
		t.Errorf("recorder saw %d requests; the subscription should have been invisible", got)
	}
	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 {
		t.Fatalf("delivery rows = %d, want 1", len(rows))
	}
	if rows[0].Status != "failed" {
		t.Errorf("status = %q, want failed", rows[0].Status)
	}
	if rows[0].LastError != "subscription deleted" {
		t.Errorf("last_error = %q, want \"subscription deleted\" — the row reads as a "+
			"deleted subscription when the subscription is merely invisible, which is "+
			"what made this look like data loss rather than a wiring bug",
			rows[0].LastError)
	}
}

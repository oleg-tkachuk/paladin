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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
)

// Event is the wire payload delivered to subscribers. The shape is JSON-
// stable across releases; new fields go to the end.
type Event struct {
	Type         string         `json:"type"` // canonical "paladin.<kind>.<verb>", e.g. "paladin.object.uploaded"
	At           time.Time      `json:"at"`
	TenantID     string         `json:"tenant_id"`
	ResourceName string         `json:"resource_name"`
	ActorSubject string         `json:"actor_subject,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
	// ID is the delivery-time unique identifier (the event_deliveries row
	// id), stamped by the drain loop just before a sink delivers. It is NOT
	// part of the stored producer payload (json:"-"); sinks that need a
	// CloudEvents-unique `id` read it here. Stable across retries of the
	// same row, so downstream consumers can dedup.
	ID string `json:"-"`
}

// SubscriptionStore is the read seam the producer uses to look up subs
// at fan-out time, and the dispatcher pod uses to load a sub by ID at
// delivery time. Defined narrowly so tests can stub.
type SubscriptionStore interface {
	List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error)
	Get(ctx context.Context, id uuid.UUID) (admindomain.EventSubscription, error)
}

// RepoSubscriptionStore adapts an EventSubscriptionRepository to the
// SubscriptionStore read seam (List + Get). Shared by every producer plane
// (api / admin / ingest) so the fan-out reads subscriptions the same way
// regardless of which pool/queries the repo is bound to.
type RepoSubscriptionStore struct {
	Repo admindomain.EventSubscriptionRepository
}

// NewRepoSubscriptionStore wraps repo as a SubscriptionStore.
func NewRepoSubscriptionStore(repo admindomain.EventSubscriptionRepository) RepoSubscriptionStore {
	return RepoSubscriptionStore{Repo: repo}
}

func (s RepoSubscriptionStore) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return s.Repo.List(ctx, args)
}

func (s RepoSubscriptionStore) Get(ctx context.Context, id uuid.UUID) (admindomain.EventSubscription, error) {
	return s.Repo.Get(ctx, id)
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
	// SQS is the optional per-region SQS client pool used by the SQS sink.
	// nil = no SQS subs configured (deliver() rejects the row with a clear
	// error if one shows up). Owned by the dispatcher pod's main.
	SQS *SQSClientPool
	// RabbitMQ is the optional connection pool used by the RabbitMQ sink.
	// nil = no rabbitmq subs configured. Owned by the dispatcher pod's main;
	// closed at shutdown.
	RabbitMQ *RabbitMQConnPool
	// Kafka is the optional writer pool used by the Kafka sink. nil = no
	// kafka subs configured. Owned by the dispatcher pod's main; closed at
	// shutdown.
	Kafka *KafkaWriterPool
	// MaxAttempts caps retry per subscription on the synchronous
	// DeliverOne path. <=0 → 3. The outbox loop's retry budget is
	// driven by OutboxRunner.DefaultMaxAttempts instead.
	MaxAttempts int
	// BaseBackoff is the per-attempt backoff seed for DeliverOne.
	BaseBackoff time.Duration

	// Secrets resolves "k8s:<name>/<key>" refs in sink-credential fields
	// (see sink_secrets.go). nil = inline-only configs; a ref with no
	// resolver fails the delivery loudly rather than using the literal.
	Secrets         SinkSecretResolver
	secretCache     *sinkSecretCache
	secretCacheOnce sync.Once

	// Filter evaluates a subscription's CEL filter against the event
	// envelope (cel.EventEnvelopeSchema). Optional — nil falls back to a
	// process-shared evaluator, so struct-literal construction keeps working.
	Filter *cel.Evaluator
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

// subscriptionFanoutPageSize pages the per-event subscription fan-out. Modest
// so each round-trip is cheap; the loop covers every subscription, so there is
// no silent cap regardless of how many a tenant has.
const subscriptionFanoutPageSize = 500

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
	// Page through EVERY matching subscription. A single-page cap would
	// silently drop events for tenants with more subscriptions than the page
	// size — a real hazard now that audit_mirror fans every mutation out.
	var subs []admindomain.EventSubscription
	var afterID uuid.UUID
	for {
		page, next, lErr := d.Store.List(ctx, admindomain.ListEventSubscriptionsArgs{
			TenantID: tenantUUID,
			PageSize: subscriptionFanoutPageSize,
			AfterID:  afterID,
		})
		if lErr != nil {
			return 0, fmt.Errorf("list subscriptions: %w", lErr)
		}
		subs = append(subs, page...)
		if next == "" {
			break // exhausted (a short/empty page never carries a cursor)
		}
		parsed, pErr := uuid.Parse(next)
		if pErr != nil {
			// A non-UUID cursor should be impossible (it's a subscription id);
			// stop rather than risk an infinite loop, and surface the anomaly.
			d.log().Warn("subscription fan-out: unparseable page cursor; stopping early",
				zap.String("tenant_id", tenantID), zap.String("cursor", next), zap.Error(pErr))
			break
		}
		afterID = parsed
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
		if !d.subscriptionMatches(sub, evt) {
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

// defaultEventEvaluator backs Dispatchers constructed without an explicit
// Filter (struct literals, tests). The compile cache is process-shared and
// concurrency-safe, so one instance serves every such Dispatcher.
var defaultEventEvaluator = cel.NewEvaluator()

// subscriptionMatches reports whether evt satisfies sub's CEL filter,
// evaluated against cel.EventEnvelopeSchema. An empty filter matches every
// event (the EventSubscription contract: "empty → all events"). A filter that
// fails to compile or evaluate is treated as NON-matching (fail-closed) and
// logged loudly: the write path validates filters (see eventsubh.Create/
// Update), so this only fires on a filter stored before that validation
// existed or a genuine runtime error — dropping-and-warning is safer than
// fanning out events the operator meant to exclude.
func (d *Dispatcher) subscriptionMatches(sub admindomain.EventSubscription, evt Event) bool {
	if sub.CELFilter == "" {
		return true
	}
	eval := d.Filter
	if eval == nil {
		eval = defaultEventEvaluator
	}
	prog, err := eval.Compile(cel.EventEnvelopeSchema, sub.CELFilter)
	if err != nil {
		d.log().Warn("subscription filter did not compile; skipping delivery (fail-closed)",
			zap.String("subscription_id", sub.SubscriptionID.String()),
			zap.String("filter", sub.CELFilter),
			zap.Error(err),
		)
		return false
	}
	ok, err := cel.Match(prog, eventCELVars(evt))
	if err != nil {
		d.log().Warn("subscription filter eval error; skipping delivery (fail-closed)",
			zap.String("subscription_id", sub.SubscriptionID.String()),
			zap.String("filter", sub.CELFilter),
			zap.Error(err),
		)
		return false
	}
	return ok
}

// eventCELVars projects an Event onto the cel.EventEnvelopeSchema variable
// set. EVERY declared schema var must be present or cel-go errors on an
// unknown attribute at eval time, so absent values are supplied as their
// zero value (a filter referencing them evaluates to false/0 rather than
// erroring). The CloudEvents 1.0 envelope fields aren't populated on the
// producer-side Event; the payload-derived fields are pulled from evt.Payload
// by key (object events carry object_key / etag / size_bytes today).
func eventCELVars(evt Event) map[string]any {
	sev := classifyEvent(evt)
	return map[string]any{
		"type":            evt.Type,
		"at":              evt.At,
		"tenant_id":       evt.TenantID,
		"resource_name":   evt.ResourceName,
		"actor_subject":   evt.ActorSubject,
		"id":              evt.ID,
		"source":          "",
		"specversion":     "",
		"time":            "",
		"datacontenttype": "",
		"subject":         "",
		"kind":            kindFromType(evt.Type),
		"severity":        sev.label,
		"severity_level":  sev.level,
		"object_key":      payloadString(evt.Payload, "object_key"),
		"bucket_name":     eventBucketName(evt),
		"etag":            payloadString(evt.Payload, "etag"),
		"size_bytes":      payloadInt(evt.Payload, "size_bytes"),
	}
}

// eventBucketName resolves the bucket a filter can match on. A producer that
// stamps "bucket_name" in the payload wins; otherwise it's derived from the
// A-shape resource name (…/buckets/<name>/…), which object and bucket events
// carry. Empty when neither is available (a C-shape object event whose
// binding didn't resolve, or a non-bucket resource) — empty, never wrong.
func eventBucketName(evt Event) string {
	if b := payloadString(evt.Payload, "bucket_name"); b != "" {
		return b
	}
	return bucketFromResourceName(evt.ResourceName)
}

// kindFromType extracts the resource kind from a canonical event type of the
// form "paladin.<kind>.<verb>" — "paladin.object.uploaded" → "object",
// "paladin.object_key.created" → "object_key", "paladin.audit.login" → "audit". All
// producers emit this shape (see the EventType constants and the paladin.* string
// literals across the dispatch call sites). Returns "" for anything that
// doesn't fit, so a `kind ==` filter never matches something wrong.
func kindFromType(t string) string {
	const prefix = "paladin."
	if !strings.HasPrefix(t, prefix) {
		return ""
	}
	rest := t[len(prefix):] // "<kind>.<verb>..."
	if i := strings.IndexByte(rest, '.'); i > 0 {
		return rest[:i]
	}
	return ""
}

// severityInfo pairs the human-readable severity label with its ordered level.
// Both surface as CEL vars (severity string, severity_level int); the number
// carries the ordering the lexicographic string can't ("critical" < "info" <
// "warning" alphabetically), so subscribers threshold on it: severity_level >=
// 30. Levels are gapped (10/30/50) so a rank can be inserted later (e.g. an
// "error" at 40) without renumbering existing filters.
type severityInfo struct {
	label string
	level int64
}

var (
	sevInfo     = severityInfo{"info", 10}
	sevWarning  = severityInfo{"warning", 30}
	sevCritical = severityInfo{"critical", 50}
)

// eventSeverityByType is the static classification. It holds only the types the
// destructive-verb heuristic below can't infer — today just the security event
// whose verb ("credentials_rotated") isn't destructive. Destructive verbs
// (purged/deleted/deleting/trashed) are handled by the heuristic so a new
// *.deleted type isn't silently "info".
var eventSeverityByType = map[string]severityInfo{
	"paladin.backend.credentials_rotated": sevWarning, // security-relevant, non-destructive verb
}

// classifyEvent resolves an event's severity. Precedence:
//  1. an explicit payload "severity" (audit-mirror can stamp it from is_error);
//  2. a permanent object delete (payload.mode == "permanent") → critical, since
//     soft and hard object deletes share the paladin.object.deleted type;
//  3. the static per-type override;
//  4. the destructive-verb safety net;
//  5. else info.
func classifyEvent(evt Event) severityInfo {
	if s := payloadString(evt.Payload, "severity"); s != "" {
		return severityForLabel(s)
	}
	if payloadString(evt.Payload, "mode") == "permanent" {
		return sevCritical // hard delete (vs "soft") — irreversible
	}
	if si, ok := eventSeverityByType[evt.Type]; ok {
		return si
	}
	switch verbOf(evt.Type) {
	case "purged":
		return sevCritical
	case "deleted", "deleting", "trashed":
		return sevWarning
	}
	return sevInfo
}

// severityForLabel maps a producer-supplied label back to its level so an
// explicit payload severity still gets an ordered severity_level. An unknown
// label keeps level 0 (sorts below info) — it's an unranked custom value.
func severityForLabel(label string) severityInfo {
	switch label {
	case sevInfo.label:
		return sevInfo
	case sevWarning.label:
		return sevWarning
	case sevCritical.label:
		return sevCritical
	default:
		return severityInfo{label: label, level: 0}
	}
}

// verbOf returns the final ".<verb>" segment of an event type
// ("paladin.object.deleted" → "deleted"). "" when there's no dot.
func verbOf(t string) string {
	if i := strings.LastIndexByte(t, '.'); i >= 0 && i+1 < len(t) {
		return t[i+1:]
	}
	return ""
}

// bucketFromResourceName extracts the bucket segment from an A-shape resource
// name (storageBackends/{b}/buckets/{bk}/…). The "/buckets/" delimiter sits
// between the backend id and any user-controlled key, so the first match is
// always the real bucket. Returns "" when the name carries no bucket segment.
func bucketFromResourceName(name string) string {
	const sep = "/buckets/"
	i := strings.Index(name, sep)
	if i < 0 {
		return ""
	}
	rest := name[i+len(sep):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}
	return rest // trailing bucket segment (e.g. a bucket-lifecycle event)
}

// payloadString reads a string key from an event payload, returning "" when
// absent or not a string — keeps eventCELVars total over the schema without
// panicking on a producer that shaped the field differently.
func payloadString(p map[string]any, key string) string {
	if s, ok := p[key].(string); ok {
		return s
	}
	return ""
}

// payloadInt reads an integer key from an event payload as int64 (cel's
// IntType). Producers stamp Go ints directly (in-process, pre-JSON), but a
// float64 (post-JSON round-trip) is coerced too; anything else → 0.
func payloadInt(p map[string]any, key string) int64 {
	switch v := p[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
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
	case "sqs":
		return d.deliverSQS(ctx, sub, evt)
	case "rabbitmq":
		return d.deliverRabbitMQ(ctx, sub, evt)
	case "kafka":
		return d.deliverKafka(ctx, sub, evt)
	default:
		return 0, fmt.Errorf("unknown sink kind %q", sub.SinkKind)
	}
}

// deliverHTTPWithStatus signs and POSTs `evt` to the sub's HTTP sink,
// returning the HTTP status code of the last attempt (0 if no response was
// received — i.e. transport error) for the outbox loop to persist for
// operator visibility. Per-attempt retries here are intentionally narrow
// (sub.MaxAttempts) — they cover transient flakes within a single delivery
// attempt. The outbox loop's row-level retry budget is the durable retry
// path; this loop just gives flaky-network requests a small in-process
// re-try window before the row is marked for backoff.
func (d *Dispatcher) deliverHTTPWithStatus(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	var sink struct {
		URL              string `json:"url"`
		SigningSecretRef string `json:"signing_secret_ref"`
		MaxAttempts      int32  `json:"max_attempts"`
		Format           string `json:"format"`
	}
	if err := json.Unmarshal(sub.SinkConfig, &sink); err != nil {
		return 0, fmt.Errorf("decode sink config: %w", err)
	}
	if sink.URL == "" {
		return 0, errors.New("http sink missing url")
	}
	// Wire format: the default (empty / unset) is now the CloudEvents 1.0
	// envelope the broker sinks emit — HTTP is symmetric with every other sink.
	// Only an explicit "raw" keeps the legacy bare Event JSON, for a webhook
	// subscriber that predates the flip and still parses the old shape. The HMAC
	// signature (below) covers whichever body we send, so verification is
	// unaffected. (Default flipped from "raw" → "cloudevents"; the UI now sends
	// an explicit "raw"/"cloudevents", so this only affects API-created subs
	// that left format unset.)
	contentType := "application/cloudevents+json"
	var (
		body []byte
		err  error
	)
	if sink.Format == "raw" {
		contentType = "application/json"
		body, err = json.Marshal(evt)
	} else {
		body, err = json.Marshal(d.newCloudEventEnvelope(sub, evt))
	}
	// dedupID is the retry-stable delivery-row id (CloudEvents `id`). The
	// cloudevents body carries it, but the raw body doesn't — so surface it in a
	// header on BOTH formats. The outbox is at-least-once (a 2xx received but a
	// failed commit → redelivery), so subscribers MUST dedup on this id; the raw
	// path had no dedup key before. See docs/event-delivery-dedup.md.
	dedupID := evt.ID
	if dedupID == "" {
		dedupID = sub.SubscriptionID.String()
	}
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
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-PALADIN-Event-Type", evt.Type)
		req.Header.Set("X-PALADIN-Subscription-Id", sub.SubscriptionID.String())
		// Dedup key (= CloudEvents `id`), stable across retries. Emitted on both
		// formats so a raw-format subscriber — whose body carries no CE id — can
		// still dedup an at-least-once redelivery.
		req.Header.Set("X-PALADIN-Event-Id", dedupID)
		// signing_secret_ref: either the HMAC key inline (lab-grade) or a
		// "k8s:<name>/<key>" Secret ref resolved at delivery time — see
		// sink_secrets.go. Resolution errors fail the attempt (retryable):
		// signing with the literal ref string would produce signatures the
		// subscriber can never verify.
		if sink.SigningSecretRef != "" {
			secret, serr := d.resolveSinkValue(ctx, sink.SigningSecretRef)
			if serr != nil {
				return 0, serr
			}
			sig := signHMAC(body, secret)
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
// RLS PrepareConn hook; the policy WITH CHECK clause keeps the
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
	//
	// Rows targeting the SAME batchable sink are collected and flushed together
	// after the loop — SQS via SendMessageBatch (≤10/call), NATS via N publishes
	// + one Flush per connection, Kafka via one WriteMessages(msgs...). Batching
	// changes the transport only: every row keeps its own attempts / backoff /
	// permanent bookkeeping via the per-row outcome map.
	type sqsQueued struct {
		row  pending
		sub  admindomain.EventSubscription
		item sqsBatchItem
	}
	type natsQueued struct {
		row  pending
		item natsBatchItem
	}
	type kafkaQueued struct {
		row  pending
		item kafkaBatchItem
	}
	sqsGroups := map[string][]sqsQueued{}
	sqsCfgs := map[string]sqsSinkConfig{}
	natsGroups := map[string][]natsQueued{}
	kafkaGroups := map[string][]kafkaQueued{}
	for _, p := range batchRows {
		evt := Event{}
		if err := json.Unmarshal(p.payload, &evt); err != nil {
			r.markFailed(ctx, tx, p.id, p.attempts, 0, fmt.Sprintf("decode payload: %v", err), true)
			continue
		}
		// Stamp the unique, retry-stable delivery id so sinks (NATS
		// CloudEvents) can emit a CloudEvents-conformant `id`.
		evt.ID = p.id.String()

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

		if key, cfg, ok := sqsGroupTarget(sub); ok && r.Dispatcher.SQS != nil {
			sqsCfgs[key] = cfg
			sqsGroups[key] = append(sqsGroups[key], sqsQueued{
				row: p, sub: sub,
				item: sqsBatchItem{RowID: p.id, Sub: sub, Evt: evt},
			})
			continue
		}
		if key, ok := natsGroupTarget(sub); ok && r.Dispatcher.NATS != nil {
			natsGroups[key] = append(natsGroups[key], natsQueued{
				row:  p,
				item: natsBatchItem{RowID: p.id, Sub: sub, Evt: evt},
			})
			continue
		}
		if key, ok := kafkaGroupTarget(sub); ok && r.Dispatcher.Kafka != nil {
			kafkaGroups[key] = append(kafkaGroups[key], kafkaQueued{
				row:  p,
				item: kafkaBatchItem{RowID: p.id, Sub: sub, Evt: evt},
			})
			continue
		}

		status, deliverErr := r.Dispatcher.deliver(ctx, sub, evt)
		if deliverErr == nil {
			if err := r.markDelivered(ctx, tx, p.id, status); err != nil {
				return 0, err
			}
			continue
		}

		// Failure path: bump attempts, schedule next attempt, or mark
		// terminal failure when the sub's max-attempts budget is spent.
		max := r.maxAttemptsFor(sub)
		permanent := p.attempts+1 >= max
		r.markFailed(ctx, tx, p.id, p.attempts, status, deliverErr.Error(), permanent)
	}

	for key, queued := range sqsGroups {
		items := make([]sqsBatchItem, len(queued))
		for i, q := range queued {
			items[i] = q.item
		}
		outcome := r.Dispatcher.deliverSQSBatch(ctx, sqsCfgs[key], items)
		for _, q := range queued {
			if derr := outcome[q.row.id]; derr == nil {
				if err := r.markDelivered(ctx, tx, q.row.id, 0); err != nil {
					return 0, err
				}
			} else {
				max := r.maxAttemptsFor(q.sub)
				permanent := q.row.attempts+1 >= max
				r.markFailed(ctx, tx, q.row.id, q.row.attempts, 0, derr.Error(), permanent)
			}
		}
	}

	for _, queued := range natsGroups {
		items := make([]natsBatchItem, len(queued))
		for i, q := range queued {
			items[i] = q.item
		}
		outcome := r.Dispatcher.deliverNATSBatch(ctx, items)
		for _, q := range queued {
			if derr := outcome[q.row.id]; derr == nil {
				if err := r.markDelivered(ctx, tx, q.row.id, 0); err != nil {
					return 0, err
				}
			} else {
				max := r.maxAttemptsFor(q.item.Sub)
				permanent := q.row.attempts+1 >= max
				r.markFailed(ctx, tx, q.row.id, q.row.attempts, 0, derr.Error(), permanent)
			}
		}
	}

	for _, queued := range kafkaGroups {
		items := make([]kafkaBatchItem, len(queued))
		for i, q := range queued {
			items[i] = q.item
		}
		outcome := r.Dispatcher.deliverKafkaBatch(ctx, items)
		for _, q := range queued {
			if derr := outcome[q.row.id]; derr == nil {
				if err := r.markDelivered(ctx, tx, q.row.id, 0); err != nil {
					return 0, err
				}
			} else {
				max := r.maxAttemptsFor(q.item.Sub)
				permanent := q.row.attempts+1 >= max
				r.markFailed(ctx, tx, q.row.id, q.row.attempts, 0, derr.Error(), permanent)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit batch: %w", err)
	}
	committed = true
	return len(batchRows), nil
}

// markDelivered stamps a row delivered. Shared by the per-row and the
// SQS-batched paths so the success bookkeeping can't drift.
func (r *OutboxRunner) markDelivered(ctx context.Context, tx pgx.Tx, id uuid.UUID, statusCode int) error {
	if _, err := tx.Exec(ctx,
		`UPDATE event_deliveries
		    SET status='delivered',
		        attempts=attempts+1,
		        last_attempt_at=now(),
		        last_status_code=$2,
		        delivered_at=now()
		  WHERE id=$1`,
		id, statusCode,
	); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	return nil
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

// DeliveryStats is the dispatcher's operator view: global queue depth plus a
// per-subscription breakdown of what's stuck and why. Served by the pod's
// ops listener (/system/dispatcher-stats.json) and proxied to the console by
// admin SystemService.GetDispatcherStats — the dispatcher pod computes it
// because its pool is the BYPASSRLS one (event_deliveries is RLS'd per
// tenant, and this view is deliberately cross-tenant / operator-only).
type DeliveryStats struct {
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
	// OldestPendingSeconds is the age of the oldest still-pending row — the
	// single best "is the loop keeping up" number. 0 when nothing is pending.
	OldestPendingSeconds int64                   `json:"oldest_pending_seconds"`
	Subscriptions        []SubscriptionDelivStat `json:"subscriptions"`
}

// SubscriptionDelivStat aggregates one subscription's undelivered work. Only
// subscriptions with pending or failed rows appear — a healthy subscription
// has nothing to report.
type SubscriptionDelivStat struct {
	SubscriptionID string `json:"subscription_id"`
	TenantID       string `json:"tenant_id"`
	Pending        int64  `json:"pending"`
	Failed         int64  `json:"failed"`
	// LastError / LastStatusCode / LastAttemptAt come from the row with the
	// most recent attempt, so the operator sees the CURRENT failure reason.
	LastError      string `json:"last_error,omitempty"`
	LastStatusCode int32  `json:"last_status_code,omitempty"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"` // RFC3339; "" = never attempted
}

// deliveryStatsMaxSubscriptions caps the per-subscription breakdown so one
// pathological tenant can't balloon the ops payload; worst offenders (most
// failed, then most pending) sort first, so the cap trims the healthy tail.
const deliveryStatsMaxSubscriptions = 100

// DeliveryStats computes the operator view in two cheap aggregate queries.
func (r *OutboxRunner) DeliveryStats(ctx context.Context) (*DeliveryStats, error) {
	out := &DeliveryStats{Subscriptions: []SubscriptionDelivStat{}}
	err := r.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status = 'failed'),
		       COALESCE(EXTRACT(EPOCH FROM now() - min(created_at) FILTER (WHERE status = 'pending'))::bigint, 0)
		FROM event_deliveries`,
	).Scan(&out.Pending, &out.Failed, &out.OldestPendingSeconds)
	if err != nil {
		return nil, fmt.Errorf("delivery stats: totals: %w", err)
	}

	rows, err := r.Pool.Query(ctx, `
		SELECT subscription_id::text, tenant_id::text,
		       count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status = 'failed'),
		       COALESCE((array_agg(last_error       ORDER BY last_attempt_at DESC NULLS LAST))[1], ''),
		       COALESCE((array_agg(last_status_code ORDER BY last_attempt_at DESC NULLS LAST))[1], 0),
		       COALESCE(to_char(max(last_attempt_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		FROM event_deliveries
		GROUP BY subscription_id, tenant_id
		HAVING count(*) FILTER (WHERE status IN ('pending', 'failed')) > 0
		ORDER BY count(*) FILTER (WHERE status = 'failed') DESC,
		         count(*) FILTER (WHERE status = 'pending') DESC
		LIMIT $1`, deliveryStatsMaxSubscriptions)
	if err != nil {
		return nil, fmt.Errorf("delivery stats: per-subscription: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s SubscriptionDelivStat
		if err := rows.Scan(&s.SubscriptionID, &s.TenantID, &s.Pending, &s.Failed,
			&s.LastError, &s.LastStatusCode, &s.LastAttemptAt); err != nil {
			return nil, fmt.Errorf("delivery stats: scan: %w", err)
		}
		out.Subscriptions = append(out.Subscriptions, s)
	}
	return out, rows.Err()
}

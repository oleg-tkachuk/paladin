// Event dispatcher worker — pulls EventSubscription rows that match an
// incoming event and delivers them to the configured sink.
//
// Sinks: HTTP (signed POST), Kafka (slice 8), SQS (slice 8). HTTP is the
// reference path covered here; the dispatcher dispatches Kafka/SQS through
// the same Sink interface so adding new transports is additive.
//
// Delivery semantics:
//
//   - Best-effort with bounded retry (exponential backoff up to MaxAttempts).
//   - HMAC-SHA256 signature over the raw JSON payload using a per-subscription
//     `signing_secret`. Header X-PALADIN-Signature is `sha256=<hex>`.
//   - Failures past MaxAttempts are logged and dropped — durable retry is a
//     follow-up via the operations queue.
package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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

// SubscriptionStore is the read seam the dispatcher uses. Defined narrowly
// so tests can stub.
type SubscriptionStore interface {
	List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error)
}

// Dispatcher delivers Events to subscribers whose filter matches. Filter
// evaluation is intentionally trivial in v2: empty filter = match-all,
// non-empty filter = exact match against `Event.Type`. CEL evaluation is
// a slice 8 follow-up.
type Dispatcher struct {
	Store      SubscriptionStore
	HTTPClient *http.Client
	Logger     *zap.Logger
	// MaxAttempts caps retry per subscription. <=0 → 3.
	MaxAttempts int
	// BaseBackoff is the delay before retry 1; doubles each subsequent.
	BaseBackoff time.Duration
}

// Dispatch fan-outs `evt` to every subscription enabled for `tenantID` whose
// filter matches. Returns the number of successful deliveries; per-sink
// errors are logged but do not abort the fan-out.
func (d *Dispatcher) Dispatch(ctx context.Context, tenantID string, evt Event) (int, error) {
	if d.Store == nil {
		return 0, errors.New("dispatcher: no subscription store")
	}
	subs, _, err := d.Store.List(ctx, admindomain.ListEventSubscriptionsArgs{PageSize: 1000})
	if err != nil {
		return 0, fmt.Errorf("list subscriptions: %w", err)
	}
	delivered := 0
	for _, sub := range subs {
		if sub.Disabled {
			continue
		}
		if sub.TenantID.String() != tenantID {
			continue
		}
		if sub.CELFilter != "" && sub.CELFilter != evt.Type {
			continue
		}
		if err := d.deliver(ctx, sub, evt); err != nil {
			d.log().Warn("failed to deliver event",
				zap.String("subscription_id", sub.SubscriptionID.String()),
				zap.String("event_type", evt.Type),
				zap.Error(err),
			)
			continue
		}
		delivered++
	}
	return delivered, nil
}

// DeliverOne delivers a synthetic test event to a single subscription.
// Used by EventSubscriptionService.TestSubscription. Bypasses store lookup +
// filter evaluation — the caller has the subscription in hand.
func (d *Dispatcher) DeliverOne(ctx context.Context, sub admindomain.EventSubscription, eventType string) error {
	return d.deliver(ctx, sub, Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     sub.TenantID.String(),
		ResourceName: fmt.Sprintf("tenants/%s/eventSubscriptions/%s", sub.TenantID, sub.SubscriptionID),
		Payload: map[string]any{
			"synthetic": true,
		},
	})
}

func (d *Dispatcher) deliver(ctx context.Context, sub admindomain.EventSubscription, evt Event) error {
	switch sub.SinkKind {
	case "http":
		return d.deliverHTTP(ctx, sub, evt)
	case "kafka", "sqs":
		return fmt.Errorf("sink %q delivery not yet wired (slice 8)", sub.SinkKind)
	default:
		return fmt.Errorf("unknown sink kind %q", sub.SinkKind)
	}
}

func (d *Dispatcher) deliverHTTP(ctx context.Context, sub admindomain.EventSubscription, evt Event) error {
	var sink struct {
		URL              string `json:"url"`
		SigningSecretRef string `json:"signing_secret_ref"`
		MaxAttempts      int32  `json:"max_attempts"`
	}
	if err := json.Unmarshal(sub.SinkConfig, &sink); err != nil {
		return fmt.Errorf("decode sink config: %w", err)
	}
	if sink.URL == "" {
		return errors.New("http sink missing url")
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
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

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, sink.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PALADIN-Event-Type", evt.Type)
		req.Header.Set("X-PALADIN-Subscription-Id", sub.SubscriptionID.String())
		// Slice 8: resolve sink.SigningSecretRef from the secret manager and
		// sign the body. Inline-secret-by-value is reserved for tests and
		// SHOULD NOT be used in production.
		if sink.SigningSecretRef != "" {
			sig := signHMAC(body, sink.SigningSecretRef)
			req.Header.Set("X-PALADIN-Signature", "sha256="+sig)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		}
		if attempt < maxAttempts {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
		}
	}
	return fmt.Errorf("delivery failed after %d attempts: %w", maxAttempts, lastErr)
}

// signHMAC produces a hex-encoded HMAC-SHA256 of body using key. Key is the
// raw secret ref text (treated as bytes). Slice-8 work is to resolve the
// ref against a secret manager before hashing.
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

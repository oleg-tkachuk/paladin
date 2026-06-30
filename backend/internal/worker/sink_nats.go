// NATS sink — slice 8 follow-up that turns the dispatcher's switch
// from "HTTP only" into a real multi-sink fan-out. Adds NATS first
// (smallest dep, pure-Go client, natural fit for cloud-native /
// agentic consumers); Kafka and SQS branch off the same shape later.
//
// CloudEvents 1.0 envelope is emitted on the wire so downstream
// consumers can use any CloudEvents SDK without a custom parser. The
// envelope shape mirrors the future cross-cutting BACKLOG entry —
// once HTTP delivery also wraps in CloudEvents the two paths share
// the encoder.
//
// Connection pooling: one *nats.Conn per unique URL string across
// the dispatcher's lifetime. Per-deliver dial costs (TLS handshake,
// SASL/NKEY exchange) are millisecond-class but multiply over the
// 50-row batches the runner processes — pooling drops that to a
// one-time cost per server pool. Pool key = url string only;
// credentials are applied during dial. Subscriptions targeting the
// same servers with different credentials would collide here, so the
// pool also keys on credentials_ref to keep that case correct.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// NatsConnPool keeps one *nats.Conn per unique (url, credentials_ref)
// pair across the dispatcher's lifetime. Reconnect / drain are
// handled by the upstream client; the pool only holds references and
// closes on shutdown.
//
// Credentials_ref is part of the key because two subscriptions
// targeting the same NATS cluster but authenticating as different
// principals must NOT share a connection — the second sub's publish
// would carry the first sub's identity.
type NatsConnPool struct {
	mu    sync.Mutex
	conns map[string]*nats.Conn
	log   *zap.Logger
}

// NewNatsConnPool builds an empty connection pool. See type doc for
// pool-key / lifetime contract. Logger may be nil (no-op zap is used).
func NewNatsConnPool(log *zap.Logger) *NatsConnPool {
	if log == nil {
		log = zap.NewNop()
	}
	return &NatsConnPool{
		conns: make(map[string]*nats.Conn),
		log:   log,
	}
}

// poolKey segregates connections by (url, credentialsRef) so two
// subscriptions to the same cluster with different auth principals
// don't share a single conn.
func poolKey(url, credentialsRef string) string {
	return url + "\x00" + credentialsRef
}

// get returns a connected *nats.Conn for the (url, credentialsRef)
// pair, dialing on first call and reusing on subsequent calls. The
// returned conn is owned by the pool; callers must NOT call Close on
// it (the pool's close() handles drain on dispatcher shutdown).
func (p *NatsConnPool) get(url, credentialsRef string) (*nats.Conn, error) {
	if url == "" {
		return nil, errors.New("nats sink: empty url")
	}
	key := poolKey(url, credentialsRef)
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[key]; ok && c.IsConnected() {
		return c, nil
	}
	// Drop a closed/reconnecting conn so we redial cleanly.
	if c, ok := p.conns[key]; ok {
		c.Close()
		delete(p.conns, key)
	}
	opts := []nats.Option{
		nats.Name("paladin-dispatcher"),
		nats.Timeout(5 * time.Second),
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(-1),
	}
	authOpt, err := parseNatsCredentials(credentialsRef)
	if err != nil {
		return nil, err
	}
	if authOpt != nil {
		opts = append(opts, authOpt)
	}
	c, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats dial %s: %w", url, err)
	}
	p.conns[key] = c
	p.log.Info("nats: connection opened",
		zap.String("url", url),
		zap.String("connected_url", c.ConnectedUrl()),
	)
	return c, nil
}

// Close drains and closes every pooled connection. Safe to call from
// shutdown paths even if no connections were ever opened.
func (p *NatsConnPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, c := range p.conns {
		// Drain blocks until in-flight publishes flush, then closes.
		// The publish path is fire-and-forget today, so this is only
		// non-trivial once async producer batching lands.
		if err := c.Drain(); err != nil {
			p.log.Warn("nats: drain failed", zap.String("key", k), zap.Error(err))
		}
		delete(p.conns, k)
	}
}

// Warmup eagerly dials each (url, credentialsRef) pair. Errors are
// reported per-pair and never abort the loop — pre-warm is best-effort.
// Used by the dispatcher pod's boot sequence so the first delivery
// doesn't pay the dial cost inside the hot tick loop, and so the
// health probe has something to report before any row arrives.
func (p *NatsConnPool) Warmup(pairs []NatsTarget) {
	for _, t := range pairs {
		if _, err := p.get(t.URL, t.CredentialsRef); err != nil {
			p.log.Warn("nats: pre-warm dial failed",
				zap.String("url", t.URL),
				zap.Error(err),
			)
		}
	}
}

// NatsTarget is a (url, credentialsRef) pair for pool warmup and
// health-probe enumeration.
type NatsTarget struct {
	URL            string
	CredentialsRef string
}

// Statuses returns a snapshot of (url, status) for every pooled conn.
// The dispatcher's health probe walks this to surface per-server
// connectivity in /system/health.json.
func (p *NatsConnPool) Statuses() map[string]nats.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]nats.Status, len(p.conns))
	for k, c := range p.conns {
		// Strip the credentialsRef suffix from the key for the
		// human-readable status map — we don't want the auth secret
		// label leaking into operator-facing surfaces.
		url := k
		if i := strings.IndexByte(k, '\x00'); i >= 0 {
			url = k[:i]
		}
		out[url] = c.Status()
	}
	return out
}

// parseNatsCredentials parses a `<scheme>:<value>` credentials_ref
// string into a nats.Option. Empty string → nil (anonymous publish,
// dev/lab only). Unknown schemes are rejected so a typo doesn't
// silently degrade to anonymous.
//
// Schemes:
//
//   - `token:<token>` — bare token auth, the common in-cluster case.
//   - `nkey:<seed>` — NKey challenge-response. The value is a user nkey
//     SEED (starts with `SU…`); we derive the public key and sign the
//     server nonce in-process via nats.Nkey.
//   - `jwt:<user-jwt>+<seed>` — decentralized (operator/account/user JWT)
//     auth. The JWT identifies the user; the trailing nkey seed signs the
//     nonce. A `nkey:` prefix on the seed half is tolerated, so both
//     `jwt:<jwt>+<seed>` and `jwt:<jwt>+nkey:<seed>` parse.
//
// All modes are wired in-memory (nats.go's UserJWTAndSeed / nkeys.FromSeed)
// — no 0600 temp-file materialisation. The credential string itself is
// expected to arrive from a K8s Secret via the sink_config resolution path,
// the same way the token value does.
func parseNatsCredentials(ref string) (nats.Option, error) {
	if ref == "" {
		return nil, nil
	}
	idx := strings.IndexByte(ref, ':')
	if idx <= 0 {
		return nil, fmt.Errorf("nats credentials_ref: missing scheme prefix (expected 'scheme:value')")
	}
	scheme, value := ref[:idx], ref[idx+1:]
	switch scheme {
	case "token":
		if value == "" {
			return nil, errors.New("nats credentials_ref: token: scheme with empty value")
		}
		return nats.Token(value), nil
	case "nkey":
		if value == "" {
			return nil, errors.New("nats credentials_ref: nkey: scheme with empty seed")
		}
		kp, err := nkeys.FromSeed([]byte(value))
		if err != nil {
			return nil, fmt.Errorf("nats credentials_ref: invalid nkey seed: %w", err)
		}
		pub, err := kp.PublicKey()
		if err != nil {
			return nil, fmt.Errorf("nats credentials_ref: nkey public key: %w", err)
		}
		// Sign from the in-memory keypair on each (re)connect nonce.
		return nats.Nkey(pub, func(nonce []byte) ([]byte, error) {
			return kp.Sign(nonce)
		}), nil
	case "jwt":
		jwtStr, seedPart, ok := strings.Cut(value, "+")
		seed := strings.TrimPrefix(seedPart, "nkey:")
		if !ok || jwtStr == "" || seed == "" {
			return nil, errors.New("nats credentials_ref: jwt: scheme expects '<jwt>+<seed>'")
		}
		return nats.UserJWTAndSeed(jwtStr, seed), nil
	default:
		return nil, fmt.Errorf("nats credentials_ref: unknown scheme %q", scheme)
	}
}

// natsSinkConfig is the JSON shape stored in event_subscriptions.sink_config
// for sink_kind='nats'. Mirrors NatsSink in the proto.
type natsSinkConfig struct {
	URL            string `json:"url"`
	Subject        string `json:"subject"`
	CredentialsRef string `json:"credentials_ref"`
}

// cloudEventEnvelope is the on-the-wire shape published to the
// configured NATS subject. Field names match CloudEvents 1.0 with
// the `tenantid` extension (CE extensions are flat lower-case).
type cloudEventEnvelope struct {
	SpecVersion     string         `json:"specversion"`
	Type            string         `json:"type"`
	Source          string         `json:"source"`
	ID              string         `json:"id"`
	Time            string         `json:"time"`
	Subject         string         `json:"subject,omitempty"`
	TenantID        string         `json:"tenantid,omitempty"`
	DataContentType string         `json:"datacontenttype"`
	Data            map[string]any `json:"data,omitempty"`
}

// natsSource is the CloudEvents `source` value stamped on every
// outbound envelope. Operators override it via cfg.Runtime.SiteURL
// when building the dispatcher; the literal "paladin" is a sane fallback
// for in-cluster lab deployments where there is no canonical URL.
const natsDefaultSource = "paladin"

// newCloudEventEnvelope builds the CloudEvents 1.0 envelope shared by every
// JSON-publishing sink (NATS, SQS, RabbitMQ). The `id` MUST be unique per
// event for a given source so consumers can dedup: prefer the delivery-row
// id (stamped by the drain loop, stable across retries) and fall back to the
// subscription id only on the synchronous DeliverOne test path, which has no
// delivery row.
func (d *Dispatcher) newCloudEventEnvelope(sub admindomain.EventSubscription, evt Event) cloudEventEnvelope {
	eventID := evt.ID
	if eventID == "" {
		eventID = sub.SubscriptionID.String()
	}
	return cloudEventEnvelope{
		SpecVersion:     "1.0",
		Type:            evt.Type,
		Source:          natsDefaultSource,
		ID:              eventID,
		Time:            evt.At.UTC().Format(time.RFC3339Nano),
		Subject:         evt.ResourceName,
		TenantID:        evt.TenantID,
		DataContentType: "application/json",
		Data:            evt.Payload,
	}
}

// deliverNATS publishes a CloudEvents 1.0 envelope to the NATS
// subject configured on the subscription. Fire-and-forget at the
// protocol level — there's no broker ack like HTTP's 2xx, so success
// just means "Publish returned no error". Failures map to
// statusCode=0 so the dispatcher's existing UI / metrics treat them
// the same as transport errors on HTTP sinks.
func (d *Dispatcher) deliverNATS(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	if d.NATS == nil {
		return 0, errors.New("nats sink: dispatcher has no NATS pool")
	}
	var cfg natsSinkConfig
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return 0, fmt.Errorf("nats sink: decode config: %w", err)
	}
	if cfg.URL == "" {
		return 0, errors.New("nats sink: missing url")
	}
	if cfg.Subject == "" {
		return 0, errors.New("nats sink: missing subject")
	}

	// CloudEvents `id` MUST be unique per event for a given source so
	// consumers can dedup. Prefer the delivery-row id (stamped by the drain
	// loop, stable across retries); fall back to the subscription id only on
	// the synchronous DeliverOne test path, which has no delivery row.
	body, err := json.Marshal(d.newCloudEventEnvelope(sub, evt))
	if err != nil {
		return 0, fmt.Errorf("nats sink: marshal envelope: %w", err)
	}

	// Honour ctx for cancellation but the publish itself is
	// non-blocking — nats.go buffers in-process and flushes async.
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	conn, err := d.NATS.get(cfg.URL, cfg.CredentialsRef)
	if err != nil {
		return 0, err
	}
	if err := conn.Publish(cfg.Subject, body); err != nil {
		return 0, fmt.Errorf("nats publish: %w", err)
	}
	// Flush synchronously so the publish error (if any) surfaces
	// before we mark the row delivered. Without this the nats client
	// would queue the message and return nil even when the server
	// is unreachable, causing rows to flip to delivered while the
	// message sits in a doomed buffer.
	if err := conn.FlushTimeout(2 * time.Second); err != nil {
		return 0, fmt.Errorf("nats flush: %w", err)
	}
	return 0, nil
}

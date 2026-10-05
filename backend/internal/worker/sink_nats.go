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

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/logfield"
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
	conns map[natsPoolKey]*nats.Conn
	log   *zap.Logger
}

// NewNatsConnPool builds an empty connection pool. See type doc for
// pool-key / lifetime contract. Logger may be nil (no-op zap is used).
func NewNatsConnPool(log *zap.Logger) *NatsConnPool {
	if log == nil {
		log = zap.NewNop()
	}
	return &NatsConnPool{
		conns: make(map[natsPoolKey]*nats.Conn),
		log:   log,
	}
}

// natsPoolKey segregates connections by (url, credentialsRef) so two
// subscriptions to the same cluster with different auth principals don't
// share a single conn. A struct rather than the two joined into a string, so
// reading the URL back out — for a log line, for the status map — is a field
// access and not a parse of a separator every reader has to agree on.
type natsPoolKey struct {
	url, credentialsRef string
}

func poolKey(url, credentialsRef string) natsPoolKey {
	return natsPoolKey{url: url, credentialsRef: credentialsRef}
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
		logfield.URL("url", url),
		logfield.URL("connected_url", c.ConnectedUrl()),
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
			p.log.Warn("nats: drain failed", logfield.URL("url", k.url), zap.Error(err))
		}
		delete(p.conns, k)
	}
}

// Warmup eagerly dials each distinct (url, credentialsRef) pair. Errors are
// reported per-pair and never abort the loop — pre-warm is best-effort.
// Used by the dispatcher pod's boot sequence so the first delivery
// doesn't pay the dial cost inside the hot tick loop, and so the
// health probe has something to report before any row arrives.
//
// Duplicates are dropped here rather than by the caller: a failed dial is not
// cached, so a pair listed twice would dial a dead server twice.
func (p *NatsConnPool) Warmup(pairs []NatsTarget) {
	seen := make(map[natsPoolKey]bool, len(pairs))
	for _, t := range pairs {
		key := poolKey(t.URL, t.CredentialsRef)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := p.get(t.URL, t.CredentialsRef); err != nil {
			p.log.Warn("nats: pre-warm dial failed",
				logfield.URL("url", t.URL),
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

// NatsTargetFromSinkConfig reads the pool target out of a nats subscription's
// stored sink_config. ok is false for a config that does not decode or names
// no URL — nothing a warmup could dial.
func NatsTargetFromSinkConfig(raw []byte) (t NatsTarget, ok bool) {
	var cfg natsSinkConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.URL == "" {
		return NatsTarget{}, false
	}
	return NatsTarget{URL: cfg.URL, CredentialsRef: cfg.CredentialsRef}, true
}

// Statuses returns a snapshot of (url, status) for every pooled conn.
// The dispatcher's health probe walks this to surface per-server
// connectivity in /system/health.json.
func (p *NatsConnPool) Statuses() map[string]nats.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]nats.Status, len(p.conns))
	for k, c := range p.conns {
		// Keyed by URL alone: the credentials label stays out of the
		// operator-facing status map.
		out[k.url] = c.Status()
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
	// JetStream switches from core publish (fire-and-forget) to a synchronous
	// JetStream publish with Nats-Msg-Id = the CloudEvents id (server-side
	// dedup within the stream's duplicate window). Requires a provisioned
	// stream whose subject filter covers Subject.
	JetStream bool `json:"jetstream"`
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
// natsDedupID is the JetStream Nats-Msg-Id (= the CloudEvents id): the
// retry-stable delivery-row id, falling back to the subscription id on the
// synchronous DeliverOne test path that has no delivery row. Matches the id the
// CloudEvents envelope carries so a consumer's dedup and the server's dedup
// agree.
func natsDedupID(sub admindomain.EventSubscription, evt Event) string {
	if evt.ID != "" {
		return evt.ID
	}
	return sub.SubscriptionID.String()
}

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

	// credentials_ref may itself be a "k8s:" Secret ref (sink_secrets.go);
	// the resolved value carries the usual scheme string (token:/nkey:/jwt:).
	// The resolved form keys the pool, so a rotated Secret dials fresh.
	credentialsRef, err := d.resolveSinkValue(ctx, cfg.CredentialsRef)
	if err != nil {
		return 0, fmt.Errorf("nats sink: %w", err)
	}
	conn, err := d.NATS.get(cfg.URL, credentialsRef)
	if err != nil {
		return 0, err
	}
	if cfg.JetStream {
		// JetStream mode: synchronous publish with Nats-Msg-Id = the
		// CloudEvents id, so the server persists durably and dedups a
		// redelivery within the stream's duplicate window. The PubAck is the
		// durability confirmation — no separate flush needed.
		js, jerr := jetstream.New(conn)
		if jerr != nil {
			return 0, fmt.Errorf("nats jetstream: %w", jerr)
		}
		if _, perr := js.Publish(ctx, cfg.Subject, body, jetstream.WithMsgID(natsDedupID(sub, evt))); perr != nil {
			return 0, fmt.Errorf("nats jetstream publish: %w", perr)
		}
		return 0, nil
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

// ─── Batched delivery (outbox fan-in) ────────────────────────────────────────

// natsBatchItem is one outbox row headed for a shared NATS connection.
type natsBatchItem struct {
	RowID uuid.UUID
	Sub   admindomain.EventSubscription
	Evt   Event
}

// natsGroupTarget derives the batch-group key for a subscription IFF it is a
// well-formed NATS sink. The key is the CONNECTION — url + raw credentials_ref
// — NOT the subject: every row on one connection shares a single Flush
// regardless of subject, so distinct-subject rows to the same server still
// batch. Malformed rows return ok=false and take the per-row deliver path,
// failing with the same error text as before batching. So do JetStream rows:
// the batch is a core publish, which the stream neither acknowledges nor
// deduplicates.
func natsGroupTarget(sub admindomain.EventSubscription) (key natsPoolKey, ok bool) {
	if sub.SinkKind != "nats" {
		return natsPoolKey{}, false
	}
	var cfg natsSinkConfig
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return natsPoolKey{}, false
	}
	if cfg.URL == "" || cfg.Subject == "" || cfg.JetStream {
		return natsPoolKey{}, false
	}
	return poolKey(cfg.URL, cfg.CredentialsRef), true
}

// deliverNATSBatch publishes every row in one group to its own subject over a
// single pooled connection, then Flushes ONCE. The flush is the round-trip that
// surfaces transport errors; batching turns N (publish+flush) into N publishes
// + 1 flush. A per-message publish error fails just that row; a flush error
// fails every row that published (the buffered messages are doomed). All items
// share url + credentials_ref by construction, so the connection is resolved
// from the first item.
func (d *Dispatcher) deliverNATSBatch(ctx context.Context, items []natsBatchItem) map[uuid.UUID]error {
	out := make(map[uuid.UUID]error, len(items))
	failAll := func(err error) map[uuid.UUID]error {
		for _, it := range items {
			out[it.RowID] = err
		}
		return out
	}
	if d.NATS == nil {
		return failAll(errors.New("nats sink: dispatcher has no NATS pool"))
	}
	if err := ctx.Err(); err != nil {
		return failAll(err)
	}
	var cfg0 natsSinkConfig
	if err := json.Unmarshal(items[0].Sub.SinkConfig, &cfg0); err != nil {
		return failAll(fmt.Errorf("nats sink: decode config: %w", err))
	}
	credentialsRef, err := d.resolveSinkValue(ctx, cfg0.CredentialsRef)
	if err != nil {
		return failAll(fmt.Errorf("nats sink: %w", err))
	}
	conn, err := d.NATS.get(cfg0.URL, credentialsRef)
	if err != nil {
		return failAll(err)
	}

	published := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		var cfg natsSinkConfig
		if uerr := json.Unmarshal(it.Sub.SinkConfig, &cfg); uerr != nil {
			out[it.RowID] = fmt.Errorf("nats sink: decode config: %w", uerr)
			continue
		}
		body, merr := json.Marshal(d.newCloudEventEnvelope(it.Sub, it.Evt))
		if merr != nil {
			out[it.RowID] = fmt.Errorf("nats sink: marshal envelope: %w", merr)
			continue
		}
		if perr := conn.Publish(cfg.Subject, body); perr != nil {
			out[it.RowID] = fmt.Errorf("nats publish: %w", perr)
			continue
		}
		published = append(published, it.RowID)
	}
	if len(published) == 0 {
		return out
	}
	if ferr := conn.FlushTimeout(2 * time.Second); ferr != nil {
		wrapped := fmt.Errorf("nats flush: %w", ferr)
		for _, id := range published {
			out[id] = wrapped
		}
		return out
	}
	for _, id := range published {
		out[id] = nil
	}
	return out
}

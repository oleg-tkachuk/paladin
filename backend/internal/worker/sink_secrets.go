package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Sink-credential secret resolution.
//
// Sink configs carry credential-shaped strings — the HTTP HMAC signing
// secret, Kafka SASL password / mTLS PEM, the NATS credentials_ref, the
// AMQP URL (user:pass embedded). Inline values are lab-grade; production
// wants the material in a Kubernetes Secret. Any of those fields may
// instead use the reference form
//
//	k8s:<name>/<key>          — Secret in the pod's own namespace
//	k8s:<namespace>/<name>/<key>
//
// which the dispatcher resolves at delivery time through the injected
// SinkSecretResolver (the same in-cluster Secret-GET the config resolver
// uses at boot; the pod's RBAC allowlist must include the Secret name).
// Values without the "k8s:" prefix pass through untouched, so existing
// inline configs keep working.

// SinkSecretResolver fetches one key of one Kubernetes Secret. Implemented
// in the composition root by wrapping config.K8sSecretResolver — declared
// here on primitives so the worker package stays config-free.
type SinkSecretResolver interface {
	// ResolveSinkSecret returns the plaintext value. namespace == "" means
	// the pod's own namespace.
	ResolveSinkSecret(ctx context.Context, namespace, name, key string) (string, error)
}

// sinkSecretPrefix marks a sink-config value as a Secret reference.
const sinkSecretPrefix = "k8s:"

// sinkSecretTTL bounds how long a resolved value is served from the
// in-process cache. Short on purpose: a rotated Secret takes effect within
// this window without a pod restart, while the steady-state delivery path
// stays free of per-event API-server round-trips.
const sinkSecretTTL = time.Minute

type cachedSinkSecret struct {
	value string
	at    time.Time
}

// sinkSecretCache is the per-Dispatcher TTL cache, keyed by the full ref
// string. Not size-bounded: the key space is the set of distinct refs in
// sink configs, which is operator-curated and small.
type sinkSecretCache struct {
	mu sync.Mutex
	m  map[string]cachedSinkSecret
}

// resolveSinkValue passes non-ref values through, and resolves "k8s:" refs
// via d.Secrets with the TTL cache. A ref with no resolver wired is a hard
// error — silently using the literal "k8s:…" string as a credential would
// be a confusing auth failure at best and a secret-shaped log leak at worst.
func (d *Dispatcher) resolveSinkValue(ctx context.Context, v string) (string, error) {
	if !strings.HasPrefix(v, sinkSecretPrefix) {
		return v, nil
	}
	if d.Secrets == nil {
		return "", errors.New("sink config uses a k8s: secret ref but the dispatcher has no secret resolver wired")
	}
	namespace, name, key, err := parseSinkSecretRef(v)
	if err != nil {
		return "", err
	}

	if d.secretCache == nil {
		// Lazily initialised under the struct-literal construction style the
		// Dispatcher uses everywhere; guarded by the cache mutex below being
		// per-instance. A race on first use at worst double-allocates.
		d.secretCache = &sinkSecretCache{m: map[string]cachedSinkSecret{}}
	}
	c := d.secretCache
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.m[v]; ok && now.Sub(e.at) < sinkSecretTTL {
		c.mu.Unlock()
		return e.value, nil
	}
	c.mu.Unlock()

	val, err := d.Secrets.ResolveSinkSecret(ctx, namespace, name, key)
	if err != nil {
		return "", fmt.Errorf("resolve sink secret %s: %w", v, err)
	}
	c.mu.Lock()
	c.m[v] = cachedSinkSecret{value: val, at: now}
	c.mu.Unlock()
	return val, nil
}

// parseSinkSecretRef splits "k8s:[ns/]name/key". The name/key form uses the
// pod namespace (namespace returned empty).
func parseSinkSecretRef(ref string) (namespace, name, key string, err error) {
	parts := strings.Split(strings.TrimPrefix(ref, sinkSecretPrefix), "/")
	switch len(parts) {
	case 2:
		namespace, name, key = "", parts[0], parts[1]
	case 3:
		namespace, name, key = parts[0], parts[1], parts[2]
	default:
		return "", "", "", fmt.Errorf(
			"malformed sink secret ref %q: want k8s:<name>/<key> or k8s:<ns>/<name>/<key>", ref)
	}
	if name == "" || key == "" {
		return "", "", "", fmt.Errorf("malformed sink secret ref %q: empty name or key", ref)
	}
	return namespace, name, key, nil
}

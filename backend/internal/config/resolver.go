package config

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

// Redacted is the placeholder used in Obfuscated() for sensitive fields.
const Redacted = "***"

const (
	k8sTokenPath       = "/var/run/secrets/kubernetes.io/serviceaccount/token" // #nosec G101
	k8sNamespacePath   = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	defaultHTTPTimeout = 5 * time.Second
)

// How long a secret read keeps retrying a Kubernetes API that does not answer:
// 7 attempts, the wait doubling from 0.5s to 16s, about 31s in all. A pod that
// starts while the API server is briefly unreachable waits it out instead of
// exiting, which left a rollout in CrashLoopBackOff until it was deleted by
// hand. What the API refuses (not found, forbidden) is not retried.
//
// No wait.Backoff Cap: reaching it ends the retries rather than holding the
// wait at that length.
const (
	secretReadFirstDelay = 500 * time.Millisecond
	secretReadFactor     = 2.0
	secretReadJitter     = 0.1
	secretReadAttempts   = 7
)

func defaultSecretReadBackoff() wait.Backoff {
	return wait.Backoff{
		Duration: secretReadFirstDelay,
		Factor:   secretReadFactor,
		Jitter:   secretReadJitter,
		Steps:    secretReadAttempts,
	}
}

// retryableSecretRead reports whether a failed read may succeed if repeated:
// the API was unreachable, timed out or was overloaded. An answer it gave
// about the request itself will be the same next time.
func retryableSecretRead(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	refused := apierrors.IsNotFound(err) || apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err) || apierrors.IsBadRequest(err) ||
		apierrors.IsInvalid(err) || apierrors.IsMethodNotSupported(err)
	return !refused
}

// Resolver resolves secret references in the configuration.
type Resolver interface {
	ResolveConfig(ctx context.Context, cfg *Config) error
}

// K8sSecretResolver resolves Kubernetes secrets through client-go, with the
// pod's ServiceAccount.
type K8sSecretResolver struct {
	log       *zap.Logger
	tokenPath string
	nsPath    string

	// The clientset is built on first use: the resolver is constructed out of
	// cluster too (local runs, tests), where there is no in-cluster config.
	clientOnce sync.Once
	client     kubernetes.Interface
	clientErr  error

	backoff wait.Backoff
}

// NewK8sSecretResolver creates a new K8s Secret resolver.
func NewK8sSecretResolver(log *zap.Logger) *K8sSecretResolver {
	return &K8sSecretResolver{
		tokenPath: k8sTokenPath,
		nsPath:    k8sNamespacePath,
		log:       log,
		backoff:   defaultSecretReadBackoff(),
	}
}

// clientset returns the in-cluster clientset, building it once.
func (r *K8sSecretResolver) clientset() (kubernetes.Interface, error) {
	r.clientOnce.Do(func() {
		if r.client != nil {
			return
		}
		cfg, err := rest.InClusterConfig()
		if err != nil {
			r.clientErr = fmt.Errorf("in-cluster kubernetes config: %w", err)
			return
		}
		cfg.Timeout = defaultHTTPTimeout
		r.client, r.clientErr = kubernetes.NewForConfig(cfg)
	})
	return r.client, r.clientErr
}

// ResolveConfig walks through the configuration and resolves any secrets in-place.
//
// Pre-flight: detect a missing projected SA token before walking the config.
//   - Out-of-cluster (no KUBERNETES_SERVICE_HOST): silently no-op so unit
//     tests and local runs that inline all credentials don't need to stub
//     the resolver. The config is returned unchanged; any SecretRef fields
//     left behind would surface later as nil dereferences or DSN errors,
//     which is acceptable because the caller only invokes us in-cluster
//     (see config.Load).
//   - In-cluster (KUBERNETES_SERVICE_HOST set) but token file missing:
//     return an actionable error pointing at the chart toggle that almost
//     certainly caused this. The K8sSecretResolver REQUIRES the projected
//     SA token to call the K8s API, so we cannot recover here.
//
// When there are no SecretRef fields in the config, the walk below is a
// no-op anyway — but we still surface the missing-token error in-cluster
// because it almost always indicates operator drift, not a benign skip.
func (r *K8sSecretResolver) ResolveConfig(ctx context.Context, cfg *Config) error {
	if _, err := os.Stat(r.tokenPath); os.IsNotExist(err) {
		if os.Getenv(DefaultK8sServiceHostEnvKey) == "" {
			// Out-of-cluster: silently skip; caller is responsible for
			// providing inline values.
			return nil
		}
		// In-cluster but no token mounted — almost always the
		// serviceAccount.automount=false footgun. Surface the fix.
		return fmt.Errorf(
			"SA token missing at %s. "+
				"Pod has automountServiceAccountToken: false but config "+
				"contains SecretRef fields. Fix: set "+
				"serviceAccount.automount: true in chart values, or "+
				"replace *_secret fields with inline values, or remove "+
				"SecretRef fields",
			r.tokenPath,
		)
	}

	for _, b := range secretBindings(cfg) {
		value, err := r.resolveSecret(ctx, b.ref)
		if err != nil {
			return fmt.Errorf("%s: %w", b.path, err)
		}
		b.fill(value)
	}
	return nil
}

// secretBinding is one configuration field a Secret fills: its path, as an
// error names it, the reference, and fill, which stores the value and drops
// the reference so nothing downstream reads it again.
type secretBinding struct {
	path string
	ref  *SecretRef
	fill func(value string)
}

// secretBindings is every field of cfg whose Secret is to be read, in a fixed
// order: those that name a Secret, and of the replica and the bootstrap admin
// only while they are on, so a stale reference left behind cannot stop boot.
func secretBindings(cfg *Config) []secretBinding {
	var out []secretBinding
	// bind adds a field held by pointer, set when the pointer is.
	bind := func(path string, ref **SecretRef, value *string) {
		if *ref == nil {
			return
		}
		out = append(out, secretBinding{path, *ref, func(v string) { *value, *ref = v, nil }})
	}
	// bindValue adds a field held by value, set when it names a Secret.
	bindValue := func(path string, ref *SecretRef, value *string) {
		if ref.Name == "" {
			return
		}
		named := *ref
		out = append(out, secretBinding{path, &named, func(v string) { *value, *ref = v, SecretRef{} }})
	}

	pg := &cfg.Datastores.Postgres
	bind("postgres.password_secret", &pg.PasswordSecret, &pg.Password)
	// The migration and reaper roles are separate credentials, each in its
	// own, more tightly held Secret.
	bind("postgres.migrate_password_secret", &pg.MigratePasswordSecret, &pg.MigratePassword)
	bind("postgres.reaper_password_secret", &pg.ReaperPasswordSecret, &pg.ReaperPassword)
	if pg.Replica.Enabled {
		bind("postgres.replica.password_secret", &pg.Replica.PasswordSecret, &pg.Replica.Password)
	}
	if cfg.Bootstrap.Admin.Enabled {
		bind("bootstrap.admin.password_secret", &cfg.Bootstrap.Admin.PasswordSecret, &cfg.Bootstrap.Admin.Password)
	}
	// Validate() refuses a key given both inline and by reference.
	bind("auth.signing_key_secret", &cfg.Auth.SigningKeySecret, &cfg.Auth.SigningKey)
	bind("api_token.hmac_key_secret", &cfg.APIToken.HMACKeySecret, &cfg.APIToken.HMACKey)
	bind("capability.dpop_nonce.key_secret", &cfg.Capability.DPoPNonce.KeySecret, &cfg.Capability.DPoPNonce.Key)
	bind("mcp.dpop_key_secret", &cfg.MCP.DPoPKeySecret, &cfg.MCP.DPoPKey)
	bind("runtime.health_snapshot_token_secret", &cfg.Runtime.HealthSnapshotTokenSecret, &cfg.Runtime.HealthSnapshotToken)
	bindValue("ingest.webhook.shared_secret_ref", &cfg.Ingest.Webhook.SharedSecretRef, &cfg.Ingest.Webhook.SharedSecret)
	bindValue("ingest.nats.token_ref", &cfg.Ingest.NATS.TokenRef, &cfg.Ingest.NATS.Token)
	bindValue("ingest.rabbitmq.url_ref", &cfg.Ingest.RabbitMQ.URLRef, &cfg.Ingest.RabbitMQ.URL)
	bind("mcp.oauth.exchange.client_secret_ref", &cfg.MCP.OAuth.Exchange.ClientSecretRef, &cfg.MCP.OAuth.Exchange.ClientSecret)
	for i := range cfg.Auth.OAuth.SeedClients {
		sc := &cfg.Auth.OAuth.SeedClients[i]
		bind(fmt.Sprintf("auth.oauth.seed_clients[%s].client_secret_ref", sc.ClientID), &sc.ClientSecretRef, &sc.ClientSecret)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Storage.Backends)) {
		out = append(out, backendBindings(cfg.Storage.Backends, name)...)
	}
	return out
}

// backendBindings are a storage backend's credential fields. The map holds
// backends by value, so each fill writes the backend back.
func backendBindings(backends map[string]StorageBackend, name string) []secretBinding {
	var out []secretBinding
	b := backends[name]
	for _, f := range []struct {
		field string
		ref   *SecretRef
		set   func(*StorageBackendAuth, string)
	}{
		{"access_key_secret", b.Auth.AccessKeySecret, func(a *StorageBackendAuth, v string) { a.AccessKey, a.AccessKeySecret = v, nil }},
		{"secret_key_secret", b.Auth.SecretKeySecret, func(a *StorageBackendAuth, v string) { a.SecretKey, a.SecretKeySecret = v, nil }},
		{"session_token_secret", b.Auth.SessionTokenSecret, func(a *StorageBackendAuth, v string) { a.SessionToken, a.SessionTokenSecret = v, nil }},
	} {
		if f.ref == nil {
			continue
		}
		set := f.set
		out = append(out, secretBinding{
			path: fmt.Sprintf("storage.backends.%s.auth.%s", name, f.field),
			ref:  f.ref,
			fill: func(v string) {
				b := backends[name]
				set(&b.Auth, v)
				backends[name] = b
			},
		})
	}
	return out
}

// ResolveSecret reads one key from a K8s Secret at runtime via the pod's
// ServiceAccount — the same mechanism ResolveConfig uses at boot. Exported for
// runtime callers (e.g. the dynamic-backend connectivity probe) that resolve a
// secret outside config load.
func (r *K8sSecretResolver) ResolveSecret(ctx context.Context, ref *SecretRef) (string, error) {
	return r.resolveSecret(ctx, ref)
}

func (r *K8sSecretResolver) resolveSecret(ctx context.Context, ref *SecretRef) (string, error) {
	if ref.Name == "" {
		return "", fmt.Errorf("secret name is required")
	}

	key := ref.Key
	if key == "" {
		key = DefaultSecretKey
	}

	namespace := ref.Namespace
	if namespace == "" {
		nsBytes, err := os.ReadFile(r.nsPath)
		if err != nil {
			return "", fmt.Errorf("failed to read pod namespace (are you in cluster?): %w", err)
		}
		namespace = string(nsBytes)
	}

	client, err := r.clientset()
	if err != nil {
		return "", err
	}
	var secret *corev1.Secret
	attempt := 0
	err = retry.OnError(r.backoff, func(err error) bool { return retryableSecretRead(ctx, err) }, func() error {
		attempt++
		var getErr error
		secret, getErr = client.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if getErr != nil && retryableSecretRead(ctx, getErr) {
			// No secret name: the failure is the API's, not the secret's, and
			// the caller's error names the config field once retries run out.
			r.log.Warn("kubernetes API did not answer a secret read; retrying",
				zap.Int("attempt", attempt), zap.Error(getErr))
		}
		return getErr
	})
	switch {
	case apierrors.IsForbidden(err):
		return "", fmt.Errorf("access denied reading secret %s (ensure RBAC allows get secrets)", ref.Name)
	case apierrors.IsNotFound(err):
		return "", fmt.Errorf("secret %s not found in namespace %s", ref.Name, namespace)
	case err != nil:
		return "", fmt.Errorf("failed to fetch secret: %w", err)
	}

	if val, ok := secret.Data[key]; ok {
		return string(val), nil
	}

	return "", fmt.Errorf("key %q not found in secret", key)
}

// Obfuscated returns a deep copy of the configuration structure with all resolved secret
// values replaced by Redacted. Useful for logging or debugging.
func (c *Config) Obfuscated() Config {
	cc := *c

	// Redact Postgres credentials — every role's password, not just the
	// runtime one. The resolver populates these from *_password_secret at
	// boot, so by the time Obfuscated() runs for the "config loaded" log they
	// hold the real cleartext secret.
	if cc.Datastores.Postgres.Password != "" {
		cc.Datastores.Postgres.Password = Redacted
	}
	if cc.Datastores.Postgres.MigratePassword != "" {
		cc.Datastores.Postgres.MigratePassword = Redacted
	}
	if cc.Datastores.Postgres.ReaperPassword != "" {
		cc.Datastores.Postgres.ReaperPassword = Redacted
	}
	if cc.Datastores.Postgres.Replica.Password != "" {
		cc.Datastores.Postgres.Replica.Password = Redacted
	}

	// Redact per-backend creds. Copy the map so we don't mutate the source.
	if len(cc.Storage.Backends) > 0 {
		redacted := make(map[string]StorageBackend, len(cc.Storage.Backends))
		for name, b := range cc.Storage.Backends {
			if b.Auth.AccessKey != "" {
				b.Auth.AccessKey = Redacted
			}
			if b.Auth.SecretKey != "" {
				b.Auth.SecretKey = Redacted
			}
			if b.Auth.SessionToken != "" {
				b.Auth.SessionToken = Redacted
			}
			redacted[name] = b
		}
		cc.Storage.Backends = redacted
	}

	if cc.Auth.SigningKey != "" {
		cc.Auth.SigningKey = Redacted
	}

	if cc.MCP.OAuth.Exchange.ClientSecret != "" {
		cc.MCP.OAuth.Exchange.ClientSecret = Redacted
	}

	// OAuth seed client secrets. Copy the slice so the source keeps them.
	if len(cc.Auth.OAuth.SeedClients) > 0 {
		clients := slices.Clone(cc.Auth.OAuth.SeedClients)
		for i := range clients {
			if clients[i].ClientSecret != "" {
				clients[i].ClientSecret = Redacted
			}
		}
		cc.Auth.OAuth.SeedClients = clients
	}

	// Bootstrap admin password (resolved from bootstrap.admin.password_secret).
	if cc.Bootstrap.Admin.Password != "" {
		cc.Bootstrap.Admin.Password = Redacted
	}

	// API-token pepper (resolved from api_token.hmac_key_secret). It was
	// missed here, so a deployment that did the right thing — a dedicated key
	// in a Secret instead of one derived from the signing key — printed that
	// key in cleartext in the "config loaded" line at every boot. Knowing it
	// turns a stolen api_tokens dump into forgeable credentials, which is the
	// single property the pepper exists to deny.
	if cc.APIToken.HMACKey != "" {
		cc.APIToken.HMACKey = Redacted
	}

	// DPoP nonce key (resolved from capability.dpop_nonce.key_secret): with
	// it anyone can mint nonces every replica accepts.
	if cc.Capability.DPoPNonce.Key != "" {
		cc.Capability.DPoPNonce.Key = Redacted
	}

	// MCP bridge DPoP key (resolved from mcp.dpop_key_secret): a private key
	// that every capability delegated to the bridge is bound to.
	if cc.MCP.DPoPKey != "" {
		cc.MCP.DPoPKey = Redacted
	}

	// Ingest webhook HMAC (resolved from ingest.webhook.shared_secret_ref).
	if cc.Ingest.Webhook.SharedSecret != "" {
		cc.Ingest.Webhook.SharedSecret = Redacted
	}

	// Ingest broker credentials (resolved from token_ref / url_ref).
	if cc.Ingest.NATS.Token != "" {
		cc.Ingest.NATS.Token = Redacted
	}
	cc.Ingest.RabbitMQ.URL = redactURLPassword(cc.Ingest.RabbitMQ.URL)

	// The /system/health.json gate, shared with the console.
	if cc.Runtime.HealthSnapshotToken != "" {
		cc.Runtime.HealthSnapshotToken = Redacted
	}

	return cc
}

// redactURLPassword replaces the password in a URL's userinfo, keeping the
// host and user an operator debugs with. A URL that does not parse is
// redacted whole: there is no telling where its password is.
func redactURLPassword(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Redacted
	}
	if _, hasPassword := u.User.Password(); !hasPassword {
		return raw
	}
	// Spliced in rather than set through url.UserPassword, which would
	// percent-encode the sentinel into "%2A%2A%2A".
	user := url.User(u.User.Username()).String()
	u.User = nil
	const schemeSep = "://"
	rest := strings.TrimPrefix(u.String(), u.Scheme+schemeSep)
	return u.Scheme + schemeSep + user + ":" + Redacted + "@" + rest
}

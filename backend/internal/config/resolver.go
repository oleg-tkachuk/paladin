package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Redacted is the placeholder used in Obfuscated() for sensitive fields.
const Redacted = "***"

const (
	k8sTokenPath       = "/var/run/secrets/kubernetes.io/serviceaccount/token" // #nosec G101
	k8sNamespacePath   = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	defaultHTTPTimeout = 5 * time.Second
)

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
}

// NewK8sSecretResolver creates a new K8s Secret resolver.
func NewK8sSecretResolver(log *zap.Logger) *K8sSecretResolver {
	return &K8sSecretResolver{
		tokenPath: k8sTokenPath,
		nsPath:    k8sNamespacePath,
		log:       log,
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

	// Postgres
	if cfg.Datastores.Postgres.PasswordSecret != nil {
		pwd, err := r.resolveSecret(ctx, cfg.Datastores.Postgres.PasswordSecret)
		if err != nil {
			return fmt.Errorf("postgres.password_secret: %w", err)
		}
		cfg.Datastores.Postgres.Password = pwd
		cfg.Datastores.Postgres.PasswordSecret = nil
	}
	// Migration role secret is independent — production deploys keep the
	// DDL credential in a separate, more tightly access-controlled secret
	// than the runtime credential.
	if cfg.Datastores.Postgres.MigratePasswordSecret != nil {
		pwd, err := r.resolveSecret(ctx, cfg.Datastores.Postgres.MigratePasswordSecret)
		if err != nil {
			return fmt.Errorf("postgres.migrate_password_secret: %w", err)
		}
		cfg.Datastores.Postgres.MigratePassword = pwd
		cfg.Datastores.Postgres.MigratePasswordSecret = nil
	}
	// Reaper role secret — the least-privilege BYPASSRLS DML credential the
	// worker's background jobs use. Independent of both the runtime and the
	// migrate secret.
	if cfg.Datastores.Postgres.ReaperPasswordSecret != nil {
		pwd, err := r.resolveSecret(ctx, cfg.Datastores.Postgres.ReaperPasswordSecret)
		if err != nil {
			return fmt.Errorf("postgres.reaper_password_secret: %w", err)
		}
		cfg.Datastores.Postgres.ReaperPassword = pwd
		cfg.Datastores.Postgres.ReaperPasswordSecret = nil
	}

	// Bootstrap admin password — only resolved when bootstrap.admin.enabled
	// is true. Skip otherwise so a misconfigured Secret doesn't crash boot
	// in clusters where bootstrap is intentionally off.
	if cfg.Bootstrap.Admin.Enabled && cfg.Bootstrap.Admin.PasswordSecret != nil {
		pwd, err := r.resolveSecret(ctx, cfg.Bootstrap.Admin.PasswordSecret)
		if err != nil {
			return fmt.Errorf("bootstrap.admin.password_secret: %w", err)
		}
		cfg.Bootstrap.Admin.Password = pwd
		cfg.Bootstrap.Admin.PasswordSecret = nil
	}

	// Auth signing key — the HMAC key used to both sign and verify every
	// JWT. Resolving it from a SecretRef lets prod keep the key in a
	// (Sealed)Secret instead of inline in Helm values; Validate() already
	// rejects setting both auth.signing_key and auth.signing_key_secret.
	if cfg.Auth.SigningKeySecret != nil {
		key, err := r.resolveSecret(ctx, cfg.Auth.SigningKeySecret)
		if err != nil {
			return fmt.Errorf("auth.signing_key_secret: %w", err)
		}
		cfg.Auth.SigningKey = key
		cfg.Auth.SigningKeySecret = nil
	}

	// API-token HMAC key — the server-side pepper for the token lookup
	// digest. Resolving it from a SecretRef keeps the key out of inline
	// Helm values; Validate() rejects setting both api_token.hmac_key and
	// api_token.hmac_key_secret.
	if cfg.APIToken.HMACKeySecret != nil {
		key, err := r.resolveSecret(ctx, cfg.APIToken.HMACKeySecret)
		if err != nil {
			return fmt.Errorf("api_token.hmac_key_secret: %w", err)
		}
		cfg.APIToken.HMACKey = key
		cfg.APIToken.HMACKeySecret = nil
	}

	// Ingest webhook HMAC shared secret — the key the storage-event publisher
	// signs bodies with. SharedSecretRef is a value type, so an empty Name is
	// "not set" (the inline shared_secret, or dev's empty-passes-check, is used
	// instead). Resolving it keeps the HMAC key out of inline values too.
	if cfg.Ingest.Webhook.SharedSecretRef.Name != "" {
		sec, err := r.resolveSecret(ctx, &cfg.Ingest.Webhook.SharedSecretRef)
		if err != nil {
			return fmt.Errorf("ingest.webhook.shared_secret_ref: %w", err)
		}
		cfg.Ingest.Webhook.SharedSecret = sec
		cfg.Ingest.Webhook.SharedSecretRef = SecretRef{}
	}

	// Per-backend credential secrets.
	for name, b := range cfg.Storage.Backends {
		if b.Auth.AccessKeySecret != nil {
			key, err := r.resolveSecret(ctx, b.Auth.AccessKeySecret)
			if err != nil {
				return fmt.Errorf("storage.backends.%s.auth.access_key_secret: %w", name, err)
			}
			b.Auth.AccessKey = key
			b.Auth.AccessKeySecret = nil
		}
		if b.Auth.SecretKeySecret != nil {
			key, err := r.resolveSecret(ctx, b.Auth.SecretKeySecret)
			if err != nil {
				return fmt.Errorf("storage.backends.%s.auth.secret_key_secret: %w", name, err)
			}
			b.Auth.SecretKey = key
			b.Auth.SecretKeySecret = nil
		}
		if b.Auth.SessionTokenSecret != nil {
			tok, err := r.resolveSecret(ctx, b.Auth.SessionTokenSecret)
			if err != nil {
				return fmt.Errorf("storage.backends.%s.auth.session_token_secret: %w", name, err)
			}
			b.Auth.SessionToken = tok
			b.Auth.SessionTokenSecret = nil
		}
		cfg.Storage.Backends[name] = b
	}

	return nil
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
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
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

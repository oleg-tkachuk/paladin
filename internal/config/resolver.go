package config

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
)

// Redacted is the placeholder used in Obfuscated() for sensitive fields.
const Redacted = "***"

const (
	defaultK8sAPIBaseURL = "https://kubernetes.default.svc"
	k8sTokenPath         = "/var/run/secrets/kubernetes.io/serviceaccount/token" // #nosec G101
	k8sNamespacePath     = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	k8sCACertPath        = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	defaultHTTPTimeout   = 5 * time.Second
)

// Resolver resolves secret references in the configuration.
type Resolver interface {
	ResolveConfig(ctx context.Context, cfg *Config) error
}

// K8sSecretResolver resolves Kubernetes secrets by reading the projected token
// and calling the Kubernetes API.
type K8sSecretResolver struct {
	log        *zap.Logger
	client     *http.Client
	apiBaseURL string
	tokenPath  string
	nsPath     string
}

// NewK8sSecretResolver creates a new K8s Secret resolver.
// It configures TLS using the projected cluster CA.
func NewK8sSecretResolver(log *zap.Logger) *K8sSecretResolver {
	caCertPool := x509.NewCertPool()
	caCert, err := os.ReadFile(k8sCACertPath)
	if err == nil {
		caCertPool.AppendCertsFromPEM(caCert)
	}

	return &K8sSecretResolver{
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs: caCertPool,
				},
			},
			Timeout: defaultHTTPTimeout,
		},
		apiBaseURL: defaultK8sAPIBaseURL,
		tokenPath:  k8sTokenPath,
		nsPath:     k8sNamespacePath,
		log:        log,
	}
}

// ResolveConfig walks through the configuration and resolves any secrets in-place.
func (r *K8sSecretResolver) ResolveConfig(ctx context.Context, cfg *Config) error {
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

	tokenBytes, err := os.ReadFile(r.tokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read service account token (are you in cluster?): %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/namespaces/%s/secrets/%s", r.apiBaseURL, namespace, ref.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+string(tokenBytes))
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch secret: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			r.log.Error("failed to close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("access denied reading secret %s (ensure RBAC allows get secrets)", ref.Name)
		}
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("secret %s not found in namespace %s", ref.Name, namespace)
		}

		return "", fmt.Errorf("kubernetes API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read secret payload: %w", err)
	}

	var secretResp struct {
		Data       map[string]string `json:"data"`
		StringData map[string]string `json:"stringData"`
	}
	if err := json.Unmarshal(body, &secretResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal secret payload: %w", err)
	}

	if val, ok := secretResp.StringData[key]; ok {
		return val, nil
	}

	if b64Val, ok := secretResp.Data[key]; ok {
		decoded, err := base64.StdEncoding.DecodeString(b64Val)
		if err != nil {
			return "", fmt.Errorf("secret key %s contains invalid base64 data: %w", key, err)
		}

		return string(decoded), nil
	}

	return "", fmt.Errorf("key %q not found in secret", key)
}

// Obfuscated returns a deep copy of the configuration structure with all resolved secret
// values replaced by Redacted. Useful for logging or debugging.
func (c *Config) Obfuscated() Config {
	cc := *c

	// Redact Postgres
	if cc.Datastores.Postgres.Password != "" {
		cc.Datastores.Postgres.Password = Redacted
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

	return cc
}

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

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"go.uber.org/zap"
)

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

	// S3 Access Key
	if cfg.Datastores.S3.AccessKeySecret != nil {
		key, err := r.resolveSecret(ctx, cfg.Datastores.S3.AccessKeySecret)
		if err != nil {
			return fmt.Errorf("s3.access_key_secret: %w", err)
		}
		cfg.Datastores.S3.AccessKey = key
		cfg.Datastores.S3.AccessKeySecret = nil
	}

	// S3 Secret Key
	if cfg.Datastores.S3.SecretKeySecret != nil {
		key, err := r.resolveSecret(ctx, cfg.Datastores.S3.SecretKeySecret)
		if err != nil {
			return fmt.Errorf("s3.secret_key_secret: %w", err)
		}
		cfg.Datastores.S3.SecretKey = key
		cfg.Datastores.S3.SecretKeySecret = nil
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
			r.log.Error("close response body failed", zap.Error(err))
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
// values replaced by domain.Redacted. Useful for logging or debugging.
func (c *Config) Obfuscated() Config {
	cc := *c

	// Redact Postgres
	if cc.Datastores.Postgres.Password != "" {
		cc.Datastores.Postgres.Password = domain.Redacted
	}

	// Redact S3
	if cc.Datastores.S3.AccessKey != "" {
		cc.Datastores.S3.AccessKey = domain.Redacted
	}
	if cc.Datastores.S3.SecretKey != "" {
		cc.Datastores.S3.SecretKey = domain.Redacted
	}

	return cc
}

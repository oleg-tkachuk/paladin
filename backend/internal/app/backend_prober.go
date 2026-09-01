package app

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
)

// secretResolver reads a single key from a K8s Secret at runtime. Satisfied by
// *config.K8sSecretResolver; an interface so the prober is unit-testable.
type secretResolver interface {
	ResolveSecret(ctx context.Context, ref *config.SecretRef) (string, error)
}

// s3BackendProber satisfies backendh.BackendProber. It holds one S3 client per
// runtime-configured backend (keyed by backend_id == the storage.backends.<id>
// config key — see bootstrap.EnsureBackends) and runs a read-only ListBuckets
// reachability check on TestBackend.
//
// Config-declared backends are probed via their pre-built client. A backend
// created purely via CreateBackend (its credentials in a K8s Secret the process
// never loaded) is probed DYNAMICALLY: its credentials_secret_ref is resolved
// from K8s at probe time, an ephemeral client is built, and that is probed.
// Both paths surface unreachable as a probe error, never a false reachable.
type s3BackendProber struct {
	clients  map[string]*s3adapter.Client
	resolver secretResolver // nil → dynamic backends can't be probed
}

// BuildBackendProber constructs a prober over every configured storage backend
// plus a runtime K8s secret resolver for dynamic ones. Returns an error only if
// a configured backend is structurally invalid (bad endpoint / auth mode);
// connectivity is checked per probe.
func BuildBackendProber(ctx context.Context, storage config.Storage, l *zap.Logger) (*s3BackendProber, error) {
	clients := make(map[string]*s3adapter.Client, len(storage.Backends))
	for id, b := range storage.Backends {
		c, err := s3adapter.New(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("backend prober: build client for %q: %w", id, err)
		}
		c.SetBackendID(id)
		clients[id] = c
	}
	return &s3BackendProber{
		clients:  clients,
		resolver: config.NewK8sSecretResolver(l.Named("backend-secret-resolver")),
	}, nil
}

// Probe runs the reachability check for a backend row. Config-declared backends
// use their pre-built client; everything else is resolved dynamically.
func (p *s3BackendProber) Probe(ctx context.Context, b admindomain.StorageBackend) error {
	if c, ok := p.clients[b.BackendID]; ok {
		return c.Probe(ctx)
	}
	return p.probeDynamic(ctx, b)
}

// probeDynamic resolves a dynamic backend's credentials from K8s, builds an
// ephemeral S3 client, and probes it. The v1 credentials_secret_ref contract:
// a K8s secret reference ("name" in the pod namespace, or "namespace/name")
// whose data carries the keys access_key_id + secret_access_key. Scheme-
// prefixed refs (vault://, csi://) are not supported yet and return a clear
// error rather than a misleading "unreachable".
func (p *s3BackendProber) probeDynamic(ctx context.Context, b admindomain.StorageBackend) error {
	if p.resolver == nil {
		return fmt.Errorf("backend %q is not in the runtime config and no secret resolver is wired", b.BackendID)
	}
	ns, name, ok := parseDynamicCredsRef(b.CredentialsSecretRef)
	if !ok {
		return fmt.Errorf("backend %q: credentials_secret_ref %q is not a supported K8s secret reference "+
			`(expected "name" or "namespace/name")`, b.BackendID, b.CredentialsSecretRef)
	}
	accessKey, err := p.resolver.ResolveSecret(ctx, &config.SecretRef{Namespace: ns, Name: name, Key: "access_key_id"})
	if err != nil {
		return fmt.Errorf("resolve access_key_id from secret %q: %w", name, err)
	}
	secretKey, err := p.resolver.ResolveSecret(ctx, &config.SecretRef{Namespace: ns, Name: name, Key: "secret_access_key"})
	if err != nil {
		return fmt.Errorf("resolve secret_access_key from secret %q: %w", name, err)
	}
	client, err := s3adapter.New(ctx, config.StorageBackend{
		Kind:           b.Kind,
		Region:         b.Region,
		Endpoint:       b.Endpoint,
		PublicEndpoint: b.PublicEndpoint,
		ForcePathStyle: b.ForcePathStyle,
		Auth: config.StorageBackendAuth{
			Mode:      config.AuthModeStaticKeys,
			AccessKey: accessKey,
			SecretKey: secretKey,
		},
	})
	if err != nil {
		return fmt.Errorf("build ephemeral client for %q: %w", b.BackendID, err)
	}
	return client.Probe(ctx)
}

// parseDynamicCredsRef splits a credentials_secret_ref into (namespace, name).
// "namespace/name" → both; "name" → name in the pod namespace. A scheme-
// prefixed ref (contains "://") or an empty ref is unsupported (ok=false).
func parseDynamicCredsRef(ref string) (namespace, name string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "://") {
		return "", "", false
	}
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		return ref[:i], ref[i+1:], i+1 < len(ref)
	}
	return "", ref, true
}

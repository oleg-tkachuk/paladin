package s3adapter

import (
	"context"
	"fmt"
	"sync"

	"github.com/oleg-tkachuk/paladin-private/internal/config"
)

// BackendRegistry hands out one *Client per storage backend, keyed by
// backend id, building each lazily from config.Storage.Backends. It is the
// multi-backend replacement for the single per-process client: a bucket's
// physical location is (backend_id, bucket), and this is the "backend_id →
// client" half.
//
// Connection/auth params come from config (the authoritative source for how
// to reach a backend); the operational flags enabled/read_only/maintenance
// live in the DB and are enforced by the bucket resolver, NOT here — the
// registry is purely "give me a client for backend X" and must still build a
// client for a disabled backend so a drain/migration can read off it.
//
// Same pooled-resource shape as worker.SQSClientPool / RabbitMQConnPool:
// lazy build under a mutex, cached for the process lifetime. S3 clients are
// stateless HTTP, so there is nothing to Close.
type BackendRegistry struct {
	mu      sync.Mutex
	clients map[string]*Client
	cfg     config.Storage
	// build is New, overridable in tests so the registry hands back a fake
	// client without touching AWS credential resolution.
	build func(ctx context.Context, b config.StorageBackend) (*Client, error)
}

// NewBackendRegistry returns an empty registry whose clients build on first
// use per backend id. Call Warmup to eagerly build the default (and, with
// all=true, every configured backend) at boot.
func NewBackendRegistry(cfg config.Storage) *BackendRegistry {
	return &BackendRegistry{
		clients: map[string]*Client{},
		cfg:     cfg,
		build:   New,
	}
}

// For returns the cached client for backendID, building it lazily from
// cfg.Backends[backendID]. The id is REQUIRED — there is no implicit default:
// an empty id is a hard error, never a silent fallback that could place a
// tenant's bytes on an arbitrary store. An unknown id is likewise a hard error.
func (r *BackendRegistry) For(ctx context.Context, backendID string) (*Client, error) {
	if backendID == "" {
		return nil, fmt.Errorf("s3 registry: backend id required (no default backend)")
	}
	id := backendID
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[id]; ok {
		return c, nil
	}
	bcfg, ok := r.cfg.Backends[id]
	if !ok {
		return nil, fmt.Errorf("s3 registry: unknown storage backend %q", id)
	}
	c, err := r.build(ctx, bcfg)
	if err != nil {
		return nil, fmt.Errorf("s3 registry: build backend %q: %w", id, err)
	}
	r.clients[id] = c
	return c, nil
}

// Warmup eagerly builds every configured backend so a boot misconfiguration
// fails loudly and the request path never pays first-build latency. At least
// one backend must be configured. Returns the first build error.
func (r *BackendRegistry) Warmup(ctx context.Context) error {
	if len(r.cfg.Backends) == 0 {
		return fmt.Errorf("s3 registry: no storage backends configured")
	}
	for id := range r.cfg.Backends {
		if _, err := r.For(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Invalidate drops the cached client for backendID so the next For rebuilds
// it — the hook a credential-rotation driver calls after a backend's
// credentials-ref changes. A no-op if nothing is cached.
func (r *BackendRegistry) Invalidate(backendID string) {
	r.mu.Lock()
	delete(r.clients, backendID)
	r.mu.Unlock()
}

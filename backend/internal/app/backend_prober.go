package app

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
)

// s3BackendProber satisfies backendh.BackendProber. It holds one S3 client
// per runtime-configured backend (keyed by backend_id, which == the
// storage.backends.<id> config key — see bootstrap.EnsureBackends), and runs
// a read-only ListBuckets reachability check on TestBackend.
//
// Clients are built once at startup (s3adapter.New does no network I/O — it
// only assembles config + credential providers), so an unreachable backend
// doesn't fail construction; the failure surfaces on the probe call instead.
//
// Dynamically-created backends (a CreateBackend RPC writing a row with a
// credentials_ref not present in the runtime config) have no client here —
// their credentials live in a Secret the running process never loaded. They
// probe as a clear "not in runtime config" error rather than a false
// reachable=true.
type s3BackendProber struct {
	clients map[string]*s3adapter.Client
}

// BuildBackendProber constructs a prober over every configured storage
// backend. Returns an error only if a backend's *config* is structurally
// invalid (bad endpoint, unknown auth mode) — connectivity is checked later,
// per probe.
func BuildBackendProber(ctx context.Context, storage config.Storage) (*s3BackendProber, error) {
	clients := make(map[string]*s3adapter.Client, len(storage.Backends))
	for id, b := range storage.Backends {
		c, err := s3adapter.New(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("backend prober: build client for %q: %w", id, err)
		}
		clients[id] = c
	}
	return &s3BackendProber{clients: clients}, nil
}

func (p *s3BackendProber) Probe(ctx context.Context, backendID string) error {
	c, ok := p.clients[backendID]
	if !ok {
		return fmt.Errorf("backend %q is not in the runtime storage config; "+
			"dynamically-registered backends can't be probed (their credentials "+
			"aren't loaded by this process)", backendID)
	}
	return c.Probe(ctx)
}

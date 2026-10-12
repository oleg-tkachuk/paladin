package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1admindomain "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

// fakeBackends is an in-memory BackendStore.
type fakeBackends struct {
	rows    map[string]v1admindomain.StorageBackend
	upserts int
	health  map[string]string
}

func (f *fakeBackends) Upsert(_ context.Context, b v1admindomain.StorageBackend) error {
	if f.rows == nil {
		f.rows = map[string]v1admindomain.StorageBackend{}
	}
	// Like the real upsert, the recorded features are not the config's.
	b.Features = f.rows[b.BackendID].Features
	f.rows[b.BackendID] = b
	f.upserts++
	return nil
}

func (f *fakeBackends) Get(_ context.Context, id string) (v1admindomain.StorageBackend, error) {
	b, ok := f.rows[id]
	if !ok {
		return v1admindomain.StorageBackend{}, v1admindomain.ErrNotFound
	}
	return b, nil
}

func (f *fakeBackends) SetHealth(_ context.Context, id, status, _ string, _ time.Time) error {
	if f.health == nil {
		f.health = map[string]string{}
	}
	f.health[id] = status
	return nil
}

func (f *fakeBackends) SetFeatures(_ context.Context, id string, results []features.Result) error {
	b := f.rows[id]
	b.Features = results
	f.rows[id] = b
	return nil
}

// fakeProber records which backends it probed; unreachable ones fail the
// reachability check.
type fakeProber struct {
	probed      []string
	unreachable map[string]bool
}

func (p *fakeProber) Probe(_ context.Context, b v1admindomain.StorageBackend) error {
	p.probed = append(p.probed, b.BackendID)
	if p.unreachable[b.BackendID] {
		return errors.New("dial tcp: connection refused")
	}
	return nil
}

func (p *fakeProber) ProbeFeatures(context.Context, v1admindomain.StorageBackend) ([]features.Result, error) {
	return features.EveryFeature(nil), nil
}

func storageWith(ids ...string) config.Storage {
	s := config.Storage{Backends: map[string]config.StorageBackend{}}
	for _, id := range ids {
		s.Backends[id] = config.StorageBackend{Kind: "s3-compatible", Endpoint: "http://" + id, Region: "us-east-1"}
	}
	return s
}

// A backend registered from config is probed as it is registered (ADR-0026),
// so it does not wait for an operator's TestBackend.
func TestEnsureBackends_ProbesANewBackend(t *testing.T) {
	store := &fakeBackends{}
	pr := &fakeProber{}

	require.NoError(t, EnsureBackends(context.Background(), storageWith("primary"),
		BackendDeps{Backends: store, Prober: pr}))

	require.Equal(t, []string{"primary"}, pr.probed)
	require.Len(t, store.rows["primary"].Features, len(features.Catalog))
	require.Equal(t, "ok", store.health["primary"])
}

// A backend already registered but never probed — say, mirrored before the
// probe existed — is probed; one that has results is not probed again: that
// would be the periodic re-probe the ADR rejects.
func TestEnsureBackends_ProbesOnlyTheNeverProbed(t *testing.T) {
	cfg := storageWith("probed", "unprobed", "drifted")
	store := &fakeBackends{rows: map[string]v1admindomain.StorageBackend{}}
	for id, b := range cfg.Backends {
		store.rows[id] = domainBackendFromYAML(id, b)
	}
	store.rows["probed"] = withFeatures(store.rows["probed"])
	// A config change re-upserts the row but keeps its results.
	drifted := withFeatures(store.rows["drifted"])
	drifted.Endpoint = "http://old"
	store.rows["drifted"] = drifted
	pr := &fakeProber{}

	require.NoError(t, EnsureBackends(context.Background(), cfg, BackendDeps{Backends: store, Prober: pr}))

	require.Equal(t, []string{"unprobed"}, pr.probed)
	require.Equal(t, 1, store.upserts, "the drifted backend is still mirrored")
}

// An unreachable backend does not fail the bootstrap: it is registered, its
// features stay unknown, and the next boot probes it again.
func TestEnsureBackends_UnreachableBackendStillRegisters(t *testing.T) {
	store := &fakeBackends{}
	pr := &fakeProber{unreachable: map[string]bool{"primary": true}}
	deps := BackendDeps{Backends: store, Prober: pr}

	require.NoError(t, EnsureBackends(context.Background(), storageWith("primary"), deps))
	require.Contains(t, store.rows, "primary")
	require.Empty(t, store.rows["primary"].Features)
	require.Equal(t, "error", store.health["primary"])

	require.NoError(t, EnsureBackends(context.Background(), storageWith("primary"), deps))
	require.Equal(t, []string{"primary", "primary"}, pr.probed)
}

// Without a prober the mirror works as before.
func TestEnsureBackends_NoProberSkipsTheProbe(t *testing.T) {
	store := &fakeBackends{}
	require.NoError(t, EnsureBackends(context.Background(), storageWith("primary"), BackendDeps{Backends: store}))
	require.Contains(t, store.rows, "primary")
	require.Empty(t, store.rows["primary"].Features)
}

func withFeatures(b v1admindomain.StorageBackend) v1admindomain.StorageBackend {
	b.Features = []features.Result{{Feature: features.ConditionalPut, Support: features.Supported}}
	return b
}

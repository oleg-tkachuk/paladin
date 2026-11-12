package app

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// pepperBytes is a pepper long enough for the API-token hasher.
const pepperBytes = 32

// unconnectedDeps is shared dependencies over a pool that never connects,
// in env: enough for the bundles' constructors, which open nothing.
func unconnectedDeps(env string) *SharedDeps {
	return &SharedDeps{
		Cfg:    config.Config{App: config.App{Env: env}},
		Logger: zap.NewNop(),
		Pool:   &pgxpool.Pool{},
	}
}

// A role without token auth builds the API-token store and reads no pepper:
// with none configured it still builds, and holds nothing that would hash.
func TestAPITokenBundle_WithoutTokenAuthBuildsTheStoreAlone(t *testing.T) {
	cfg := config.APIToken{Enabled: true}

	got, err := BuildAPITokenBundle(cfg, unconnectedDeps("prod"), false)
	if err != nil {
		t.Fatalf("without token auth and without a pepper: %v", err)
	}
	if got.Store == nil {
		t.Error("the purger's store was not built")
	}
	if got.Auth != nil {
		t.Error("a role without token auth holds an issuer and a verifier")
	}

	// The same configuration with token auth needs the pepper.
	if _, err := BuildAPITokenBundle(cfg, unconnectedDeps("prod"), true); err == nil {
		t.Error("token auth built without a pepper")
	}
	cfg.HMACKey = string(make([]byte, pepperBytes))
	got, err = BuildAPITokenBundle(cfg, unconnectedDeps("prod"), true)
	if err != nil {
		t.Fatalf("token auth with a pepper: %v", err)
	}
	if got.Auth == nil || got.Auth.Issuer == nil || got.Auth.Verifier == nil {
		t.Errorf("token auth built no issuer or verifier: %+v", got.Auth)
	}
}

// A role without token auth builds the capability stores the purger needs
// and loads no signing key and no DPoP nonce key: in prod, with neither
// configured, it still builds.
func TestCapabilityBundle_WithoutTokenAuthBuildsTheStoresAlone(t *testing.T) {
	cfg := capCfg()
	cfg.DPoPNonce = config.DPoPNonce{Enabled: true}

	got, err := BuildCapabilityBundle(cfg, unconnectedDeps("prod"), false)
	if err != nil {
		t.Fatalf("without token auth, signing key or nonce key: %v", err)
	}
	if got.Store == nil || got.Usage == nil || got.Replay == nil || got.Ledger == nil {
		t.Errorf("the purger's stores were not all built: %+v", got)
	}
	if got.Auth != nil {
		t.Error("a role without token auth holds a verifier and issuers")
	}
}

// A plane refuses shared dependencies holding an enabled token subsystem
// without what verifies its tokens: it would serve unauthenticated.
func TestRequireTokenAuth(t *testing.T) {
	cases := map[string]struct {
		deps *SharedDeps
		want error
	}{
		"both subsystems off":    {&SharedDeps{}, nil},
		"both with auth":         {&SharedDeps{Capability: &CapabilityBundle{Auth: &CapabilityAuth{}}, APIToken: &APITokenBundle{Auth: &APITokenAuth{}}}, nil},
		"capability stores only": {&SharedDeps{Capability: &CapabilityBundle{}}, errPlaneWithoutTokenAuth},
		"api token store only":   {&SharedDeps{APIToken: &APITokenBundle{}}, errPlaneWithoutTokenAuth},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := requireTokenAuth(tc.deps); !errors.Is(err, tc.want) {
				t.Errorf("requireTokenAuth = %v, want %v", err, tc.want)
			}
		})
	}
	if _, _, err := AssembleAdminMux(context.Background(), &SharedDeps{APIToken: &APITokenBundle{}}, BuildMeta{}); !errors.Is(err, errPlaneWithoutTokenAuth) {
		t.Errorf("AssembleAdminMux = %v, want %v", err, errPlaneWithoutTokenAuth)
	}
	if _, _, err := AssembleIAMMux(context.Background(), &SharedDeps{Capability: &CapabilityBundle{}}, BuildMeta{}); !errors.Is(err, errPlaneWithoutTokenAuth) {
		t.Errorf("AssembleIAMMux = %v, want %v", err, errPlaneWithoutTokenAuth)
	}
}

// A role that builds no storage reads no backend credentials and warms no
// client, however the backends are configured; one that builds it refuses
// a configuration with no backend.
func TestStorageRegistry(t *testing.T) {
	broken := config.Storage{Backends: map[string]config.StorageBackend{
		"primary": {Kind: "no-such-kind"},
	}}
	if _, err := storageRegistry(context.Background(), broken, false, zap.NewNop()); err != nil {
		t.Errorf("without storage, a backend was built: %v", err)
	}
	if _, err := storageRegistry(context.Background(), broken, true, zap.NewNop()); err == nil {
		t.Error("with storage, a broken backend was accepted")
	}
	if _, err := storageRegistry(context.Background(), config.Storage{}, true, zap.NewNop()); err == nil {
		t.Error("with storage, no backend at all was accepted")
	}
}

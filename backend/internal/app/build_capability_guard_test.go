package app

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/config"
)

// The ephemeral-key guard.
//
// Running the capability issuer on a generated key means every pod restart
// silently invalidates every capability agents are still holding. The startup
// warning that used to be the only defence is the wrong mechanism — nobody
// reads pod logs on a green rollout — so anything outside a disposable
// environment now refuses to start.
//
// These tests pin the allow-list direction specifically: an UNKNOWN
// environment must fail, not pass. A deny-list would silently ship ephemeral
// keys to every environment nobody thought to name.

func capCfg() config.Capability {
	return config.Capability{
		Enabled:        true,
		IssuerName:     "test-issuer",
		SigningKeyPath: "", // ← the condition under test
	}
}

func depsForEnv(env string) *SharedDeps {
	return &SharedDeps{
		Cfg:    config.Config{App: config.App{Env: env}},
		Logger: zap.NewNop(),
	}
}

func TestEphemeralKeyRefusedOutsideDisposableEnvs(t *testing.T) {
	for _, env := range []string{"prod", "production", "staging", "do", ""} {
		t.Run(env, func(t *testing.T) {
			_, err := BuildCapabilityBundle(capCfg(), depsForEnv(env))
			if err == nil {
				t.Fatalf("env %q: ephemeral signing key was accepted — a restart would "+
					"invalidate every live capability", env)
			}
			// The message has to name the fix, not just the fault: whoever hits
			// this is mid-deploy and needs to know what to do.
			if !strings.Contains(err.Error(), "signing_key_path") {
				t.Errorf("error should name the missing setting, got: %v", err)
			}
		})
	}
}

// An environment nobody has classified must fail closed. This is the case a
// deny-list would get wrong, and the reason the guard is written as an
// allow-list.
func TestUnknownEnvFailsClosed(t *testing.T) {
	_, err := BuildCapabilityBundle(capCfg(), depsForEnv("some-env-invented-next-year"))
	if err == nil {
		t.Fatal("an unclassified environment must refuse to start, not default to ephemeral keys")
	}
}

// The disposable environments keep working — the guard must not break local
// development, which is the only reason ephemeral keys exist at all.
func TestEphemeralKeyAllowedInDisposableEnvs(t *testing.T) {
	for env := range ephemeralKeyEnvs {
		t.Run(env, func(t *testing.T) {
			_, err := BuildCapabilityBundle(capCfg(), depsForEnv(env))
			// Wiring fails later on the nil Postgres pool; what matters is that
			// it got PAST the key guard rather than being rejected by it.
			if err != nil && strings.Contains(err.Error(), "signing_key_path") {
				t.Fatalf("env %q must tolerate an ephemeral key, got: %v", env, err)
			}
		})
	}
}

// A configured key path is acceptable everywhere — the guard keys on the
// absence of a key, not on the environment alone.
func TestConfiguredKeyPathPassesTheGuardInProd(t *testing.T) {
	cfg := capCfg()
	cfg.SigningKeyPath = "/etc/paladin/capability.pem"

	_, err := BuildCapabilityBundle(cfg, depsForEnv("prod"))
	if err != nil && strings.Contains(err.Error(), "signing_key_path") {
		t.Fatalf("a configured key path must satisfy the guard in prod, got: %v", err)
	}
}

// Disabled means disabled: the guard must not fire for operators who never
// turned the subsystem on.
func TestDisabledCapabilityIsUnaffected(t *testing.T) {
	cfg := capCfg()
	cfg.Enabled = false

	bundle, err := BuildCapabilityBundle(cfg, depsForEnv("prod"))
	if err != nil {
		t.Fatalf("disabled capability must not error: %v", err)
	}
	if bundle != nil {
		t.Error("disabled capability must return a nil bundle")
	}
}

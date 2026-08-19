package config

import (
	"fmt"
	"sort"
	"strings"
)

// Weak-secret gate.
//
// Every credential this repository ships as a working default is, by
// definition, public: the values live in backend/configs/*.yaml and in the
// Helm values files, and the repository is open source. A deployment that
// boots on one of them is not "insecurely configured" — it is
// *unauthenticated*, because the JWT signing key that mints platform.admin
// tokens is printed in a file anyone can read.
//
// The gate is deliberately shaped like the capability-issuer gate in
// internal/app/build_capability.go (`ephemeralKeyEnvs`): environments where a
// throwaway credential is acceptable are ALLOW-listed, so a new environment
// name defaults to refusing to start rather than to shipping the committed
// default. Guessing wrong is asymmetric — a laptop that fails loudly costs a
// minute, a staging cluster reachable with a published signing key costs
// rather more.
//
// Two independent detectors, because neither alone is enough:
//
//   - exact values — every credential literal actually committed to this
//     repository. Precise, zero false positives, but only covers what we
//     already know about.
//   - markers — substrings operators reach for when they mean "fill this in
//     later" (`change-me`, `<placeholder>`, `not-a-secret`). Catches values
//     this repository has never seen, including the ones a user invents while
//     copying a values file.
//
// NOT an entropy check. Rejecting low-entropy strings would fail a
// legitimately random secret often enough to teach operators to work around
// the gate, and the failure this guard exists to prevent is a *known public
// value*, not a merely short one. Minimum lengths are enforced separately by
// the fields that need them.

// disposableEnvs are the app.env values where booting on a committed default
// credential is acceptable — throwaway environments with nothing to reach.
// Kept in sync by hand with ephemeralKeyEnvs in internal/app; the two guard
// different fields but answer the same question. An empty app.env is
// included: unset means "someone ran the binary against configs/config.yaml
// without an overlay", which is a laptop, not a cluster.
var disposableEnvs = map[string]bool{
	"":            true,
	"local":       true,
	"dev":         true,
	"development": true,
	"test":        true,
	"ci":          true,
	"e2e":         true,
}

// weakSecretValues are exact credential literals committed to this repository.
// Compared case-insensitively against the whole field value.
//
// When you add a default credential to a config or chart values file, add it
// here in the same commit — that is the entire contract that keeps this guard
// honest. TestWeakSecretsCoverCommittedDefaults enforces it for the config
// files it can read.
var weakSecretValues = map[string]bool{
	"dev-secret-change-me-32-bytes-min": true,
	"admin-dev-password-change-me":      true,
	"smoke-admin-password-change-me":    true,
	"e2e-not-a-secret-2026":             true,
	"paladin":                               true, // compose Postgres password
	"minioadmin":                        true,
	"paladin-e2e-access":                    true,
	"paladin-e2e-secret-key":                true,
}

// weakSecretMarkers are substrings that mark a value as a fill-me-in
// placeholder rather than a credential. Matched case-insensitively anywhere
// in the value.
var weakSecretMarkers = []string{
	"change-me",
	"changeme",
	"not-a-secret",
	"dummy",
	"notasecret",
	"placeholder",
	"replace-me",
	"replaceme",
	"insert-",
	"your-secret",
	"xxxxx",
}

// isWeakSecret reports whether v is a known-public or placeholder credential.
// The empty string is NOT weak — "unset" is a different failure, owned by the
// required-field checks, and reporting it here would produce two errors for
// one mistake.
func isWeakSecret(v string) bool {
	if v == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(v))
	if weakSecretValues[lower] {
		return true
	}
	// `<staging-signing-key>`, `<prod-health-token-from-sealed-secret>`,
	// `<oidc-client-id>` — the angle brackets ARE the convention for "an
	// operator fills this in", used throughout the chart values files. One
	// shape check beats chasing each new bracket text with a marker.
	if strings.HasPrefix(lower, "<") && strings.HasSuffix(lower, ">") {
		return true
	}
	for _, marker := range weakSecretMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// validateNoWeakSecrets rejects committed-default and placeholder credentials
// outside the disposable environments. Every offending field is reported at
// once: an operator promoting a values file usually has several, and fixing
// them one boot-failure at a time is a miserable loop.
func validateNoWeakSecrets(c *Config) error {
	if disposableEnvs[strings.ToLower(strings.TrimSpace(c.App.Env))] {
		return nil
	}

	// Field path -> value. Only inline values are checked; a *Secret sibling
	// resolves from Kubernetes at boot and is never a committed literal.
	candidates := map[string]string{
		"auth.signing_key":                     c.Auth.SigningKey,
		"api_token.hmac_key":                   c.APIToken.HMACKey,
		"datastores.postgres.password":         c.Datastores.Postgres.Password,
		"datastores.postgres.migrate_password": c.Datastores.Postgres.MigratePassword,
		"datastores.postgres.reaper_password":  c.Datastores.Postgres.ReaperPassword,
		"runtime.health_snapshot_token":        c.Runtime.HealthSnapshotToken,
		"ingest.webhook.shared_secret":         c.Ingest.Webhook.SharedSecret,
	}
	if c.Bootstrap.Admin.Enabled {
		candidates["bootstrap.admin.password"] = c.Bootstrap.Admin.Password
	}
	for name, b := range c.Storage.Backends {
		candidates[fmt.Sprintf("storage.backends.%s.auth.secret_key", name)] = b.Auth.SecretKey
		candidates[fmt.Sprintf("storage.backends.%s.auth.session_token", name)] = b.Auth.SessionToken
	}

	var offenders []string
	for path, value := range candidates {
		if isWeakSecret(value) {
			offenders = append(offenders, path)
		}
	}
	if len(offenders) == 0 {
		return nil
	}
	sort.Strings(offenders)

	return fmt.Errorf(
		"weak or placeholder credential(s) in app.env=%q — these values are "+
			"published in this repository, so a deployment using one is "+
			"effectively unauthenticated:\n  %s\n"+
			"Set a real secret (or the `_secret` SecretRef sibling) for each, "+
			"or set app.env to a disposable environment (%s) if this really is "+
			"a throwaway stack.",
		c.App.Env,
		strings.Join(offenders, "\n  "),
		strings.Join(sortedDisposableEnvs(), ", "))
}

// sortedDisposableEnvs renders disposableEnvs for the error message, with the
// empty key spelled out rather than printed as nothing.
func sortedDisposableEnvs() []string {
	out := make([]string, 0, len(disposableEnvs))
	for env := range disposableEnvs {
		if env == "" {
			out = append(out, `""`)
			continue
		}
		out = append(out, env)
	}
	sort.Strings(out)
	return out
}

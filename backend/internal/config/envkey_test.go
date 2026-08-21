package config

import (
	"strings"
	"testing"
)

// The mapping from PALADIN_* to a config path is the difference between a
// setting taking effect and being silently discarded, so it is pinned per
// shape rather than by example.
func TestEnvKeyMapper(t *testing.T) {
	m := EnvKeyMapper(KnownConfigPaths())

	for _, tc := range []struct {
		name string
		env  string
		want string
	}{
		{
			// The regression: a blind `_` → `.` produced
			// storage.backends.primary.public.endpoint, which matches no
			// field, so the variable was ignored and the file's value
			// stood. Presigned URLs then pointed at a host the caller
			// could not reach.
			name: "underscore inside a field name",
			env:  "PALADIN_STORAGE_BACKENDS_PRIMARY_PUBLIC_ENDPOINT",
			want: "storage.backends.primary.public_endpoint",
		},
		{
			name: "single-word field under a map key",
			env:  "PALADIN_STORAGE_BACKENDS_PRIMARY_BUCKET",
			want: "storage.backends.primary.bucket",
		},
		{
			name: "nested struct, underscored leaf",
			env:  "PALADIN_STORAGE_BACKENDS_PRIMARY_AUTH_ACCESS_KEY",
			want: "storage.backends.primary.auth.access_key",
		},
		{
			name: "plain nested path",
			env:  "PALADIN_DATASTORES_POSTGRES_DSN",
			want: "datastores.postgres.dsn",
		},
		{
			name: "underscored field two levels down",
			env:  "PALADIN_AUTH_ACCESS_TOKEN_TTL",
			want: "auth.access_token_ttl",
		},
		{
			// A map key may itself contain an underscore. It cannot be
			// told apart from a field name in general, but the field
			// lookup fails first, so it lands as a dynamic key.
			name: "unknown leaf stays joined so strict-check reports it",
			env:  "PALADIN_AUTH_NO_SUCH_SETTING",
			want: "auth.no_such_setting",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := m(tc.env); got != tc.want {
				t.Errorf("%s\n got %q\nwant %q", tc.env, got, tc.want)
			}
		})
	}
}

// Every path the schema knows must be reachable from some environment
// variable. A field that cannot be set from the environment is a field an
// operator cannot configure in Kubernetes, which is where this runs.
//
// One class is genuinely unreachable and is excluded rather than papered
// over: a dynamic map whose KEYS contain dots. `otel.resource` is keyed by
// OpenTelemetry attribute names — `service.name`, `deployment.environment` —
// and an environment variable cannot distinguish that dot from the one
// separating path segments. Those attributes are set in YAML. Naming this
// here means the next person meets a documented limit instead of a puzzle.
func TestEveryLeafIsAddressableFromEnv(t *testing.T) {
	known := KnownConfigPaths()
	m := EnvKeyMapper(known)
	for path := range known {
		if containsWildcard(path) {
			continue // dynamic map keys are addressed by their instance name
		}
		if strings.HasPrefix(path, "otel.resource.") {
			continue // see the note above: dotted attribute names
		}
		env := EnvPrefix + upperUnderscore(path)
		if got := m(env); got != path {
			t.Errorf("path %q is addressed by %s, which maps back to %q", path, env, got)
		}
	}
}

func containsWildcard(s string) bool {
	for _, r := range s {
		if r == '*' {
			return true
		}
	}
	return false
}

func upperUnderscore(path string) string {
	out := make([]rune, 0, len(path))
	for _, r := range path {
		switch {
		case r == '.':
			out = append(out, '_')
		case r >= 'a' && r <= 'z':
			out = append(out, r-32)
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

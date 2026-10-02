package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"
	cueyaml "cuelang.org/go/encoding/yaml"
	"go.uber.org/zap"
	goyaml "gopkg.in/yaml.v3"
)

// Config drift guards.
//
// The config surface lives in five places that must agree, and nothing
// but convention kept them in step:
//
//	internal/config/types.go        the Go struct — source of truth for
//	                                strict-key validation at load time
//	internal/config/schema.cue      CUE schema: types + defaults
//	configs/config.yaml             shipped in-cluster default
//	configs/{compose,local}.yaml    dev overlays
//	deploy/chart/values.yaml        the chart's `config:` block, rendered
//	                                into the ConfigMap (covered by
//	                                TestHelmValuesConfigBlock, which loads it
//	                                for real rather than re-checking here)
//
// The loader is strict: an unknown key fails Load, which means a chart or
// config file that names a knob the binary doesn't have crashes every pod
// at boot rather than being ignored. That is the right runtime behaviour
// and a terrible way to find out. These tests move every such mismatch to
// CI.
//
// The golden file is the tripwire the rest hang off: it cannot be left
// stale, because adding or removing any field changes the key set.

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

const goldenPath = "testdata/config-keys.golden"

// repoFile resolves a path relative to the repository's backend/ directory.
// Tests run with the package dir as cwd, so everything outside
// internal/config is reached from there.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	return filepath.Join("..", "..", rel)
}

// configKeyPaths returns every dotted config path the Go struct accepts,
// sorted. This is exactly the set validateNoUnknownKeys checks against at
// load time, so the golden is a snapshot of the real accepted surface, not
// a parallel list someone has to remember to maintain.
func configKeyPaths() []string {
	known := collectKnownPaths(reflect.TypeOf(Config{}), "")
	out := make([]string, 0, len(known))
	for k := range known {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestConfigKeySetGolden fails whenever a config parameter is added or
// removed. That is the point: the failure is a prompt to go update the
// other four places, which no compiler checks.
//
// Regenerate with:
//
//	go test ./internal/config -run TestConfigKeySetGolden -update
func TestConfigKeySetGolden(t *testing.T) {
	got := strings.Join(configKeyPaths(), "\n") + "\n"

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %d keys", len(configKeyPaths()))
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read %s: %v\n\nRegenerate with: go test ./internal/config -run TestConfigKeySetGolden -update",
			goldenPath, err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	added, removed := diffLines(want, got)
	var b strings.Builder
	b.WriteString("config key set changed.\n\n")
	if len(added) > 0 {
		b.WriteString("ADDED:\n")
		for _, k := range added {
			b.WriteString("  + " + k + "\n")
		}
	}
	if len(removed) > 0 {
		b.WriteString("REMOVED:\n")
		for _, k := range removed {
			b.WriteString("  - " + k + "\n")
		}
	}
	b.WriteString(`
Every config parameter has to land in five places. Check each before
regenerating the golden:

  1. internal/config/types.go     the field itself, with a doc comment
                                  saying what it does and what 0/"" means
  2. internal/config/schema.cue   type constraint + default, so a config
                                  that omits it still loads
  3. configs/config.yaml          the shipped default, documented inline
  4. deploy/chart/values.yaml     under config:, if operators set it
  5. this golden

A REMOVED key also needs sweeping out of configs/*.yaml and the chart —
the loader is strict, so a leftover key crashes every pod at boot.

Then: go test ./internal/config -run TestConfigKeySetGolden -update`)
	t.Fatal(b.String())
}

// diffLines reports lines present only in got (added) and only in want
// (removed). Both inputs are sorted key-per-line, so a set diff is enough.
func diffLines(want, got string) (added, removed []string) {
	inWant := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(want), "\n") {
		if l != "" {
			inWant[l] = true
		}
	}
	inGot := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		if l != "" {
			inGot[l] = true
		}
	}
	for l := range inGot {
		if !inWant[l] {
			added = append(added, l)
		}
	}
	for l := range inWant {
		if !inGot[l] {
			removed = append(removed, l)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// TestShippedConfigsHaveNoUnknownKeys runs the loader's own strict check
// over every YAML this repo ships. A key that survives here but not at
// runtime is the worst kind of config bug: the pod crashloops on boot with
// "unknown config key", usually during a deploy, usually at the worst
// time.
func TestShippedConfigsHaveNoUnknownKeys(t *testing.T) {
	// config.yaml and local.yaml are additionally round-tripped through a
	// real Load by TestLoadRealConfigYAML, and the chart's config: block by
	// TestHelmValuesConfigBlock. compose.yaml had no coverage at all — it is
	// the reason this test enumerates rather than trusting the others.
	for _, rel := range []string{
		"configs/config.yaml",
		"configs/compose.yaml",
		"configs/local.yaml",
	} {
		t.Run(rel, func(t *testing.T) {
			path := repoFile(t, rel)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s is missing — update this test if it moved: %v", rel, err)
			}
			if err := validateNoUnknownKeys(path); err != nil {
				t.Errorf("%s\n\nEither the key is a typo, or the field was removed from "+
					"internal/config/types.go and this file still sets it.", err)
			}
		})
	}
}

// TestCueSchemaDeclaresNoUnknownKeys catches the mirror-image drift: a key
// declared in schema.cue that the Go struct does not have. CUE would
// happily inject a default for it, the JSON→struct decode would silently
// drop it, and the knob would look supported while doing nothing.
//
// Works by unifying the schema with a minimal concrete config (so every
// default materialises), then running the merged result through the same
// strict check the loader applies to operator YAML.
func TestCueSchemaDeclaresNoUnknownKeys(t *testing.T) {
	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		t.Fatalf("CUE schema invalid: %v", schemaVal.Err())
	}
	yamlFile, err := cueyaml.Extract("minimal.yaml", []byte(minimalConfigYAML))
	if err != nil {
		t.Fatalf("YAML -> CUE AST: %v", err)
	}
	combined := schemaVal.Unify(ctx.BuildFile(yamlFile))
	if err := combined.Validate(); err != nil {
		t.Fatalf("CUE validation failed: %v", err)
	}
	jsonBytes, err := combined.MarshalJSON()
	if err != nil {
		t.Fatalf("CUE -> JSON: %v", err)
	}
	var raw map[string]any
	if err := goyaml.Unmarshal(jsonBytes, &raw); err != nil {
		t.Fatalf("JSON -> map: %v", err)
	}
	if err := validateNoUnknownKeysInMap(raw); err != nil {
		t.Errorf("internal/config/schema.cue — %v\n\n"+
			"The schema declares a key the Go struct has no yaml tag for. Its "+
			"default is computed and then dropped on decode, so the knob does "+
			"nothing. Add the field to types.go or remove it from the schema.", err)
	}
}

// TestShippedConfigLoads is the end-to-end guard: the config this repo
// actually ships must survive a real Load, defaults and all. Catches a
// schema constraint that the shipped values violate — e.g. a duration
// written without units, or a value outside a declared bound.
func TestShippedConfigLoads(t *testing.T) {
	cfg, err := Load([]string{repoFile(t, "configs/config.yaml")}, zap.NewNop())
	if err != nil {
		t.Fatalf("configs/config.yaml does not load: %v", err)
	}
	// A couple of anchors so this fails loudly if the merge silently
	// produces a zero-valued Config rather than the shipped one.
	if cfg.App.Name == "" {
		t.Error("app.name is empty after load")
	}
	if cfg.Runtime.ShutdownTimeout == 0 {
		t.Error("runtime.shutdown_timeout is zero after load — defaults did not apply")
	}
}

// minimalConfigYAML carries only the fields schema.cue declares WITHOUT a
// default. Keeping it here (rather than inline in one test) means a new
// no-default field breaks every test that unifies the schema, which is the
// signal we want: a required knob with no default is a breaking change for
// every existing config file.
const minimalConfigYAML = `
app:
  name: "test-app"
  env: "local"
datastores:
  postgres:
    dsn: "postgres://localhost/test"
storage:
  backends:
    primary:
      kind: "s3-compatible"
      auth:
        mode: "static_keys"
        access_key: "a"
        secret_key: "b"
`

// schemaGapAllowlist records top-level Go config blocks that schema.cue
// does NOT declare, together with why that is tolerated. Being on this list
// means: the block's fields get Go zero values when a config file omits
// them, because there is no CUE default to inject.
//
// Adding an entry is a deliberate act with a cost — see the `ingest` note.
// The test exists so a NEW gap fails loudly instead of joining this one
// unnoticed.
var schemaGapAllowlist = map[string]string{
	// Empty, and it should stay that way. `ingest` was the sole entry and is
	// declared now; the test refuses a block that is both declared and
	// allowlisted, so an entry cannot outlive the gap it describes.
}

// TestGoBlocksAreDeclaredInSchema is the mirror of
// TestCueSchemaDeclaresNoUnknownKeys: that one catches schema keys with no
// Go field, this one catches Go blocks with no schema declaration.
//
// The two directions fail differently, which is why both are needed. A
// schema key with no struct field is inert — the value is computed and
// dropped. A struct block with no schema entry is worse: it loads fine,
// looks configured, and quietly runs on zero values.
func TestGoBlocksAreDeclaredInSchema(t *testing.T) {
	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		t.Fatalf("CUE schema invalid: %v", schemaVal.Err())
	}
	declared := map[string]bool{}
	iter, err := schemaVal.Fields()
	if err != nil {
		t.Fatalf("iterate schema fields: %v", err)
	}
	for iter.Next() {
		declared[iter.Selector().String()] = true
	}

	cfgType := reflect.TypeOf(Config{})
	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		tag := strings.SplitN(f.Tag.Get("yaml"), ",", 2)[0]
		if tag == "" || tag == "-" {
			continue
		}
		if declared[tag] {
			if why, listed := schemaGapAllowlist[tag]; listed {
				t.Errorf("%q is declared in schema.cue but still on schemaGapAllowlist "+
					"(%q) — drop the allowlist entry", tag, why)
			}
			continue
		}
		if _, allowed := schemaGapAllowlist[tag]; allowed {
			continue
		}
		t.Errorf("config block %q exists in types.go but is not declared in "+
			"internal/config/schema.cue.\n\n"+
			"Every field under it will take a Go zero value when a config file "+
			"omits it — no type constraint, no default. Either declare the block "+
			"in schema.cue (preferred), or add it to schemaGapAllowlist with a "+
			"note explaining why zero values are acceptable there.", tag)
	}
}

// unsetKnobAllowlist records dotted config paths that neither the chart nor
// schema.cue supplies a value for, together with why that is right.
//
// Prefix match: an entry covers itself and everything under it. Families
// rather than individual keys, because these are families — a knob added to
// an opt-in feature that is off by default should not need a new entry, while
// a knob added anywhere else should.
var unsetKnobAllowlist = map[string]string{
	"security.log_sensitive": "retired; accepted so old configs load, read by nothing, warned about when true",
	// Supplied at deploy time, never in a committed values file.
	"auth.signing_key_secret":                    "secret ref; the env overlay or external-secrets fills it",
	"api_token.hmac_key":                         "secret; per-deploy",
	"api_token.hmac_key_secret":                  "secret ref; per-deploy",
	"runtime.health_snapshot_token_secret":       "secret ref; per-deploy",
	"bootstrap.admin.password":                   "secret; per-deploy",
	"datastores.postgres.password":               "secret; per-deploy",
	"datastores.postgres.migrate_password":       "secret; per-deploy",
	"datastores.postgres.reaper_password":        "secret; per-deploy",
	"datastores.postgres.reaper_password_secret": "secret ref; per-deploy",
	"runtime.health_snapshot_token":              "secret; opt-in debug endpoint",

	// Built by the chart's ConfigMap template from the top-level `postgres`
	// block, so they never appear under `config:` in values.yaml.
	"datastores.postgres.migrate_dsn":             "rendered from chart values postgres.*",
	"datastores.postgres.password_secret":         "rendered from chart values postgres.app",
	"datastores.postgres.migrate_password_secret": "rendered from chart values postgres.migrate",

	// TLS knobs whose empty value IS the configuration: the certificate's
	// SANs already cover the Service names, and skipping verification is
	// something you turn on, never something you leave on.
	"admin.server.tls.server_name":             "cert SANs cover the Service name",
	"admin.server.tls.insecure_skip_verify":    "off is the only safe default",
	"api.server.data.tls.server_name":          "cert SANs cover the Service name",
	"api.server.data.tls.insecure_skip_verify": "off is the only safe default",
	"api.server.iam.tls.server_name":           "cert SANs cover the Service name",
	"api.server.iam.tls.insecure_skip_verify":  "off is the only safe default",
	"mcp.upstreams.tls.server_name":            "cert SANs cover the Service name",
	"mcp.upstreams.tls.insecure_skip_verify":   "off is the only safe default",
	"worker.ops.tls.server_name":               "cert SANs cover the Service name",
	"worker.ops.tls.insecure_skip_verify":      "off is the only safe default",

	// The dispatcher's ops listener is in-cluster and plaintext by design;
	// the whole tls block is therefore unset rather than half-filled.
	"dispatcher.ops.tls": "in-cluster plaintext listener; enabling TLS is opt-in",

	// Opt-in features. Zero means off, and off is the shipped state.
	"auth.oauth":      "OAuth AS is opt-in (ADR-0009)",
	"mcp.oauth":       "MCP OAuth metadata is opt-in",
	"mcp.always_deny": "empty means 'no extra denies'; profiles carry the defaults",
	"auth.login_rate_limit_per_ip_per_minute":         "0 disables; a deploy that wants it sets it",
	"auth.login_rate_limit_per_subject_per_minute":    "0 disables; a deploy that wants it sets it",
	"worker.jobs.housekeeping.hard_delete_after":      "0 disables the hard-deleter; opt-in per deploy",
	"worker.jobs.housekeeping.hard_delete_batch_size": "unused while hard_delete_after is 0",
	"dispatcher.charge_events_enabled":                "opt-in billing signal",
	"dispatcher.audit_mirror_enabled":                 "set per deploy; the chart's overlay turns it on",

	// Whole blocks already recorded as schema gaps by schemaGapAllowlist.
	// Repeating every leaf here would say the same thing 30 times.
	"ingest": "see schemaGapAllowlist — the whole block has no CUE schema",
}

// TestEveryKnobHasAValueFromSomewhere fails when a config key gets its value
// from neither the chart nor schema.cue, so a real deploy runs it on the Go
// zero value.
//
// This is the shape of a bug that has already shipped twice, and both times
// it arrived as a page report rather than a config one:
//
//	mcp.http.sessions_url    absent → the admin plane had no bridge to ask,
//	                         so /mcp answered "no MCP server configured" and
//	                         could never show a session
//
// The loader is strict about keys it does not recognise and silent about keys
// nobody sets. That is the right way round for safety and the wrong way round
// for noticing, which is what this test is for.
//
// "From somewhere" deliberately excludes the env overlays: values-local.yaml
// is a dev file, and a knob only it sets is still unset in production.
func TestEveryKnobHasAValueFromSomewhere(t *testing.T) {
	chart := chartConfigKeys(t)

	// What CUE alone supplies: load a config carrying only the fields that
	// have no default, and see what came out non-zero.
	dir := t.TempDir()
	minPath := filepath.Join(dir, "min.yaml")
	if err := os.WriteFile(minPath, []byte(minimalConfigYAML), 0o600); err != nil {
		t.Fatalf("write minimal config: %v", err)
	}
	minimal, err := Load([]string{minPath}, zap.NewNop())
	if err != nil {
		t.Fatalf("load minimal config: %v", err)
	}

	var unset []string
	for _, path := range zeroValuedPaths(reflect.ValueOf(minimal), "") {
		if chart[path] || allowlistedKnob(path) {
			continue
		}
		unset = append(unset, path)
	}
	sort.Strings(unset)

	for _, path := range unset {
		t.Errorf("config knob %q has no value from the chart and no default in "+
			"schema.cue, so a deploy runs it on the Go zero value.\n\n"+
			"Set it in deploy/chart/values.yaml (preferred — that is what the "+
			"ConfigMap renders), give it a default in schema.cue, or add it to "+
			"unsetKnobAllowlist with the reason zero is correct there.", path)
	}
}

// allowlistedKnob reports whether path, or any prefix of it, is allowlisted.
func allowlistedKnob(path string) bool {
	for {
		if _, ok := unsetKnobAllowlist[path]; ok {
			return true
		}
		i := strings.LastIndexByte(path, '.')
		if i < 0 {
			return false
		}
		path = path[:i]
	}
}

// chartConfigKeys returns every dotted path the chart's `config:` block names,
// parents included. values.yaml only: the env overlays are per-deploy, and a
// knob that only values-local.yaml sets is still unset in production.
func chartConfigKeys(t *testing.T) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(repoFile(t, "deploy/chart/values.yaml"))
	if err != nil {
		t.Fatalf("read chart values: %v", err)
	}
	var wrap struct {
		Config map[string]any `yaml:"config"`
	}
	if err := goyaml.Unmarshal(body, &wrap); err != nil {
		t.Fatalf("decode chart values: %v", err)
	}
	out := map[string]bool{}
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for k, v := range m {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = true
			if sub, ok := v.(map[string]any); ok {
				walk(sub, p)
			}
		}
	}
	walk(wrap.Config, "")
	return out
}

// zeroValuedPaths returns the dotted path of every leaf field left at its Go
// zero value. Structs are descended into; anything else is a leaf.
func zeroValuedPaths(v reflect.Value, prefix string) []string {
	var out []string
	tp := v.Type()
	for i := 0; i < tp.NumField(); i++ {
		tag := strings.SplitN(tp.Field(i).Tag.Get("yaml"), ",", 2)[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct && fv.Type() != reflect.TypeOf(time.Time{}) {
			out = append(out, zeroValuedPaths(fv, path)...)
			continue
		}
		if fv.IsZero() {
			out = append(out, path)
		}
	}
	return out
}

// The gap this closes was not "a block is missing from the schema" — it was
// what that silence did. With no `ingest:` declaration and none in
// configs/config.yaml, every knob took its Go zero value, and two of them are
// load-bearing: serve_ingest passes dedup_ttl and reaper_interval straight
// through with no fallback, and worker.RunTicker reads an interval <= 0 as
// "disabled". So the reaper never ran and `ingested_events` grew without
// bound, while the struct comments promised "Default 24h" and "Default 1h".
//
// Declaring the block is only half the fix; the half worth a test is that the
// values actually arrive. A schema block with a typo'd key name would satisfy
// the drift test above and still leave the zero values in place.
func TestIngestDefaultsReachTheStruct(t *testing.T) {
	cfg, err := Load([]string{"../../configs/config.yaml"}, zap.NewNop())
	if err != nil {
		t.Fatalf("load the shipped config: %v", err)
	}
	if got := cfg.Ingest.DedupTTL; got != 24*time.Hour {
		t.Errorf("dedup_ttl = %v, want 24h — a zero here retains nothing", got)
	}
	if got := cfg.Ingest.ReaperInterval; got != time.Hour {
		t.Errorf("reaper_interval = %v, want 1h — a zero here disables the reaper", got)
	}
	// One value per sub-block, enough to catch a block that parsed but landed
	// nowhere.
	if got := cfg.Ingest.Webhook.Addr; got == "" {
		t.Error("webhook.addr is empty; the webhook driver would bind nothing")
	}
	if got := cfg.Ingest.Webhook.SignatureHeader; got != "X-Paladin-Signature" {
		t.Errorf("signature_header = %q, want X-Paladin-Signature", got)
	}
	if got := cfg.Ingest.Webhook.MaxBodyBytes; got <= 0 {
		t.Errorf("max_body_bytes = %d; a zero cap rejects every payload", got)
	}
	if got := cfg.Ingest.SQS.MaxMessages; got <= 0 {
		t.Errorf("sqs.max_messages = %d; a zero would long-poll for nothing", got)
	}
}

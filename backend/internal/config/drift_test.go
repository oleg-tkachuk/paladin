package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

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
	"ingest": "no `ingest:` block in schema.cue AND none in configs/config.yaml, " +
		"so every knob falls back to a Go zero value. dedup_ttl and " +
		"reaper_interval are read raw by serve_ingest with no fallback, and " +
		"RunTicker treats interval<=0 as 'disabled' — so an ingest deployment " +
		"that doesn't spell them out silently never reaps ingested_events. " +
		"See BACKLOG.",
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

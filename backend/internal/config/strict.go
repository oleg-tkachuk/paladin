package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	goyaml "gopkg.in/yaml.v3"
)

// validateNoUnknownKeys parses the YAML at `path` and reports any dotted
// key that has no matching `yaml:` tag on the Config struct (or its
// substructures, recursively). The check is **strict by default** — a
// typo in a field name fails config load with an explicit error listing
// every unknown key, so an operator never wonders why an option they set
// has no effect.
//
// Limitations:
//   - Env-var overrides loaded by koanf are NOT validated. Operators
//     occasionally pass extra PALADIN_* env vars (token shells, debug
//     toggles); whitelisting them is a separate concern.
//   - Map-typed subtrees with dynamic string keys (storage.backends.<name>,
//     mcp.upstreams.*) are walked into via the value type — the wildcard
//     level itself is opaque.
func validateNoUnknownKeys(path string) error {
	body, err := os.ReadFile(path) // #nosec G304 — operator-supplied path
	if err != nil {
		// File-not-found / permission errors are not our problem here;
		// the main loader already surfaces them.
		return nil
	}
	var raw map[string]any
	if err := goyaml.Unmarshal(body, &raw); err != nil {
		// Malformed YAML — let the main loader's parse error fire.
		return nil
	}
	if err := validateNoUnknownKeysInMap(raw); err != nil {
		return fmt.Errorf("unknown config key(s) in %s — %w", path, err)
	}
	return nil
}

// validateNoUnknownKeysInMap is the post-merge variant used by the
// multi-file Load path. It accepts the already-merged koanf raw map
// so an overlay file that legally drops most of the schema isn't
// rejected for keys it never tried to set.
func validateNoUnknownKeysInMap(raw map[string]any) error {
	known := collectKnownPaths(reflect.TypeOf(Config{}), "")

	var unknown []string
	walkUnknown(raw, "", "", known, &unknown)

	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf(
		"unknown config key(s) — typo or unsupported field?\n  %s",
		strings.Join(unknown, "\n  "),
	)
}

// collectKnownPaths walks a struct type and returns the set of all
// dotted paths that map to Go fields via `yaml:` tags. Map-with-dynamic-
// keys subtrees yield a "<prefix>.*" wildcard token plus the recursed
// paths under "<prefix>.*.<sub>".
func collectKnownPaths(t reflect.Type, prefix string) map[string]struct{} {
	out := map[string]struct{}{}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("yaml")
		// Skip yaml:"-" (in-memory only) and untagged fields.
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = struct{}{}

		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			for sub := range collectKnownPaths(ft, path) {
				out[sub] = struct{}{}
			}
		case reflect.Map:
			// `map[string]X` — wildcard the key, recurse into X.
			out[path+".*"] = struct{}{}
			for sub := range collectKnownPaths(ft.Elem(), path+".*") {
				out[sub] = struct{}{}
			}
		}
	}
	return out
}

// walkUnknown traverses the YAML-decoded map and appends to `unknown`
// any dotted path that is not present in `known`.
//
// Two prefixes are tracked independently:
//   - `literal` keeps the actual key sequence so error messages name
//     the offending field (e.g. "storage.backends.primary.kynd").
//   - `lookup` uses `*` for any segment whose parent was a map with
//     dynamic string keys, so the lookup itself matches the schema
//     entry registered as `storage.backends.*.kind`.
func walkUnknown(node any, literal, lookup string, known map[string]struct{}, unknown *[]string) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	for key, val := range m {
		literalPath := key
		if literal != "" {
			literalPath = literal + "." + key
		}

		// Direct match against the schema-derived path.
		candidate := key
		if lookup != "" {
			candidate = lookup + "." + key
		}
		if _, ok := known[candidate]; ok {
			walkUnknown(val, literalPath, candidate, known, unknown)
			continue
		}

		// Wildcard match — current level is a map with dynamic keys, so
		// any literal name is allowed; descend with `*` substituted.
		wildcard := "*"
		if lookup != "" {
			wildcard = lookup + ".*"
		}
		if _, ok := known[wildcard]; ok {
			walkUnknown(val, literalPath, wildcard, known, unknown)
			continue
		}

		*unknown = append(*unknown, literalPath)
	}
}

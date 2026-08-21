package config

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
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

// ─── environment-variable key mapping ───────────────────────────────────────

// EnvKeyMapper turns PALADIN_A_B_C into the config path it addresses.
//
// The naive mapping — lowercase, then replace every `_` with `.` — is wrong
// for any key that contains an underscore of its own. `public_endpoint`
// became `public.endpoint`, a path no struct field has, so the variable was
// silently ignored: the file's value stayed and nothing said otherwise.
// `PALADIN_STORAGE_BACKENDS_PRIMARY_PUBLIC_ENDPOINT` pointing at a
// presigned-URL host that the caller can actually reach is exactly the kind
// of setting that only fails later, at the client, far from the config.
//
// So the mapping is resolved against the schema instead of guessed: split
// the variable on `_`, then walk the known paths choosing, at each step, the
// longest field name that matches the next segments. Segments that match no
// field fall back to being joined with `_`, which is how a map's dynamic key
// (a backend named `my_backend`) still works.
func EnvKeyMapper(known map[string]struct{}) func(string) string {
	return func(s string) string {
		segs := strings.Split(strings.ToLower(strings.TrimPrefix(s, EnvPrefix)), "_")
		var path []string   // resolved config path
		var lookup []string // same, with `*` for dynamic map keys

		for i := 0; i < len(segs); {
			matched := false
			// Longest match first: `public_endpoint` must win over `public`.
			for n := len(segs) - i; n >= 1; n-- {
				cand := strings.Join(segs[i:i+n], "_")
				probe := append(append([]string{}, lookup...), cand)
				if _, ok := known[strings.Join(probe, ".")]; ok {
					path = append(path, cand)
					lookup = append(lookup, cand)
					i += n
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			// No field by that name. If the parent is a map, this is its
			// dynamic key — take one segment and continue under `*`.
			wildcard := strings.Join(append(append([]string{}, lookup...), "*"), ".")
			if _, ok := known[wildcard]; ok {
				path = append(path, segs[i])
				lookup = append(lookup, "*")
				i++
				continue
			}
			// Unresolvable: emit the remainder joined by `_` so the strict
			// check downstream reports it as an unknown key rather than
			// inventing a plausible-looking path.
			path = append(path, strings.Join(segs[i:], "_"))
			break
		}
		return strings.Join(path, ".")
	}
}

// KnownConfigPaths exposes the schema's path set for the env mapper.
func KnownConfigPaths() map[string]struct{} {
	return collectKnownPaths(reflect.TypeOf(Config{}), "")
}

// KnownConfigKinds maps each schema path to the kind of the field it names.
// The env loader uses it to convert a variable's string into the type the
// schema expects — environment values are always strings, and CUE rejects
// "16" where it wants an int.
func KnownConfigKinds() map[string]reflect.Kind {
	out := map[string]reflect.Kind{}
	collectKinds(reflect.TypeOf(Config{}), "", out)
	return out
}

func collectKinds(t reflect.Type, prefix string, out map[string]reflect.Kind) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("yaml")
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
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		out[path] = ft.Kind()
		switch ft.Kind() {
		case reflect.Struct:
			collectKinds(ft, path, out)
		case reflect.Map:
			collectKinds(ft.Elem(), path+".*", out)
		}
	}
}

// EnvValueParser converts an environment string into the type its config
// path expects. Anything it cannot place is left as a string, so an unknown
// key still reaches the strict check as-is rather than being coerced into
// something that happens to validate.
func EnvValueParser(kinds map[string]reflect.Kind) func(string, string) any {
	return func(path, raw string) any {
		kind, ok := kinds[wildcardPath(path, kinds)]
		if !ok {
			return raw
		}
		switch kind {
		case reflect.Bool:
			if b, err := strconv.ParseBool(raw); err == nil {
				return b
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// time.Duration is an int64 that must stay a string: "30s"
			// parses as a duration downstream, not as a number of
			// nanoseconds someone typed out.
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				return n
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
				return n
			}
		case reflect.Float32, reflect.Float64:
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				return f
			}
		case reflect.Slice:
			// Comma-separated is the only list form an env var can carry.
			return strings.Split(raw, ",")
		}
		return raw
	}
}

// wildcardPath rewrites a concrete path so a dynamic map key matches the
// schema entry registered under `*`.
func wildcardPath(path string, kinds map[string]reflect.Kind) string {
	if _, ok := kinds[path]; ok {
		return path
	}
	segs := strings.Split(path, ".")
	for i := range segs {
		probe := append(append([]string{}, segs[:i]...), "*")
		probe = append(probe, segs[i+1:]...)
		if _, ok := kinds[strings.Join(probe, ".")]; ok {
			return strings.Join(probe, ".")
		}
	}
	return path
}

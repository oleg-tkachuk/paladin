package cel

import (
	"errors"
	"testing"
)

// maxFuzzFilterBytes caps the filters the fuzz target compiles, for its
// throughput: cel-go bounds what it parses itself, and maxEvalCost what one
// evaluation may cost.
const maxFuzzFilterBytes = 4096

// Any filter a caller sends, under every schema a list RPC compiles against:
// compiling never panics, the admin validation RPC and the list RPCs agree on
// whether it compiles, a refusal is the *CompileError the validation RPC
// reads its position from, a filter that compiles evaluates to an answer or
// an error within maxEvalCost, and the pushdown extractors that narrow the
// SQL never panic on it either.
func FuzzCompileFilter(f *testing.F) {
	for _, expr := range []string{
		`type == "paladin.object.uploaded"`,
		`tenant_id != "" && type.startsWith("paladin.")`,
		`size_bytes > 1048576`,
		`state == "AVAILABLE" && size_bytes > 0`,
		`search.contains("deletetenant")`,
		`type == "x" && bogus_field`,
		`size_bytes`,
		`provider == "s3" && display_name.startsWith("prod")`,
		`enabled && read_only == false && !maintenance`,
		`state in ["FAILED", "RUNNING"] || state == "PENDING"`,
		`tags['env'] == 'prod' && metadata['owner'] == 'ops'`,
		`tags['a'] == 'x' || tags['b'] == 'y'`,
		`[1, 2].all(x, x > 0)`,
		nestedComprehension(),
		`(((((`,
		"",
	} {
		f.Add(expr)
	}
	eval := NewEvaluator()
	f.Fuzz(func(t *testing.T, expr string) {
		if len(expr) > maxFuzzFilterBytes {
			t.Skip("longer than the harness compiles")
		}
		for _, name := range SchemaNames() {
			schema := SchemaByName(name)
			verr := Validate(schema, expr)
			cerr := CompileFirstError(schema, expr)
			if (verr == nil) != (cerr == nil) {
				t.Fatalf("%s %q: Validate = %v, CompileFirstError = %v", name, expr, verr, cerr)
			}
			var ce *CompileError
			if cerr != nil && !errors.As(cerr, &ce) {
				t.Fatalf("%s %q: CompileFirstError = %T, want *CompileError", name, expr, cerr)
			}
			if prog, err := eval.Compile(schema, expr); err == nil {
				_, _ = Match(prog, map[string]any{})
			} else if verr == nil {
				t.Fatalf("%s %q: Validate accepted what Compile refused: %v", name, expr, err)
			}
			_, _ = ExtractPushdown(schema, expr)
			_, _ = ExtractPushdownBranches(schema, expr)
		}
		_, _ = ExtractObjectPushdown(expr)
		_, _ = ExtractObjectBranches(expr)
		_, _ = ExtractAuditPushdown(expr)
		_, _ = ObjectFilterMentions(expr, "tags")
	})
}

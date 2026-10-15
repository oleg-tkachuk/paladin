package cel

import (
	"errors"
	"strings"
	"testing"
)

// A literal the field's SQL enum does not hold must fail compilation. For
// objects it used to compile, reach Postgres as an object_state parameter, and
// fail the whole query with 22P02 — an Internal error for a caller's typo.
func TestEnumLiteralRejectedAtCompile(t *testing.T) {
	cases := []struct {
		name   string
		schema *Schema
		expr   string
		value  string
	}{
		{"object state lowercase", ObjectSchema, `state == "available"`, "available"},
		{"object state reversed operands", ObjectSchema, `"active" == state`, "active"},
		{"object state not-equals", ObjectSchema, `state != "gone"`, "gone"},
		{"object state in list", ObjectSchema, `state in ["AVAILABLE", "nope"]`, "nope"},
		{"object state under or", ObjectSchema, `key == "a" || state == "x"`, "x"},
		{"object state under not", ObjectSchema, `!(state == "x")`, "x"},
		{"object state inside macro", ObjectSchema, `["k"].exists(k, state == "x")`, "x"},
		{"object state in conditional", ObjectSchema, `size_bytes > 0 ? state == "x" : true`, "x"},
		{"object state under member call", ObjectSchema, `(state == "x" ? "a" : "b").startsWith("a")`, "x"},
		{"object state in list literal", ObjectSchema, `[state == "x"].all(b, b)`, "x"},
		{"object state in map literal", ObjectSchema, `{"a": state == "x"}["a"]`, "x"},
		// A failed object is deleted, so FAILED is not a state any more.
		{"object state FAILED", ObjectSchema, `state == "FAILED"`, "FAILED"},
		{"operation state", OperationSchema, `state == "running"`, "running"},
		{"tenant storage layout", TenantSchema, `storage_layout == "SHARED"`, "SHARED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accepted, _ := tc.schema.EnumValues(enumField(t, tc.schema, tc.expr))
			want := []string{strings.Join(accepted, ", "), `"` + tc.value + `"`}

			_, err := NewEvaluator().Compile(tc.schema, tc.expr)
			assertMentions(t, "Compile", err, want)
			assertMentions(t, "Validate", Validate(tc.schema, tc.expr), want)

			err = CompileFirstError(tc.schema, tc.expr)
			assertMentions(t, "CompileFirstError", err, want)
			var ce *CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("CompileFirstError: %T, want *CompileError", err)
			}
			wantCol := strings.Index(tc.expr, `"`+tc.value+`"`) + 1
			if ce.Line != 1 || ce.Column != wantCol {
				t.Errorf("position = %d:%d, want 1:%d", ce.Line, ce.Column, wantCol)
			}
		})
	}
}

func TestEnumLiteralAcceptedValuesCompile(t *testing.T) {
	cases := []struct {
		schema *Schema
		expr   string
	}{
		{ObjectSchema, `state == "AVAILABLE"`},
		{ObjectSchema, `"DELETED" == state`},
		{ObjectSchema, `state != "DELETED"`},
		{ObjectSchema, `state in ["PENDING", "AVAILABLE", "DELETED"]`},
		// Not a comparison with a literal: nothing to check.
		{ObjectSchema, `state.startsWith("avail")`},
		{ObjectSchema, `state == content_type`},
		// Non-enum fields take any string.
		{ObjectSchema, `content_type == "anything"`},
		{CollectionSchema, `collection == "state"`},
		{OperationSchema, `state == "SUCCEEDED" && type == "whatever"`},
		{TenantSchema, `storage_layout in ["shared", "dedicated"]`},
	}
	for _, tc := range cases {
		if _, err := NewEvaluator().Compile(tc.schema, tc.expr); err != nil {
			t.Errorf("Compile(%s, %s): %v", tc.schema.Name, tc.expr, err)
		}
		if err := CompileFirstError(tc.schema, tc.expr); err != nil {
			t.Errorf("CompileFirstError(%s, %s): %v", tc.schema.Name, tc.expr, err)
		}
	}
}

func TestEnumValues(t *testing.T) {
	got, ok := ObjectSchema.EnumValues(fieldState)
	if !ok || strings.Join(got, ",") != "AVAILABLE,DELETED,PENDING" {
		t.Errorf("EnumValues(state) = %v, %v", got, ok)
	}
	got[0] = "mutated"
	if again, _ := ObjectSchema.EnumValues(fieldState); again[0] == "mutated" {
		t.Error("EnumValues returned the schema's own slice")
	}
	if _, ok := ObjectSchema.EnumValues("key"); ok {
		t.Error("key reported as enum-backed")
	}
	if _, ok := CollectionSchema.EnumValues(fieldState); ok {
		t.Error("Collection has no enum-backed fields")
	}
}

// Syntax errors keep cel-go's full report for logs, and its first diagnostic
// for the validation RPC.
func TestCompileErrorKeepsDetail(t *testing.T) {
	err := CompileFirstError(ObjectSchema, `nope == "x"`)
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("%T, want *CompileError", err)
	}
	if strings.Contains(ce.Message, "ERROR:") {
		t.Errorf("Message carries the rendered report: %q", ce.Message)
	}
	if !strings.Contains(ce.Error(), "ERROR:") || !strings.Contains(ce.Error(), ce.Message) {
		t.Errorf("Error() = %q, want the rendered report", ce.Error())
	}
}

func enumField(t *testing.T, s *Schema, expr string) string {
	t.Helper()
	for f := range s.enums {
		if strings.Contains(expr, f) {
			return f
		}
	}
	t.Fatalf("no enum field in %q", expr)
	return ""
}

func assertMentions(t *testing.T, what string, err error, want []string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: nil error", what)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%s: %q does not mention %q", what, err, w)
		}
	}
}

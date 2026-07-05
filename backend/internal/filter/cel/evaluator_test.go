package cel

import (
	"errors"
	"strings"
	"testing"
)

func TestSchemaByName(t *testing.T) {
	cases := []struct {
		in   string
		want *Schema
	}{
		{"Object", ObjectSchema},
		{"ObjectKey", BucketSchema},
		{"AuditLogEntry", AuditLogSchema},
		{"EventEnvelope", EventEnvelopeSchema},
		{"", nil},
		{"unknown", nil},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SchemaByName(tc.in)
			if got != tc.want {
				t.Fatalf("SchemaByName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateEventEnvelope_OK(t *testing.T) {
	if err := Validate(EventEnvelopeSchema, `type == "paladin.object.uploaded"`); err != nil {
		t.Fatalf("expected valid expression, got %v", err)
	}
	if err := Validate(EventEnvelopeSchema, `tenant_id != "" && type.startsWith("paladin.")`); err != nil {
		t.Fatalf("expected valid expression, got %v", err)
	}
	// Empty is the match-all sentinel.
	if err := Validate(EventEnvelopeSchema, ""); err != nil {
		t.Fatalf("empty expression must be valid, got %v", err)
	}
}

func TestValidateEventEnvelope_PayloadDerivedFields(t *testing.T) {
	// The payload-derived attributes (projected by worker.eventCELVars) must
	// type-check against the schema so the admin UI's richer-filter hints
	// compile rather than being rejected as unknown identifiers.
	exprs := []string{
		`object_key == "invoices"`,
		`size_bytes > 1048576`,
		`etag != ""`,
		`bucket_name == "paladin-primary"`,
		`kind == "storage" && severity == "high"`,
	}
	for _, expr := range exprs {
		if err := Validate(EventEnvelopeSchema, expr); err != nil {
			t.Errorf("Validate(EventEnvelope, %q): %v", expr, err)
		}
	}
}

func TestValidateEventEnvelope_UnknownField(t *testing.T) {
	err := Validate(EventEnvelopeSchema, `no_such_field == "x"`)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestValidateAllSchemas_BasicHappyPaths(t *testing.T) {
	cases := []struct {
		schema *Schema
		expr   string
	}{
		{ObjectSchema, `state == "AVAILABLE" && size_bytes > 0`},
		{BucketSchema, `display_name != ""`},
		{AuditLogSchema, `is_error == true`},
		{EventEnvelopeSchema, `type == "paladin.object.uploaded"`},
	}
	for _, tc := range cases {
		t.Run(tc.schema.Name, func(t *testing.T) {
			if err := Validate(tc.schema, tc.expr); err != nil {
				t.Fatalf("Validate(%s, %q): %v", tc.schema.Name, tc.expr, err)
			}
		})
	}
}

func TestCompileFirstError_PositionInfo(t *testing.T) {
	// Unknown identifier → expect a CompileError with line/column populated.
	err := CompileFirstError(EventEnvelopeSchema, `type == "x" && bogus_field`)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompileError, got %T (%v)", err, err)
	}
	if ce.Line < 1 {
		t.Fatalf("expected Line >= 1, got %d", ce.Line)
	}
	if ce.Column < 1 {
		t.Fatalf("expected Column >= 1, got %d", ce.Column)
	}
	if ce.Message == "" || !strings.Contains(strings.ToLower(ce.Message), "bogus_field") {
		t.Fatalf("expected message to mention identifier, got %q", ce.Message)
	}
}

func TestCompileFirstError_EmptyExpressionValid(t *testing.T) {
	if err := CompileFirstError(ObjectSchema, ""); err != nil {
		t.Fatalf("empty expression must be valid, got %v", err)
	}
}

func TestCompileFirstError_NonBoolOutput(t *testing.T) {
	err := CompileFirstError(ObjectSchema, `size_bytes`)
	if err == nil {
		t.Fatal("expected error for non-bool output")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CompileError, got %T", err)
	}
}

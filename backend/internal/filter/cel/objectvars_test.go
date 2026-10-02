package cel

import (
	"testing"
	"time"
)

// A filter compiles against the schema and evaluates against the projection;
// a declared field the projection omits compiles, then fails on every row.
// See TestTenantRowCoversEverySchemaField for the same guard on tenants.
func TestObjectVarsCoversObjectSchema(t *testing.T) {
	now := time.Now()
	vars := ObjectVars(ObjectRow{CommittedAt: &now})
	declared := map[string]bool{}
	for _, f := range ObjectSchema.Fields() {
		declared[f] = true
		if _, ok := vars[f]; !ok {
			t.Errorf("ObjectSchema declares %q and ObjectVars does not project it", f)
		}
	}
	for k := range vars {
		if !declared[k] {
			t.Errorf("ObjectVars projects %q, which ObjectSchema does not declare", k)
		}
	}
}

func TestObjectVarsEvaluates(t *testing.T) {
	committed := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	row := ObjectRow{
		Key: "logs/2026/a.txt", State: "AVAILABLE", ContentType: "text/plain",
		CreatedAt: committed.Add(-time.Hour), UpdatedAt: committed, CommittedAt: &committed,
	}
	ev := NewEvaluator()
	for expr, want := range map[string]bool{
		`key.startsWith("logs/")`:                                 true,
		`created_at < timestamp("2026-05-01T00:00:00Z")`:          true,
		`updated_at >= timestamp("2026-05-01T00:00:00Z")`:         true,
		`committed_at > timestamp("2026-06-01T00:00:00Z")`:        false,
		`!has(tags.env) && size_bytes == 0 && external_ref == ""`: true,
	} {
		prog, err := ev.Compile(ObjectSchema, expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		got, err := Match(prog, ObjectVars(row))
		if err != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", expr, got, err, want)
		}
	}

	// Never committed: a filter on committed_at reports the missing value
	// instead of matching against an invented time.
	row.CommittedAt = nil
	prog, _ := ev.Compile(ObjectSchema, `committed_at > timestamp("2000-01-01T00:00:00Z")`)
	if ok, err := Match(prog, ObjectVars(row)); ok || err == nil {
		t.Errorf("uncommitted object: match=%v err=%v; want no match and an error", ok, err)
	}
}

package cel

import (
	"testing"
	"time"
)

type row struct {
	Slug     string
	Disabled bool
	Created  time.Time
}

func rowMap(r row) map[string]any {
	return map[string]any{
		"tenant_id":      "t",
		"slug":           r.Slug,
		"display_name":   r.Slug,
		"storage_layout": "shared",
		"labels":         map[string]string{"env": r.Slug},
		"created_at":     r.Created,
		"updated_at":     r.Created,
	}
}

// TestFilterPage covers the contract every List RPC now depends on: the page
// is narrowed, the caller's cursor is untouched, and a bad expression is an
// error rather than a silently unfiltered page — which is what these five RPCs
// did before, returning everything to a caller that asked for a subset.
func TestFilterPage(t *testing.T) {
	t.Parallel()

	e := NewEvaluator()
	now := time.Now()
	page := []row{
		{Slug: "acme", Created: now},
		{Slug: "acme-eu", Disabled: true, Created: now},
		{Slug: "globex", Created: now},
	}

	t.Run("empty expression returns the page untouched", func(t *testing.T) {
		got, err := FilterPage(e, TenantSchema, "", page, rowMap)
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d rows, want all 3", len(got))
		}
	})

	t.Run("prefix match narrows", func(t *testing.T) {
		in := append([]row(nil), page...)
		got, err := FilterPage(e, TenantSchema, `slug.startsWith("acme")`, in, rowMap)
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d rows, want 2 (acme, acme-eu)", len(got))
		}
	})

	t.Run("map access", func(t *testing.T) {
		in := append([]row(nil), page...)
		got, err := FilterPage(e, TenantSchema, `labels["env"] == "globex"`, in, rowMap)
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if len(got) != 1 || got[0].Slug != "globex" {
			t.Fatalf("got %+v, want just globex", got)
		}
	})

	t.Run("a page where nothing matches is empty, not an error", func(t *testing.T) {
		in := append([]row(nil), page...)
		got, err := FilterPage(e, TenantSchema, `slug == "nobody"`, in, rowMap)
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d rows, want 0", len(got))
		}
	})

	t.Run("unknown field is rejected, not ignored", func(t *testing.T) {
		in := append([]row(nil), page...)
		if _, err := FilterPage(e, TenantSchema, `not_a_field == "x"`, in, rowMap); err == nil {
			t.Fatal("FilterPage accepted a filter over an undeclared field")
		}
	})

	t.Run("non-boolean expression is rejected", func(t *testing.T) {
		in := append([]row(nil), page...)
		if _, err := FilterPage(e, TenantSchema, `slug`, in, rowMap); err == nil {
			t.Fatal("FilterPage accepted a filter that does not yield a bool")
		}
	})
}

// TestSchemasAreReachableByName pins that every schema a List RPC filters
// against is resolvable through SchemaByName, which is what CELService.Validate
// uses to check an expression before the caller saves it. A schema the
// validator cannot find would leave the console unable to pre-check a filter
// the server will happily run.
func TestSchemasAreReachableByName(t *testing.T) {
	t.Parallel()

	for _, s := range []*Schema{
		TenantSchema, StorageBackendSchema, PhysicalBucketSchema,
		CollectionSchema, OperationSchema, UserSchema,
		ObjectSchema, AuditLogSchema, EventEnvelopeSchema,
	} {
		if got := SchemaByName(s.Name); got != s {
			t.Errorf("SchemaByName(%q) did not return the schema it names", s.Name)
		}
	}
}

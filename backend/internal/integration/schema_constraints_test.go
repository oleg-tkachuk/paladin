//go:build integration

// The names in internal/store/postgres/schema are matched against
// pg_error.ConstraintName to turn a 23505 or 23503 into a typed sentinel —
// ErrSlugConflict, ErrDefaultBindingBucketMissing, and so on. When a name
// drifts, nothing breaks loudly: the switch simply stops matching and the
// caller gets an opaque "operation failed: <pg error>" instead of "that slug
// is taken". The package comment claims a rename "forces a compile error in
// every site that depended on the old name" — that is true of the Go
// identifier and false of the string, which is the half that matters.
//
// This test closes that gap by asking Postgres what the names actually are.
package integration

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/schema"
)

func TestSchemaConstraintNamesExist(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	// Constraints and indexes are separate catalogs, and some of these
	// names are partial unique INDEXES rather than constraints — a
	// uniqueness rule scoped to live rows cannot be a table constraint.
	// Both spellings reach pg_error.ConstraintName identically, so both
	// are legitimate here.
	const q = `
		SELECT EXISTS (
		    SELECT 1 FROM pg_constraint WHERE conname = $1
		    UNION ALL
		    SELECT 1 FROM pg_class WHERE relkind = 'i' AND relname = $1
		    UNION ALL
		    SELECT 1 FROM pg_trigger WHERE tgname = $1
		)`

	for _, c := range []struct{ ident, name string }{
		{"TenantsPK", schema.TenantsPK},
		{"TenantsSlugUnique", schema.TenantsSlugUnique},
		{"TenantsSlugFormat", schema.TenantsSlugFormat},
		{"TenantsDisplayNameUnique", schema.TenantsDisplayNameUnique},
		{"TenantsDisplayNameFormat", schema.TenantsDisplayNameFormat},
		{"TenantsImmutableColumnsTrigger", schema.TenantsImmutableColumnsTrigger},
		{"CollectionsPK", schema.CollectionsPK},
		{"CollectionsTenantIDFK", schema.CollectionsTenantIDFK},
		{"CollectionsBucketFK", schema.CollectionsBucketFK},
		{"CollectionsFormat", schema.CollectionsFormat},
		{"TenantDefaultBindingsPK", schema.TenantDefaultBindingsPK},
		{"TenantDefaultBindingsTenantFK", schema.TenantDefaultBindingsTenantFK},
		{"TenantDefaultBindingsBucketFK", schema.TenantDefaultBindingsBucketFK},
		{"BucketsPK", schema.BucketsPK},
		{"BucketsBackendFK", schema.BucketsBackendFK},
	} {
		var ok bool
		if err := pool.QueryRow(ctx, q, c.name).Scan(&ok); err != nil {
			t.Fatalf("%s: query: %v", c.ident, err)
		}
		if !ok {
			t.Errorf("schema.%s = %q, which exists in the database as neither "+
				"a constraint, an index, nor a trigger — error mapping that "+
				"matches on it is dead code", c.ident, c.name)
		}
	}
}

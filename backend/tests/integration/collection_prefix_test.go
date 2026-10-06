//go:build integration

// Regression guard for the multi-segment Collection ingest disambiguation.
// `001_initial_schema.sql` allows multi-segment collections (`invoices/2026/q1`), which
// makes the naive "the OK is the first path segment" split ambiguous. The
// ResolveCollectionPrefix query resolves it by longest registered prefix; this
// pins that precedence against the real schema (the logic is pure SQL, so a
// unit test cannot cover it — handler_disambiguation_test.go covers the Go
// glue separately).
package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

func TestResolveCollectionPrefix_LongestPrefixWins(t *testing.T) {
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "ok-prefix")

	// Two NESTED keys that collide on the `invoices` prefix, plus an unrelated
	// one — all registered for the same tenant.
	// NB: collection_format rejects exactly-2-char path segments (see the
	// BACKLOG "collection 2-char segment" note), so use ≥3-char segments here.
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "invoices")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "invoices/archive/2026")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "assets")

	q := sqlc.New(f.h.PoolMigrate)
	tpg := pgtype.UUID{Bytes: tenant, Valid: true}

	cases := []struct {
		tail string
		want string
	}{
		// The longer registered OK wins over its shorter sibling.
		{"invoices/archive/2026/report.pdf", "invoices/archive/2026"},
		// Only `invoices` prefixes this tail — the nested one does not.
		{"invoices/jan.pdf", "invoices"},
		// Unrelated namespace.
		{"assets/logo.png", "assets"},
		// An exact match (tail == OK) still resolves to that OK.
		{"invoices", "invoices"},
	}
	for _, c := range cases {
		got, err := q.ResolveCollectionPrefix(context.Background(), tpg, c.tail)
		if err != nil {
			t.Fatalf("ResolveCollectionPrefix(%q): %v", c.tail, err)
		}
		if got != c.want {
			t.Errorf("ResolveCollectionPrefix(%q) = %q, want %q", c.tail, got, c.want)
		}
	}

	// A tail no registered OK prefixes → ErrNoRows (handler keeps the source
	// split and the object lookup skips it).
	if _, err := q.ResolveCollectionPrefix(context.Background(), tpg, "ghost/x.bin"); err == nil {
		t.Error("expected ErrNoRows for a tail with no registered OK prefix")
	}
}

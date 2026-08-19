//go:build integration

// Regression guard for the multi-segment ObjectKey ingest disambiguation.
// Migration 030 allows multi-segment object_keys (`invoices/2026/q1`), which
// makes the naive "the OK is the first path segment" split ambiguous. The
// ResolveObjectKeyPrefix query resolves it by longest registered prefix; this
// pins that precedence against the real schema (the logic is pure SQL, so a
// unit test cannot cover it — handler_disambiguation_test.go covers the Go
// glue separately).
package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
)

func TestResolveObjectKeyPrefix_LongestPrefixWins(t *testing.T) {
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "ok-prefix")

	// Two NESTED keys that collide on the `invoices` prefix, plus an unrelated
	// one — all registered for the same tenant.
	// NB: object_key_format rejects exactly-2-char path segments (see the
	// BACKLOG "object_key 2-char segment" note), so use ≥3-char segments here.
	mustCreateObjectKey(t, f.h.PoolMigrate, tenant, "invoices")
	mustCreateObjectKey(t, f.h.PoolMigrate, tenant, "invoices/archive/2026")
	mustCreateObjectKey(t, f.h.PoolMigrate, tenant, "assets")

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
		got, err := q.ResolveObjectKeyPrefix(context.Background(), tpg, c.tail)
		if err != nil {
			t.Fatalf("ResolveObjectKeyPrefix(%q): %v", c.tail, err)
		}
		if got != c.want {
			t.Errorf("ResolveObjectKeyPrefix(%q) = %q, want %q", c.tail, got, c.want)
		}
	}

	// A tail no registered OK prefixes → ErrNoRows (handler keeps the source
	// split and the object lookup skips it).
	if _, err := q.ResolveObjectKeyPrefix(context.Background(), tpg, "ghost/x.bin"); err == nil {
		t.Error("expected ErrNoRows for a tail with no registered OK prefix")
	}
}

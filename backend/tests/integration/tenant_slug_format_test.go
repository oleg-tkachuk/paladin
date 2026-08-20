//go:build integration

// Pins tenants_slug_format after migration 046 added the explicit 3-char floor
// so the DB CHECK matches ValidateTenantSlug (the migration-009 regex alone
// accepted a 1-char slug the API rejects). Pure DB-constraint behaviour, so it
// can only be verified against the real schema.
package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func insertTenantWithSlug(pool *pgxpool.Pool, slug string) error {
	// display_name is UNIQUE (tenants_display_name_unique) — use a fresh value
	// per call so the ONLY constraint that can reject a row is the slug format.
	_, err := pool.Exec(context.Background(),
		`INSERT INTO tenants (id, display_name, slug)
		 VALUES (gen_random_uuid(), gen_random_uuid()::text, $1)`,
		slug)
	return err
}

func TestTenantSlugFormat_MinLength(t *testing.T) {
	f := setupDispatcher(t)

	slug63 := "a" + strings.Repeat("b", 61) + "c" // 63 chars, letter-first

	valid := []string{
		"abc",     // 3-char minimum
		"ab-cd",   // hyphenated
		"acme-eu", // kebab-case
		slug63,    // 63-char max
	}
	for _, s := range valid {
		if err := insertTenantWithSlug(f.h.PoolMigrate, s); err != nil {
			t.Errorf("slug %q should be ACCEPTED; got %v", s, err)
		}
	}

	invalid := []string{
		"",           // empty
		"a",          // 1-char — the migration-046 fix (was accepted by the DB)
		"ab",         // 2-char — intentionally below the 3-char floor
		"ABC",        // uppercase
		"1abc",       // must start with a letter
		"-abc",       // leading hyphen
		"abc-",       // trailing hyphen
		slug63 + "d", // 64 chars — over the max
	}
	for _, s := range invalid {
		if err := insertTenantWithSlug(f.h.PoolMigrate, s); err == nil {
			t.Errorf("slug %q should be REJECTED, but it was accepted", s)
		}
	}
}

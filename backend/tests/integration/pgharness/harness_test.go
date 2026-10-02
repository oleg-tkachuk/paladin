//go:build integration

package pgharness_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

func TestMain(m *testing.M) {
	os.Exit(pgharness.Main(m))
}

// parallelSetups is more tests than `go test` runs at once on most machines,
// so the clones overlap the way a parallel package run does.
const parallelSetups = 24

// appRole is the runtime role the app pools must log in as.
const appRole = "paladin_app"

// Each Setup is a database of its own: what one test writes, another does not
// see, and the app role logs in to it with RLS in force.
func TestSetupIsolatesEachCall(t *testing.T) {
	ctx := context.Background()
	a, b := pgharness.Setup(t), pgharness.Setup(t)

	var dbA, dbB string
	if err := a.PoolMigrate.QueryRow(ctx, `SELECT current_database()`).Scan(&dbA); err != nil {
		t.Fatal(err)
	}
	if err := b.PoolApp.QueryRow(ctx, `SELECT current_database()`).Scan(&dbB); err != nil {
		t.Fatal(err)
	}
	if dbA == dbB {
		t.Fatalf("both harnesses are on %q, want a database each", dbA)
	}

	if _, err := a.PoolMigrate.Exec(ctx,
		`INSERT INTO tenants (slug, display_name) VALUES ('harness-a', 'A')`); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	var inB int
	if err := b.PoolMigrate.QueryRow(ctx,
		`SELECT count(*) FROM tenants WHERE slug = 'harness-a'`).Scan(&inB); err != nil {
		t.Fatal(err)
	}
	if inB != 0 {
		t.Errorf("a row written in %s is visible in %s", dbA, dbB)
	}

	// The app pools log in to the clone as the runtime role, which RLS
	// binds: the role setup is server-wide and done once, so a clone must
	// inherit it rather than fall back to the superuser.
	var user string
	var bypass bool
	if err := a.PoolAppNoGUC.QueryRow(ctx,
		`SELECT current_user, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&user, &bypass); err != nil {
		t.Fatalf("app role: %v", err)
	}
	if user != appRole || bypass {
		t.Errorf("app pool is %q with BYPASSRLS=%v, want %q bound by RLS", user, bypass, appRole)
	}
}

// Parallel Setups clone the template at the same time and hold pools open;
// none may fail for another.
func TestSetupClonesInParallel(t *testing.T) {
	for i := range parallelSetups {
		t.Run(fmt.Sprintf("setup-%d", i), func(t *testing.T) {
			t.Parallel()
			h := pgharness.Setup(t)
			for _, p := range []interface{ Ping(context.Context) error }{h.PoolMigrate, h.PoolApp, h.PoolAppNoGUC} {
				if err := p.Ping(context.Background()); err != nil {
					t.Fatalf("ping: %v", err)
				}
			}
		})
	}
}

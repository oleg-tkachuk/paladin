//go:build integration

package components

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Taint is set and read under row-level security, on the app role: the
// write and the read gate both run in the caller's tenant, and another
// tenant can neither see nor change an object's flags.
func TestObjectTaintUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	admin := startPostgres(t)
	f := seedFixture(t, ctx, admin)
	ids := seedObjects(t, ctx, admin, f, 1, "AVAILABLE")

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewObjectTaintRepo(sqlc.New(pool))
	own := auth.WithPrincipal(ctx, &auth.Principal{Subject: "u", TenantID: f.tenantID})

	if got, err := repo.TaintAtPath(own, f.tenantID, f.collection, "k-1"); err != nil || len(got) != 0 {
		t.Fatalf("fresh object: taint %v, err %v; want clean", got, err)
	}
	stored, err := repo.SetTaint(own, f.tenantID, ids[0], []string{"pii", "secrets"})
	if err != nil || !slices.Equal(stored, []string{"pii", "secrets"}) {
		t.Fatalf("SetTaint = %v, %v", stored, err)
	}
	if got, err := repo.TaintAtPath(own, f.tenantID, f.collection, "k-1"); err != nil || !slices.Equal(got, []string{"pii", "secrets"}) {
		t.Fatalf("TaintAtPath = %v, %v", got, err)
	}
	if got, err := repo.TaintAtPath(own, f.tenantID, f.collection, "no-such-key"); err != nil || got != nil {
		t.Fatalf("missing object: %v, %v; want none", got, err)
	}

	other := uuid.New()
	foreign := auth.WithPrincipal(ctx, &auth.Principal{Subject: "x", TenantID: other})
	if _, err := repo.SetTaint(foreign, f.tenantID, ids[0], nil); err == nil {
		t.Fatal("another tenant cleared the taint")
	}
	if got, _ := repo.TaintAtPath(foreign, f.tenantID, f.collection, "k-1"); got != nil {
		t.Fatalf("another tenant read the taint: %v", got)
	}

	if stored, err := repo.SetTaint(own, f.tenantID, ids[0], nil); err != nil || len(stored) != 0 {
		t.Fatalf("clear = %v, %v", stored, err)
	}
}

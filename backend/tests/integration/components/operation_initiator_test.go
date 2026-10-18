//go:build integration

package components

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// One user's list holds exactly their own operations, in either order; an
// operation from before initiators were recorded is no one's; the tenant's
// list without an initiator holds every one.
func TestListOperations_ByInitiator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	alice, bob := "0199a1b2-alice", "0199a1b2-bob"
	var mine []uuid.UUID
	for i, who := range []string{alice, bob, alice, "", alice} {
		op := operationh.Operation{
			OperationID: uuid.Must(uuid.NewV7()), TenantID: tenantID,
			Type: "BatchDelete", State: operationh.StatePending,
			Metadata: []byte(`{}`), InitiatorSubject: who,
		}
		if err := repo.Create(ctx, op); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if who == alice {
			mine = append(mine, op.OperationID)
		}
	}
	ids := func(ops []operationh.Operation) []uuid.UUID {
		out := make([]uuid.UUID, len(ops))
		for i, o := range ops {
			out[i] = o.OperationID
		}
		return out
	}

	asc, _, err := repo.List(ctx, tenantID, nil, uuid.Nil, 10, "", false, alice)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(asc), mine) {
		t.Errorf("alice's, oldest first = %v, want %v", ids(asc), mine)
	}
	desc, _, err := repo.List(ctx, tenantID, nil, uuid.Nil, 10, "", true, alice)
	if err != nil {
		t.Fatal(err)
	}
	if want := slices.Clone(mine); !slices.Equal(ids(desc), func() []uuid.UUID { slices.Reverse(want); return want }()) {
		t.Errorf("alice's, newest first = %v", ids(desc))
	}
	for _, o := range desc {
		if o.InitiatorSubject != alice {
			t.Errorf("operation %s read back as %q's", o.OperationID, o.InitiatorSubject)
		}
	}
	all, _, err := repo.List(ctx, tenantID, nil, uuid.Nil, 10, "", false, "")
	if err != nil || len(all) != 5 {
		t.Errorf("the tenant's list = %d operations (%v), want all 5", len(all), err)
	}
}

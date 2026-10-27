//go:build integration

package components

import (
	"context"
	"slices"
	"testing"
	"uuid"

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
			OperationID: uuid.NewV7(), TenantID: tenantID,
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

	asc, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: 10, Initiator: alice})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(asc), mine) {
		t.Errorf("alice's, oldest first = %v, want %v", ids(asc), mine)
	}
	desc, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: 10, NewestFirst: true, Initiator: alice})
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
	all, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: 10})
	if err != nil || len(all) != 5 {
		t.Errorf("the tenant's list = %d operations (%v), want all 5", len(all), err)
	}
}

// Dismissing clears only the caller's own finished operations, once; the
// caller's list then leaves them out unless asked, and everyone else's list
// keeps them.
func TestDismissOperations_OwnAndFinishedOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	alice, bob := "0199a1b2-alice", "0199a1b2-bob"
	mk := func(who string, state operationh.State) uuid.UUID {
		t.Helper()
		id := uuid.NewV7()
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, initiator_subject) VALUES ($1, $2, 'BatchDelete', $3, $4)`,
			id, tenantID, string(state), who)
		return id
	}
	done := mk(alice, operationh.StateSucceeded)
	failed := mk(alice, operationh.StateFailed)
	running := mk(alice, operationh.StateRunning)
	bobs := mk(bob, operationh.StateSucceeded)

	n, err := repo.Dismiss(ctx, tenantID, alice, nil)
	if err != nil || n != 2 {
		t.Fatalf("dismissed %d (%v), want alice's two finished ones", n, err)
	}
	if n, _ := repo.Dismiss(ctx, tenantID, alice, nil); n != 0 {
		t.Errorf("dismissing again cleared %d", n)
	}
	// Named, another's operation is left as it is.
	if n, _ := repo.Dismiss(ctx, tenantID, alice, []uuid.UUID{bobs}); n != 0 {
		t.Errorf("alice cleared bob's operation (%d)", n)
	}

	list := func(args operationh.ListArgs) []uuid.UUID {
		t.Helper()
		args.TenantID, args.PageSize = tenantID, 10
		ops, _, err := repo.List(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]uuid.UUID, len(ops))
		for i, o := range ops {
			out[i] = o.OperationID
		}
		return out
	}
	if got := list(operationh.ListArgs{Initiator: alice, HideDismissed: true}); !slices.Equal(got, []uuid.UUID{running}) {
		t.Errorf("alice's list = %v, want only the running one", got)
	}
	if got := list(operationh.ListArgs{Initiator: alice}); !slices.Equal(got, []uuid.UUID{done, failed, running}) {
		t.Errorf("alice's list with dismissed = %v", got)
	}
	if got := list(operationh.ListArgs{}); len(got) != 4 {
		t.Errorf("the tenant's list = %d operations, want all 4", len(got))
	}
}

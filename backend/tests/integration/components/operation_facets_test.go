//go:build integration

package components

import (
	"context"
	"maps"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Operation types the facet test seeds.
const (
	facetDelete = batchh.OpBatchDelete
	facetCopy   = batchh.OpBatchCopy
)

// Each facet is counted under every other constraint and without its own
// choices: choosing a state leaves the state counts as they were and narrows
// the type counts, and the other way round. The listing honours both.
func TestCountOperationFacets_EachWithoutItsOwnChoices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	for _, op := range []struct {
		typ   string
		state operationh.State
	}{
		{facetDelete, operationh.StateFailed},
		{facetDelete, operationh.StateFailed},
		{facetDelete, operationh.StateSucceeded},
		{facetCopy, operationh.StateFailed},
		{facetCopy, operationh.StateRunning},
	} {
		mustExec(t, ctx, pool, `INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, $3, $4)`, uuid.NewV7(), tenantID, op.typ, string(op.state))
	}

	counts := func(f operationh.Facets) map[string]map[string]int64 {
		t.Helper()
		got, err := repo.CountFacets(ctx, operationh.ListArgs{TenantID: tenantID, Facets: f})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]int64{operationh.FacetState: {}, operationh.FacetType: {}}
		for _, c := range got {
			out[c.Facet][c.Value] = c.Count
		}
		return out
	}
	failed, succeeded, running := string(operationh.StateFailed), string(operationh.StateSucceeded), string(operationh.StateRunning)
	allStates := map[string]int64{failed: 3, succeeded: 1, running: 1}
	allTypes := map[string]int64{facetDelete: 3, facetCopy: 2}

	for _, tc := range []struct {
		name          string
		facets        operationh.Facets
		states, types map[string]int64
		listed        int
	}{
		{"none chosen", operationh.Facets{}, allStates, allTypes, 5},
		{"a state", operationh.Facets{States: []operationh.State{operationh.StateFailed}},
			allStates, map[string]int64{facetDelete: 2, facetCopy: 1}, 3},
		{"a type", operationh.Facets{Types: []string{facetCopy}},
			map[string]int64{failed: 1, running: 1}, allTypes, 2},
		{"both", operationh.Facets{States: []operationh.State{operationh.StateFailed}, Types: []string{facetDelete}},
			map[string]int64{failed: 2, succeeded: 1}, map[string]int64{facetDelete: 2, facetCopy: 1}, 2},
	} {
		got := counts(tc.facets)
		if !maps.Equal(got[operationh.FacetState], tc.states) {
			t.Errorf("%s: state counts %v, want %v", tc.name, got[operationh.FacetState], tc.states)
		}
		if !maps.Equal(got[operationh.FacetType], tc.types) {
			t.Errorf("%s: type counts %v, want %v", tc.name, got[operationh.FacetType], tc.types)
		}
		ops, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: 10, Facets: tc.facets})
		if err != nil || len(ops) != tc.listed {
			t.Errorf("%s: listed %d (%v), want %d", tc.name, len(ops), err, tc.listed)
		}
	}

	// The filter is a constraint of its own: the state facet's count keeps it.
	got, err := repo.CountFacets(ctx, operationh.ListArgs{TenantID: tenantID, Filter: `state == "RUNNING"`,
		Facets: operationh.Facets{States: []operationh.State{operationh.StateFailed}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Facet == operationh.FacetState && c.Value != running {
			t.Errorf("a state the filter rules out was counted: %+v", c)
		}
	}
}

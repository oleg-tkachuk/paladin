package operationh

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// An operation has failures when it failed, or when it is a batch that ran to
// its end leaving objects behind; a clean one, or one still running, has none.
func TestHasFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		op   Operation
		want bool
	}{
		"failed":               {Operation{State: StateFailed}, true},
		"a batch left objects": {Operation{State: StateSucceeded, FailedCount: 1}, true},
		"a clean batch":        {Operation{State: StateSucceeded}, false},
		"running":              {Operation{State: StateRunning}, false},
		"cancelled":            {Operation{State: StateCancelled}, false},
	} {
		if got := tc.op.HasFailures(); got != tc.want {
			t.Errorf("%s: HasFailures = %v, want %v", name, got, tc.want)
		}
	}
}

// The filter's has_failures and failed_count read what the operation holds,
// so the CEL pass over a page agrees with the pushdown that fetched it.
func TestListOperationsFiltersByFailures(t *testing.T) {
	tid := uuid.New()
	failed := Operation{OperationID: uuid.New(), TenantID: tid, State: StateFailed}
	partial := Operation{OperationID: uuid.New(), TenantID: tid, State: StateSucceeded, FailedCount: 2}
	clean := Operation{OperationID: uuid.New(), TenantID: tid, State: StateSucceeded}
	fr := &fakeRepo{listFn: func(context.Context, ListArgs) ([]Operation, string, error) {
		return []Operation{failed, partial, clean}, "", nil
	}}
	h := NewHandler(fr, allowAuthorizer())
	for filter, want := range map[string][]uuid.UUID{
		`has_failures`:     {failed.OperationID, partial.OperationID},
		`!has_failures`:    {clean.OperationID},
		`failed_count > 1`: {partial.OperationID},
	} {
		ops, _, err := h.ListOperations(authedCtx(tid), ListInput{PageSize: 10, Filter: filter})
		if err != nil {
			t.Fatalf("%s: %v", filter, err)
		}
		var got []uuid.UUID
		for _, o := range ops {
			got = append(got, o.OperationID)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: listed %v, want %v", filter, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: listed %v, want %v", filter, got, want)
			}
		}
	}
}

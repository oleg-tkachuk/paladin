//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A cancel that changes no row tells the absent operation apart from the
// finished one: the first is not found, the second not cancellable. Another
// tenant's operation is absent to the caller.
func TestCancelOperation_MissingVersusFinished(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	otherID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	op := operationh.Operation{
		OperationID: uuid.NewV7(), TenantID: tenantID,
		Type: "BatchDelete", State: operationh.StatePending, Metadata: []byte(`{}`),
	}
	if err := repo.Create(ctx, op); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Cancel(ctx, op.OperationID, tenantID); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	for _, tc := range []struct {
		name    string
		id, tid uuid.UUID
		want    error
	}{
		{"already cancelled", op.OperationID, tenantID, operationh.ErrNotCancellable},
		{"no such operation", uuid.NewV7(), tenantID, operationh.ErrOperationNotFound},
		{"another tenant's", op.OperationID, otherID, operationh.ErrOperationNotFound},
	} {
		if err := repo.Cancel(ctx, tc.id, tc.tid); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

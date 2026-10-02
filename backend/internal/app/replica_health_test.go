package app

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
)

// With no replica the health page still shows the row, marked disabled, and
// it can never fail: there is nothing to be healthy or not.
func TestReplicaCheckDisabledWithoutReplica(t *testing.T) {
	db := &postgres.DB{Pool: &pgxpool.Pool{}, Reads: postgres.NewPrimaryOnlyRouter(&pgxpool.Pool{})}
	c := replicaCheck(db)
	if c.Name != "postgres-replica" || c.Category != health.CategoryDatabase || c.Critical {
		t.Fatalf("check = %+v; want a non-critical database row named postgres-replica", c)
	}
	if c.Note != "disabled" {
		t.Errorf("note = %q, want disabled", c.Note)
	}
	if err := c.Func(context.Background()); err != nil {
		t.Errorf("disabled replica check failed: %v", err)
	}
}

package app

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
)

// With no replica the health page still shows the row, as disabled: there is
// nothing to be healthy or not, so it is not run.
func TestReplicaCheckDisabledWithoutReplica(t *testing.T) {
	db := &postgres.DB{Pool: &pgxpool.Pool{}, Reads: postgres.NewPrimaryOnlyRouter(&pgxpool.Pool{})}
	c := replicaCheck(db)
	if c.Name != "postgres-replica" || c.Category != health.CategoryDatabase || c.Critical {
		t.Fatalf("check = %+v; want a non-critical database row named postgres-replica", c)
	}
	if !c.Disabled {
		t.Error("replica check is not disabled without a replica")
	}
}

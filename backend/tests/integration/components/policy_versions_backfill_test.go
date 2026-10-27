//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// beforeWriterTenantBackfill is the last migration under which a version
// could hold a writer but not its tenant; 103 fills the tenant in.
const beforeWriterTenantBackfill = 102

// Migrating fills in the writer's tenant from the users table for the
// versions that name a user, and leaves every other version as it was.
func TestPolicyWriterTenantIsBackfilled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgresAt(t, beforeWriterTenantBackfill)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	f := seedFixture(t, ctx, pool)

	userID := uuid.NewV7()
	mustExec(t, ctx, pool,
		`INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'writer')`, userID, f.tenantID)
	recorded := uuid.NewV7()

	owner := uuid.NewV7()
	insert := func(version int, writer, writerTenant any) {
		t.Helper()
		mustExec(t, ctx, pool, `
			INSERT INTO policy_versions (layer, owner_id, tenant_id, version, cedar_policy, written_by, written_by_tenant_id)
			VALUES ('tenant', $1, $2, $3, 'permit (principal, action, resource);', $4, $5)`,
			owner, f.tenantID, version, writer, writerTenant)
	}
	const (
		byUser = iota + 1
		byAgent
		byNobody
		alreadyRecorded
	)
	insert(byUser, userID.String(), nil)
	insert(byAgent, "agent:ci", nil)
	insert(byNobody, nil, nil)
	insert(alreadyRecorded, userID.String(), recorded)

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	want := map[int]*uuid.UUID{
		byUser:          &f.tenantID,
		byAgent:         nil,
		byNobody:        nil,
		alreadyRecorded: &recorded,
	}
	for version, w := range want {
		var got *uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT written_by_tenant_id FROM policy_versions WHERE owner_id = $1 AND version = $2`,
			owner, version).Scan(&got); err != nil {
			t.Fatalf("read version %d: %v", version, err)
		}
		if (got == nil) != (w == nil) || (got != nil && *got != *w) {
			t.Errorf("version %d writer tenant = %v, want %v", version, got, w)
		}
	}
}

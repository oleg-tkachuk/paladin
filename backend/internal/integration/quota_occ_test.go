//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// SetQuota upserted without consulting quotas.resource_version, which the
// schema has carried since the initial migration. Two operators editing the
// same tenant's limits raced: last write won, the loser was told it succeeded,
// and the caps silently became whichever request landed second.
//
// The guard lives in the upsert's DO UPDATE clause, so this exercises the SQL
// rather than a Go branch — the version compare has to happen in the same
// statement as the write, or it is a TOCTOU with extra steps.
func TestQuotaUpsertOCC(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewQuotaRepoV2(sqlc.New(pool), pool)

	base := admindomain.Quota{
		TenantID:       f.tenantID,
		MaxTotalBytes:  1 << 30,
		MaxObjectCount: 1000,
	}

	t.Run("first write creates at version 0", func(t *testing.T) {
		q := base
		q.ResourceVersion = 0
		if err := repo.UpsertTenant(ctx, q); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := repo.GetTenant(ctx, f.tenantID)
		if err != nil {
			t.Fatalf("GetTenant: %v", err)
		}
		if got.MaxTotalBytes != base.MaxTotalBytes {
			t.Errorf("max_total_bytes = %d, want %d", got.MaxTotalBytes, base.MaxTotalBytes)
		}
	})

	t.Run("creating twice at version 0 is a conflict", func(t *testing.T) {
		q := base
		q.ResourceVersion = 0
		q.MaxTotalBytes = 999
		err := repo.UpsertTenant(ctx, q)
		if !errors.Is(err, admindomain.ErrVersionMismatch) {
			t.Fatalf("err = %v, want ErrVersionMismatch — 0 asserts the row does not exist", err)
		}
		got, _ := repo.GetTenant(ctx, f.tenantID)
		if got.MaxTotalBytes == 999 {
			t.Error("the rejected write landed anyway")
		}
	})

	t.Run("update with the current version succeeds and bumps it", func(t *testing.T) {
		cur, err := repo.GetTenant(ctx, f.tenantID)
		if err != nil {
			t.Fatalf("GetTenant: %v", err)
		}
		q := base
		q.ResourceVersion = cur.ResourceVersion
		q.MaxTotalBytes = 2 << 30
		if err := repo.UpsertTenant(ctx, q); err != nil {
			t.Fatalf("update: %v", err)
		}
		after, err := repo.GetTenant(ctx, f.tenantID)
		if err != nil {
			t.Fatalf("GetTenant: %v", err)
		}
		if after.MaxTotalBytes != 2<<30 {
			t.Errorf("max_total_bytes = %d, want %d", after.MaxTotalBytes, 2<<30)
		}
		if after.ResourceVersion <= cur.ResourceVersion {
			t.Errorf("resource_version = %d, want > %d — a successful write must advance it, "+
				"or the guard never rejects anything", after.ResourceVersion, cur.ResourceVersion)
		}
	})

	t.Run("stale version is rejected and changes nothing", func(t *testing.T) {
		cur, err := repo.GetTenant(ctx, f.tenantID)
		if err != nil {
			t.Fatalf("GetTenant: %v", err)
		}
		q := base
		q.ResourceVersion = cur.ResourceVersion - 1 // what a concurrent writer held
		q.MaxTotalBytes = 4 << 30
		if err := repo.UpsertTenant(ctx, q); !errors.Is(err, admindomain.ErrVersionMismatch) {
			t.Fatalf("err = %v, want ErrVersionMismatch", err)
		}
		after, _ := repo.GetTenant(ctx, f.tenantID)
		if after.MaxTotalBytes == 4<<30 {
			t.Error("stale write overwrote the current caps")
		}
		if after.ResourceVersion != cur.ResourceVersion {
			t.Errorf("resource_version moved on a rejected write: %d → %d",
				cur.ResourceVersion, after.ResourceVersion)
		}
	})
}

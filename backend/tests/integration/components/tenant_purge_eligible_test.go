//go:build integration

package components

import (
	"context"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	tenantapi "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The purge_eligible pushdown and its count agree with tenanth.PurgeEligible
// on every row, the one trashed exactly a retention ago included: the pushdown
// may only narrow, so a boundary the SQL drew one instant apart from Go would
// drop a tenant the console marks "Past retention".
func TestPurgeEligible_SQLMatchesTheProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	const retention = 30 * 24 * time.Hour
	// Microsecond-aligned: Postgres keeps timestamps to the microsecond.
	asOf := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	trashedAt := map[string]*time.Time{
		"pe-active":   nil,
		"pe-fresh":    new(asOf.Add(-time.Hour)),
		"pe-boundary": new(asOf.Add(-retention)),
		"pe-past":     new(asOf.Add(-retention - time.Hour)),
	}
	want := map[string]bool{}
	for slug, deleted := range trashedAt {
		id := uuid.New()
		mustExec(t, ctx, pool,
			`INSERT INTO tenants (id, slug, display_name, deleted_at) VALUES ($1, $2, $3, $4)`,
			id, slug, slug+" tenant", deleted)
		tn := tenantapi.Tenant{}
		if deleted != nil {
			tn.DeletedAt = *deleted
		}
		want[slug] = tenantapi.PurgeEligible(tn, retention, asOf)
	}

	got, _, err := repo.List(ctx, tenantapi.ListTenantsArgs{
		PageSize: 50, IncludeTrashed: true, Filter: "purge_eligible",
		TrashRetention: retention, AsOf: asOf,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var listed []string
	for _, tn := range got {
		listed = append(listed, tn.Slug)
	}
	slices.Sort(listed)
	if !slices.Equal(listed, []string{"pe-boundary", "pe-past"}) {
		t.Errorf("pushed down %v, want the boundary and past tenants", listed)
	}
	for slug, eligible := range want {
		if eligible != slices.Contains(listed, slug) {
			t.Errorf("%s: SQL says %v, the projection says %v", slug, !eligible, eligible)
		}
	}

	n, err := sqlc.New(pool).CountTenantsPastTrashRetention(ctx, retention.Seconds(), pgtype.Timestamptz{Time: asOf, Valid: true})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != int64(len(listed)) {
		t.Errorf("count = %d, want %d", n, len(listed))
	}
}

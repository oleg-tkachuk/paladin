//go:build integration

package components

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// limesSchema is the schema pgstore keeps its tables in, and tenantIDColumn
// the column each of its tables keeps a row's tenant in.
const (
	limesSchema    = "limes"
	tenantIDColumn = "tenant_id"
)

// limesTenantTables are the limes schema's tables that keep a tenant's rows,
// each of which seedEveryLimesTable fills.
var limesTenantTables = []string{
	"biscuit_revocations", "capabilities", "charges", "copy_usage",
	"reservations", "revocations", "tenant_budgets", "usage",
}

// seedEveryLimesTable gives tenant a row in every table of limesTenantTables
// through the stores, acting on the tenant on the application role as a
// request does: a capability, its revocation and that of a Biscuit copy, the
// counters of both, a charge, a hold and a budget.
func seedEveryLimesTable(t *testing.T, ctx context.Context, f *biscuitFixture, records *pgstore.Store, usage *capstore.UsageStore, tenant uuid.UUID) {
	t.Helper()
	acting := auth.WithActingTenant(ctx, tenant)
	sealed, c := f.seal(t, tenant, "agent:purged")
	copyToken, err := limes.Attenuate(sealed, limes.Attenuation{Requests: copyRequests(lineageRequests.Max)})
	if err != nil {
		t.Fatalf("attenuate: %v", err)
	}
	verified, err := f.verifier.Verify(ctx, limes.VerifyRequest{Token: copyToken, Audience: biscuitAudience})
	if err != nil {
		t.Fatalf("verify copy: %v", err)
	}
	subject := verified.MeterSubject()
	if len(subject.Copies()) != 1 {
		t.Fatalf("the copy carries %d limits, want 1", len(subject.Copies()))
	}

	if _, err := usage.SetTenantBudget(acting, limes.SetTenantBudgetRequest{
		TenantID: tenant, Ceiling: limes.BudgetCeiling{Unlimited: true},
	}); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if _, err := usage.Bump(acting, limes.BumpRequest{Subject: subject}); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if _, err := usage.Charge(acting, limes.ChargeRequest{Subject: subject, Amount: limes.NanosPerUnit}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := usage.Reserve(acting, limes.ReserveRequest{Subject: subject, Amount: limes.NanosPerUnit}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := records.RevokeBiscuit(acting, limes.RevokeBiscuitRequest{Copy: f.copyOf(t, copyToken)}); err != nil {
		t.Fatalf("revoke copy: %v", err)
	}
	if err := records.Revoke(acting, limes.RevokeRequest{CapabilityID: c.ID}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}

// limesRows counts tenant's rows in each of limesTenantTables, past RLS.
func limesRows(t *testing.T, ctx context.Context, admin *pgxpool.Pool, tenant uuid.UUID) map[string]int {
	t.Helper()
	rows := map[string]int{}
	for _, table := range limesTenantTables {
		var n int
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM `+pgx.Identifier{limesSchema, table}.Sanitize()+
				` WHERE `+pgx.Identifier{tenantIDColumn}.Sanitize()+` = $1`, tenant,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		rows[table] = n
	}
	return rows
}

// The limes schema keeps no foreign key to tenants, so purging a tenant takes
// its capabilities, meter and budget only because HardDeleteTx deletes them
// by name. This holds that list to the schema pgstore actually migrates: every
// table with a tenant_id is seeded, the purged tenant leaves none of its rows
// in any, and another tenant's stay. It runs on the application role, so the
// purge must scope itself to the tenant it deletes whoever asks.
func TestPurgingATenantDeletesItsCapabilityRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)

	rows, err := admin.Query(ctx,
		`SELECT table_name FROM information_schema.columns
		 WHERE table_schema = $1 AND column_name = $2 ORDER BY table_name`, limesSchema, tenantIDColumn)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tables, limesTenantTables) {
		t.Fatalf("the limes schema keeps tenants' rows in %v; this test seeds %v — seed the new table and check DeleteTenant clears it", tables, limesTenantTables)
	}

	f := newBiscuitFixture(t, admin)
	pool := rlsPool(t, ctx, admin)
	records := newCapStore(t, pool)
	usage := newUsageStore(t, pool)
	purged, _ := mkTenant(t, ctx, admin, "shared")
	bystander, _ := mkTenant(t, ctx, admin, "shared")
	platform, _ := mkTenant(t, ctx, admin, "shared")
	for _, tenant := range []uuid.UUID{purged, bystander} {
		seedEveryLimesTable(t, ctx, f, records, usage, tenant)
	}
	for table, n := range limesRows(t, ctx, admin, purged) {
		if n == 0 {
			t.Fatalf("the seed wrote no %s row, so the purge of it proves nothing", table)
		}
	}
	kept := limesRows(t, ctx, admin, bystander)

	// As PurgeTenant runs it: the tenant in the trash, the platform admin's
	// principal on the context.
	mustExec(t, ctx, admin, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, purged)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	adminCtx := auth.WithPrincipal(ctx, &auth.Principal{TenantID: platform})
	if err := repo.RunInTx(adminCtx, func(ctx context.Context, tx pgx.Tx) error {
		return repo.HardDeleteTx(ctx, tx, purged, 0)
	}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	for table, n := range limesRows(t, ctx, admin, purged) {
		if n != 0 {
			t.Errorf("limes.%s keeps %d rows of the purged tenant", table, n)
		}
	}
	if after := limesRows(t, ctx, admin, bystander); !maps.Equal(after, kept) {
		t.Errorf("another tenant's rows moved: %v, were %v", after, kept)
	}
}

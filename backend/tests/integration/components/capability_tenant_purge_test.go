//go:build integration

package components

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
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

	if _, err := usage.SetTenantBudget(acting, meter.SetTenantBudgetRequest{
		TenantID: tenant, Ceiling: limes.BudgetCeiling{Unlimited: true, UnitCode: testUnit},
	}); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if _, err := usage.Bump(acting, meter.BumpRequest{Subject: subject}); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if _, err := usage.Charge(acting, meter.ChargeRequest{Subject: subject, Amount: limes.NanosPerUnit}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := usage.Reserve(acting, meter.ReserveRequest{Subject: subject, Amount: limes.NanosPerUnit}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := records.RevokeBiscuit(acting, limes.RevokeBiscuitRequest{Copy: f.copyOf(t, copyToken)}); err != nil {
		t.Fatalf("revoke copy: %v", err)
	}
	if _, err := records.Revoke(acting, limes.RevokeRequest{CapabilityID: c.ID}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}

// limesEraseMarks is the limes table an erase writes a tenant into rather
// than out of: the mark that refuses metering a tenant whose usage is gone
// until the purger finds none of its records left.
const limesEraseMarks = "erased_tenants"

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

// limesMeterTables are the tables of limesTenantTables that keep a tenant's
// meter: Purge erases all of them. The rest keep its records and their
// revocations, which stay, revoked, until the purger takes them.
var limesMeterTables = []string{"charges", "copy_usage", "reservations", "tenant_budgets", "usage"}

// expiredLongAgo is when the purged tenant's expired capability lapsed:
// further back than limes.MinPurgeRetention, so the erase removes it.
const expiredLongAgo = -2 * limes.MinPurgeRetention

// The limes schema keeps no foreign key to tenants, so purging a tenant
// erases its rows there through pgstore (EraseTenant), with the tenant row in
// the same transaction. Every table with a tenant_id is seeded; the purged
// tenant keeps no meter row, every capability of it is revoked — a live one
// stays on record so its token is refused, an expired one goes — and another
// tenant's rows stay. It runs on the application role, so the purge must
// scope itself to the tenant it deletes whoever asks.
func TestPurgingATenantErasesItsCapabilityRows(t *testing.T) {
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
	want := append(slices.Clone(limesTenantTables), limesEraseMarks)
	slices.Sort(want)
	if !slices.Equal(tables, want) {
		t.Fatalf("the limes schema keeps tenants' rows in %v; this test knows %v — seed the new table and check Purge erases it", tables, want)
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
	live := mkCap(purged, "agent:live", time.Now().Add(time.Hour))
	expired := mkCap(purged, "agent:expired", time.Now().Add(expiredLongAgo))
	for _, c := range []limes.Capability{live, expired} {
		if err := records.Insert(auth.WithActingTenant(ctx, purged), limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
			t.Fatalf("insert %s: %v", c.Subject.Subject, err)
		}
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
	if err := repo.Purge(adminCtx, tenanth.PurgeTenantArgs{TenantID: purged, Actor: "admin"}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var tenants int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id = $1`, purged).Scan(&tenants); err != nil {
		t.Fatal(err)
	}
	if tenants != 0 {
		t.Error("the tenant row outlived its purge")
	}
	after := limesRows(t, ctx, admin, purged)
	for _, table := range limesMeterTables {
		if after[table] != 0 {
			t.Errorf("limes.%s keeps %d rows of the purged tenant", table, after[table])
		}
	}
	var unrevoked int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM limes.capabilities c
		WHERE c.tenant_id = $1 AND NOT EXISTS (SELECT 1 FROM limes.revocations r WHERE r.capability_id = c.id)`,
		purged).Scan(&unrevoked); err != nil {
		t.Fatal(err)
	}
	if unrevoked != 0 {
		t.Errorf("%d capabilities of the purged tenant are not revoked", unrevoked)
	}
	if revoked, err := records.IsRevoked(ctx, live.ID); err != nil || !revoked {
		t.Errorf("the live capability of the purged tenant: revoked = %v, %v; want its token refused", revoked, err)
	}
	if _, err := records.GetRecord(auth.WithCrossTenantRead(ctx), expired.ID); !errors.Is(err, limes.ErrNotFound) {
		t.Errorf("the expired capability of the purged tenant reads back (%v); the erase removes what Purge would", err)
	}
	if after := limesRows(t, ctx, admin, bystander); !maps.Equal(after, kept) {
		t.Errorf("another tenant's rows moved: %v, were %v", after, kept)
	}
}

// A purge the database refuses — the tenant still has a user, which the
// users foreign key restricts — erases nothing: its capabilities stay
// unrevoked and its meter whole, since the erase and the tenant row share
// one transaction.
func TestARefusedPurgeErasesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	f := newBiscuitFixture(t, admin)
	pool := rlsPool(t, ctx, admin)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	seedEveryLimesTable(t, ctx, f, newCapStore(t, pool), newUsageStore(t, pool), tenant)
	live := mkCap(tenant, "agent:live", time.Now().Add(time.Hour))
	records := newCapStore(t, pool)
	if err := records.Insert(auth.WithActingTenant(ctx, tenant), limes.InsertRequest{Capability: live, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	mustExec(t, ctx, admin,
		`INSERT INTO users (tenant_id, subject, password_hash, roles) VALUES ($1, 'holder', 'x', '{}')`, tenant)
	before := limesRows(t, ctx, admin, tenant)

	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	if err := repo.Purge(ctx, tenanth.PurgeTenantArgs{TenantID: tenant, Actor: "admin"}); !errors.Is(err, tenanth.ErrTenantHasChildren) {
		t.Fatalf("purge of a tenant with a user: err = %v, want ErrTenantHasChildren", err)
	}
	if after := limesRows(t, ctx, admin, tenant); !maps.Equal(after, before) {
		t.Errorf("a refused purge moved the tenant's rows: %v, were %v", after, before)
	}
	if revoked, err := records.IsRevoked(ctx, live.ID); err != nil || revoked {
		t.Errorf("a refused purge revoked a capability: revoked = %v, %v", revoked, err)
	}
}

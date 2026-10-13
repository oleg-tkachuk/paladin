//go:build integration

package components

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

// capabilityJSONMigration rewrites the caveats and principals limes v0.2.0
// wrote into the spelling v0.4.0 reads.
const capabilityJSONMigration = "058_capability_json_v4.sql"

// gooseDown starts a migration's Down section; its Up section precedes it.
const gooseDown = "-- +goose Down"

// v2Caveats is mkCap's caveats as limes v0.2.0 marshalled them: field names
// as keys, the amount a JSON number of units.
const v2Caveats = `{"Ops":["get","list"],"ResourcePrefixes":null,"ResourceURIs":["paladin://docs/"],` +
	`"MaxRequests":42,"MaxBudgetAmount":12.5,"UnitCode":"USD","AllowTaintedRead":true,` +
	`"IdempotencyKeyRequired":true,"SourceIPCIDR":["10.0.0.0/8"]}`

// applyUp runs the Up section of a migration file again, on rows the test
// has put back into the shape the migration rewrites.
func applyUp(t *testing.T, ctx context.Context, admin *pgxpool.Pool, file string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	up, _, found := strings.Cut(string(raw), gooseDown)
	if !found {
		t.Fatalf("%s has no %q", file, gooseDown)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatalf("apply %s: %v", file, err)
	}
}

// v2Principal is a principal as limes v0.2.0 marshalled it.
func v2Principal(p limes.Principal) string {
	return fmt.Sprintf(`{"Type":%q,"TenantID":%q,"Subject":%q,"Agent":null}`, p.Type, p.TenantID, p.Subject)
}

// A capability stored by limes v0.2.0 reads back whole after the migration,
// and its ceilings are in the columns the charge path reads.
func TestCapabilityJSONMigrationRewritesV2Rows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	records := newCapStore(t, admin)
	want := mkCap(tenant, "agent:migrated", time.Now().Add(time.Hour))
	if err := records.Insert(ctx, limes.InsertRequest{Capability: want, IssuedBy: seedIssuer}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
UPDATE capability_records
SET caveats = $2::jsonb, principal_payload = $3::jsonb, issued_by = $4::jsonb,
    max_budget = NULL, max_requests = NULL
WHERE id = $1`, want.ID, v2Caveats, v2Principal(want.Subject), v2Principal(seedIssuer)); err != nil {
		t.Fatal(err)
	}

	applyUp(t, ctx, admin, capabilityJSONMigration)

	rec, err := records.GetRecord(ctx, want.ID)
	if err != nil {
		t.Fatalf("read the migrated row: %v", err)
	}
	if !reflect.DeepEqual(rec.Capability.Caveats, want.Caveats) {
		t.Errorf("caveats = %+v, want %+v", rec.Capability.Caveats, want.Caveats)
	}
	if !reflect.DeepEqual(rec.Capability.Subject, want.Subject) || !reflect.DeepEqual(rec.IssuedBy, seedIssuer) {
		t.Errorf("principals = %+v issued by %+v, want %+v issued by %+v",
			rec.Capability.Subject, rec.IssuedBy, want.Subject, seedIssuer)
	}
	var budget string
	var requests int64
	if err := admin.QueryRow(ctx, `SELECT max_budget::text, max_requests FROM capability_records WHERE id = $1`,
		want.ID).Scan(&budget, &requests); err != nil {
		t.Fatal(err)
	}
	if got, err := limes.ParseAmount(budget); err != nil || got != want.Caveats.Budget.Max || requests != want.Caveats.Requests.Max {
		t.Errorf("ceilings = %s, %d; want %s, %s", budget, requests, want.Caveats.Budget, want.Caveats.Requests)
	}
	var stored map[string]any
	if err := admin.QueryRow(ctx, `SELECT caveats FROM capability_records WHERE id = $1`, want.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if _, old := stored["Ops"]; old {
		t.Errorf("the old spelling is still stored: %v", stored)
	}
}

// explicitCeilingsMigration rewrites the ceilings written when 0 meant "no
// ceiling" into the explicit form limes v0.27 reads.
const explicitCeilingsMigration = "066_explicit_ceilings.sql"

// v4Unbounded is caveats as limes v0.4 wrote them for a capability with no
// ceilings: max_requests and max_budget absent, which then meant none.
const v4Unbounded = `{"ops":["get"],"unit_code":"USD"}`

// v4ZeroBudget is caveats with a ceiling spelled 0, which then meant none too.
const v4ZeroBudget = `{"ops":["get"],"max_requests":5,"max_budget":"0","unit_code":"USD"}`

// heldCopyBudget is the copy budget, in nanos, of the hold the migration must
// leave alone; the other copy's 0 meant "none of its own".
const heldCopyBudget = 5

// Every ceiling written as 0 for "none" reads as unlimited after 066, and no
// bounded one changes: a record's caveats and columns, a tenant's aggregate,
// a hold's copy budgets.
func TestExplicitCeilingsMigrationLiftsZeroCeilings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	records := newCapStore(t, admin)

	unbounded := mkCap(tenant, "agent:unbounded", time.Now().Add(time.Hour))
	zeroBudget := mkCap(tenant, "agent:zero-budget", time.Now().Add(time.Hour))
	bounded := mkCap(tenant, "agent:bounded", time.Now().Add(time.Hour))
	for _, c := range []limes.Capability{unbounded, zeroBudget, bounded} {
		if err := records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
			t.Fatal(err)
		}
	}
	for id, caveats := range map[uuid.UUID]string{unbounded.ID: v4Unbounded, zeroBudget.ID: v4ZeroBudget} {
		mustExec(t, ctx, admin, `
UPDATE capability_records
SET caveats = $2::jsonb,
    max_budget = COALESCE(($2::jsonb->>'max_budget')::numeric, 0),
    max_requests = COALESCE(($2::jsonb->>'max_requests')::bigint, 0)
WHERE id = $1`, id, caveats)
	}
	mustExec(t, ctx, admin, `INSERT INTO tenant_budgets (tenant_id, max_budget_usd, spent_usd, unit_code, period_start)
VALUES ($1, 0, 1, 'USD', now())`, tenant)
	hold := uuid.New()
	mustExec(t, ctx, admin, `INSERT INTO capability_reservations
    (id, tenant_id, capability_id, amount, unit_code, expires_at, copy_ids, copy_max_budget_nanos)
VALUES ($1, $2, $3, 1, 'USD', now() + interval '1 hour', ARRAY['\x01'::bytea, '\x02'::bytea], ARRAY[0, $4]::bigint[])`,
		hold, tenant, bounded.ID, heldCopyBudget)

	applyUp(t, ctx, admin, explicitCeilingsMigration)

	unlimitedRequests, unlimitedBudget := limes.RequestCeiling{Unlimited: true}, limes.BudgetCeiling{Unlimited: true}
	for _, tc := range []struct {
		name     string
		id       uuid.UUID
		requests limes.RequestCeiling
		budget   limes.BudgetCeiling
	}{
		{"absent ceilings", unbounded.ID, unlimitedRequests, unlimitedBudget},
		{"a budget of 0", zeroBudget.ID, limes.RequestCeiling{Max: 5}, unlimitedBudget},
		{"bounded ceilings", bounded.ID, bounded.Caveats.Requests, bounded.Caveats.Budget},
	} {
		rec, err := records.GetRecord(ctx, tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := rec.Capability.Caveats; got.Requests != tc.requests || got.Budget != tc.budget {
			t.Errorf("%s: caveats ceilings = %s, %s; want %s, %s", tc.name, got.Requests, got.Budget, tc.requests, tc.budget)
		}
		var budget, requests *string
		if err := admin.QueryRow(ctx, `SELECT max_budget::text, max_requests::text FROM capability_records WHERE id = $1`,
			tc.id).Scan(&budget, &requests); err != nil {
			t.Fatal(err)
		}
		if (budget == nil) != tc.budget.Unlimited || (requests == nil) != tc.requests.Unlimited {
			t.Errorf("%s: columns max_budget %v max_requests %v, want NULL exactly where unlimited", tc.name, budget, requests)
		}
	}

	var tenantCeiling *string
	if err := admin.QueryRow(ctx, `SELECT max_budget_usd::text FROM tenant_budgets WHERE tenant_id = $1`, tenant).Scan(&tenantCeiling); err != nil {
		t.Fatal(err)
	}
	if tenantCeiling != nil {
		t.Errorf("tenant ceiling = %s, want NULL (unlimited)", *tenantCeiling)
	}
	var copyBudgets []*int64
	if err := admin.QueryRow(ctx, `SELECT copy_max_budget_nanos FROM capability_reservations WHERE id = $1`, hold).Scan(&copyBudgets); err != nil {
		t.Fatal(err)
	}
	if len(copyBudgets) != 2 || copyBudgets[0] != nil || copyBudgets[1] == nil || *copyBudgets[1] != heldCopyBudget {
		t.Errorf("copy budgets = %v, want [NULL %d]", copyBudgets, heldCopyBudget)
	}
}

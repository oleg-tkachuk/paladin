//go:build integration

package components

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if err := records.Insert(ctx, want, seedIssuer); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
UPDATE capability_records
SET caveats = $2::jsonb, principal_payload = $3::jsonb, issued_by = $4::jsonb,
    max_budget = NULL, max_requests = NULL
WHERE id = $1`, want.ID, v2Caveats, v2Principal(want.Subject), v2Principal(seedIssuer)); err != nil {
		t.Fatal(err)
	}

	raw, err := migrations.FS.ReadFile(capabilityJSONMigration)
	if err != nil {
		t.Fatal(err)
	}
	up, _, found := strings.Cut(string(raw), gooseDown)
	if !found {
		t.Fatalf("%s has no %q", capabilityJSONMigration, gooseDown)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatalf("apply %s: %v", capabilityJSONMigration, err)
	}

	rec, err := records.GetRecord(ctx, want.ID)
	if err != nil {
		t.Fatalf("read the migrated row: %v", err)
	}
	if !reflect.DeepEqual(rec.Capability.Caveats, want.Caveats) {
		t.Errorf("caveats = %+v, want %+v", rec.Capability.Caveats, want.Caveats)
	}
	if rec.Capability.Subject != want.Subject || rec.IssuedBy != seedIssuer {
		t.Errorf("principals = %+v issued by %+v, want %+v issued by %+v",
			rec.Capability.Subject, rec.IssuedBy, want.Subject, seedIssuer)
	}
	var budget string
	var requests int64
	if err := admin.QueryRow(ctx, `SELECT max_budget::text, max_requests FROM capability_records WHERE id = $1`,
		want.ID).Scan(&budget, &requests); err != nil {
		t.Fatal(err)
	}
	if got, err := limes.ParseAmount(budget); err != nil || got != want.Caveats.MaxBudgetAmount || requests != want.Caveats.MaxRequests {
		t.Errorf("ceilings = %s, %d; want %v, %d", budget, requests, want.Caveats.MaxBudgetAmount, want.Caveats.MaxRequests)
	}
	var stored map[string]any
	if err := admin.QueryRow(ctx, `SELECT caveats FROM capability_records WHERE id = $1`, want.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if _, old := stored["Ops"]; old {
		t.Errorf("the old spelling is still stored: %v", stored)
	}
}

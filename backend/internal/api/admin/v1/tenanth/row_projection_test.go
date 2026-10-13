package tenanth

import (
	"testing"
	"time"

	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// A filter compiles against the SCHEMA and evaluates against the PROJECTION.
// A schema field with no key in the projection produces an expression that
// compiles, runs, and matches nothing — every row, every request, no error.
// An empty page is a legitimate answer, so nobody can tell the difference.
// This ties the two halves together so the gap cannot open quietly.
func TestTenantRowCoversEverySchemaField(t *testing.T) {
	row := tenantRow(Tenant{Slug: "acme"}, 0, time.Time{})

	for _, field := range celpkg.TenantSchema.Fields() {
		if _, ok := row[field]; !ok {
			t.Errorf("TenantSchema declares %q and tenantRow does not supply it — a filter on that field silently matches nothing", field)
		}
	}
}

// The milder direction: a projected key the schema does not declare is dead
// weight no filter can reference, and usually means one side was updated alone.
func TestTenantRowProjectsNothingUndeclared(t *testing.T) {
	row := tenantRow(Tenant{Slug: "acme"}, 0, time.Time{})
	declared := map[string]bool{}
	for _, f := range celpkg.TenantSchema.Fields() {
		declared[f] = true
	}

	for key := range row {
		if !declared[key] {
			t.Errorf("tenantRow projects %q, which TenantSchema does not declare", key)
		}
	}
}

// purge_eligible holds from the instant the retention lapses — the boundary
// is inclusive, as ListTenants' SQL spells it — and never for an active
// tenant or with no retention configured.
func TestPurgeEligible(t *testing.T) {
	const retention = 30 * 24 * time.Hour
	trashed := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	lapse := trashed.Add(retention)
	for name, tc := range map[string]struct {
		tenant    Tenant
		retention time.Duration
		asOf      time.Time
		want      bool
	}{
		"active":                {Tenant{}, retention, lapse, false},
		"trashed, within":       {Tenant{DeletedAt: trashed}, retention, lapse.Add(-time.Nanosecond), false},
		"trashed, at the lapse": {Tenant{DeletedAt: trashed}, retention, lapse, true},
		"trashed, past":         {Tenant{DeletedAt: trashed}, retention, lapse.Add(time.Hour), true},
		"no retention":          {Tenant{DeletedAt: trashed}, 0, lapse.Add(time.Hour), false},
	} {
		if got := PurgeEligible(tc.tenant, tc.retention, tc.asOf); got != tc.want {
			t.Errorf("%s: PurgeEligible = %v, want %v", name, got, tc.want)
		}
		if got := tenantRow(tc.tenant, tc.retention, tc.asOf)["purge_eligible"]; got != tc.want {
			t.Errorf("%s: projected purge_eligible = %v, want %v", name, got, tc.want)
		}
	}
}

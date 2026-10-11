package migrations

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// capabilityJSONMigration rewrites stored caveats and principals into the
// JSON limes writes; this test holds its spelling to limes's.
const capabilityJSONMigration = "058_capability_json_v4.sql"

// jsonKeys is the set of keys v marshals to, every field set so none is
// omitted, and the keys of the objects nested in it.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	var walk func(map[string]any)
	walk = func(m map[string]any) {
		for k, v := range m {
			keys[k] = true
			if nested, ok := v.(map[string]any); ok {
				walk(nested)
			}
		}
	}
	walk(m)
	return slices.Sorted(maps.Keys(keys))
}

// Every key limes writes for caveats and principals is one the migration
// writes: a key it spells otherwise decodes to a zero value, silently.
func TestCapabilityJSONMigrationSpellsLimesKeys(t *testing.T) {
	raw, err := FS.ReadFile(capabilityJSONMigration)
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	caveats := limes.Caveats{
		Ops:                    []limes.Op{limes.OpGet},
		ResourcePrefixes:       []string{"p/"},
		ResourceURIs:           []string{"u"},
		MaxRequests:            1,
		MaxBudgetAmount:        limes.NanosPerUnit,
		UnitCode:               limes.DefaultUnitCode,
		AllowTaintedRead:       true,
		IdempotencyKeyRequired: true,
		SourceIPCIDR:           []string{"10.0.0.0/8"},
	}
	principal := limes.Principal{
		Type: limes.PrincipalType("agent"), TenantID: uuid.New(), Subject: "s",
		Agent: &limes.AgentPrincipal{
			AgentType: "a", AgentVersion: "v", RunID: uuid.New(), ParentAgentID: uuid.New(),
			Model: "m", MCPClient: "c",
		},
	}
	for name, v := range map[string]any{"caveats": caveats, "principal": principal} {
		for _, key := range jsonKeys(t, v) {
			// As jsonb_build_object takes it: the key, then its value.
			if !strings.Contains(sql, "'"+key+"',") {
				t.Errorf("%s key %q is not written by %s", name, key, capabilityJSONMigration)
			}
		}
	}
}

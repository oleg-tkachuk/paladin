package config

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"
)

// chartTablePath is the role table the chart renders each role's RBAC from.
const chartTablePath = "../../deploy/chart/files/role-secrets.json"

// chartTable is roleSecrets as the chart reads it: the roles it deploys, each
// with the binding paths it reads, and the roles that read runtime Secrets.
type chartTable struct {
	Roles          map[Role][]string `json:"roles"`
	RuntimeReaders []Role            `json:"runtimeReaders"`
}

func currentChartTable() chartTable {
	t := chartTable{Roles: map[Role][]string{}}
	for role, paths := range roleSecrets {
		// The chart runs every role but the embedded MCP mode.
		if role == RoleMCPEmbedded {
			continue
		}
		t.Roles[role] = slices.Sorted(slices.Values(paths))
	}
	for _, r := range runtimeSecretReaders {
		if r != RoleMCPEmbedded {
			t.RuntimeReaders = append(t.RuntimeReaders, r)
		}
	}
	slices.Sort(t.RuntimeReaders)
	return t
}

// The chart grants each role its own Secrets from the same table the loader
// reads them by: one decision, in Go, which the chart consumes. Regenerate it
// with `go test ./internal/config -run TestTheChartReadsTheRoleTable -update`.
func TestTheChartReadsTheRoleTable(t *testing.T) {
	want, err := json.MarshalIndent(currentChartTable(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if *updateGolden {
		if err := os.WriteFile(chartTablePath, want, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(chartTablePath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update)", chartTablePath, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale; regenerate with -update", chartTablePath)
	}
	if roles := slices.Sorted(maps.Keys(currentChartTable().Roles)); len(roles) == 0 {
		t.Fatal("the table names no role")
	}
}

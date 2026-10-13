package postgres

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// storedJSONKeysPath lists the keys a row's caveats, principal and agent JSON
// carry. The migrations package holds 058 and 066 to the same file, so the
// stored form and the migrations that wrote it cannot drift apart.
const storedJSONKeysPath = "testdata/stored-json-keys.json"

// storedJSONKeys is the shape of storedJSONKeysPath.
type storedJSONKeys struct {
	Caveats   []string `json:"caveats"`
	Principal []string `json:"principal"`
	Agent     []string `json:"agent"`
}

func readStoredJSONKeys(t *testing.T) storedJSONKeys {
	t.Helper()
	raw, err := os.ReadFile(storedJSONKeysPath)
	if err != nil {
		t.Fatal(err)
	}
	var k storedJSONKeys
	if err := json.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	return k
}

// topKeys is the sorted set of top-level keys v marshals to.
func topKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

// Values for every stored field, set so none is omitted.
const (
	testSubject      = "agent-7"
	testAgentType    = "planner"
	testAgentVersion = "1.2.0"
	testModel        = "model-x"
	testMCPClient    = "mcp-client"
	testPrefix       = "bucket/reports/"
	testURI          = "bucket/reports/q3.csv"
	testCIDR         = "10.0.0.0/8"
	testMaxRequests  = 42
	// testBudget is 1.5 units; 058 wrote an amount as a decimal string.
	testBudget = "1.5"
)

func agentPrincipal() limes.Principal {
	return limes.Principal{
		Type: limes.PrincipalAgent, TenantID: uuid.New(), Subject: testSubject,
		Agent: &limes.AgentPrincipal{
			AgentType: testAgentType, AgentVersion: testAgentVersion, RunID: uuid.New(),
			ParentAgentID: uuid.New(), Model: testModel, MCPClient: testMCPClient,
		},
	}
}

func boundedCaveats() limes.Caveats {
	return limes.Caveats{
		Ops:                    []limes.Op{limes.OpGet, limes.OpList},
		ResourcePrefixes:       []string{testPrefix},
		ResourceURIs:           []string{testURI},
		Requests:               limes.RequestCeiling{Max: testMaxRequests},
		Budget:                 limes.BudgetCeiling{Max: limes.MustParseAmount(testBudget)},
		UnitCode:               limes.DefaultUnitCode,
		AllowTaintedRead:       true,
		IdempotencyKeyRequired: true,
		SourceIPCIDR:           []string{testCIDR},
	}
}

// The stored structs marshal to exactly the keys the migrations write, every
// field set; a key renamed here would leave every existing row decoding to
// a zero value.
func TestStoredJSONKeys(t *testing.T) {
	want := readStoredJSONKeys(t)
	caveats := toStoredCaveats(boundedCaveats())
	// Both flags set too, so no ceiling member is omitted.
	caveats.UnlimitedRequests, caveats.UnlimitedBudget = true, true
	principal := toStoredPrincipal(agentPrincipal())
	for name, tc := range map[string]struct {
		v    any
		want []string
	}{
		"caveats":   {caveats, want.Caveats},
		"principal": {principal, want.Principal},
		"agent":     {principal.Agent, want.Agent},
	} {
		if got, want := topKeys(t, tc.v), slices.Sorted(slices.Values(tc.want)); !slices.Equal(got, want) {
			t.Errorf("%s keys = %v, want %v", name, got, want)
		}
	}
}

// fieldNames is the sorted exported field names of struct type typ; limes's
// blank fields, which only force keyed literals, carry no data.
func fieldNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())
	for f := range typ.Fields() {
		if f.IsExported() {
			names = append(names, f.Name)
		}
	}
	slices.Sort(names)
	return names
}

// The stored structs carry every limes field: a field limes adds and these
// lack would be dropped from every row without a word.
func TestStoredStructsCoverLimesFields(t *testing.T) {
	if got, want := fieldNames(reflect.TypeFor[storedAgent]()), fieldNames(reflect.TypeFor[limes.AgentPrincipal]()); !slices.Equal(got, want) {
		t.Errorf("storedAgent fields = %v, want limes.AgentPrincipal's %v", got, want)
	}
	if got, want := fieldNames(reflect.TypeFor[storedPrincipal]()), fieldNames(reflect.TypeFor[limes.Principal]()); !slices.Equal(got, want) {
		t.Errorf("storedPrincipal fields = %v, want limes.Principal's %v", got, want)
	}
	// Each ceiling is two members in the stored form.
	ceilings := map[string][]string{
		"Requests": {"MaxRequests", "UnlimitedRequests"},
		"Budget":   {"MaxBudget", "UnlimitedBudget"},
	}
	var want []string
	for _, name := range fieldNames(reflect.TypeFor[limes.Caveats]()) {
		if members, ok := ceilings[name]; ok {
			want = append(want, members...)
			continue
		}
		want = append(want, name)
	}
	slices.Sort(want)
	if got := fieldNames(reflect.TypeFor[storedCaveats]()); !slices.Equal(got, want) {
		t.Errorf("storedCaveats fields = %v, want %v", got, want)
	}
}

func TestCaveatsRoundTrip(t *testing.T) {
	unlimited := boundedCaveats()
	unlimited.Requests = limes.RequestCeiling{Unlimited: true}
	unlimited.Budget = limes.BudgetCeiling{Unlimited: true}
	// A zero ceiling allows nothing: it must come back as 0, not unlimited.
	frozen := limes.Caveats{Ops: []limes.Op{limes.OpGet}}
	for name, c := range map[string]limes.Caveats{
		"bounded":   boundedCaveats(),
		"unlimited": unlimited,
		"frozen":    frozen,
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := marshalCaveats(c)
			if err != nil {
				t.Fatal(err)
			}
			got, err := unmarshalCaveats(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c) {
				t.Errorf("round trip = %+v, want %+v (JSON %s)", got, c, raw)
			}
		})
	}
}

func TestPrincipalRoundTrip(t *testing.T) {
	user := limes.Principal{Type: limes.PrincipalUser, TenantID: uuid.New(), Subject: testSubject}
	for name, p := range map[string]limes.Principal{"agent": agentPrincipal(), "no agent": user} {
		t.Run(name, func(t *testing.T) {
			raw, err := marshalPrincipal(p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := unmarshalPrincipal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, p) {
				t.Errorf("round trip = %+v, want %+v (JSON %s)", got, p, raw)
			}
		})
	}
}

// Rows 058 wrote, and 066 rewrote where a ceiling read 0 as "no ceiling",
// decode to the ceilings they meant.
func TestCaveatsDecodeMigratedRows(t *testing.T) {
	budget := limes.MustParseAmount(testBudget)
	unit := strconv.Quote(limes.DefaultUnitCode)
	maxRequests := strconv.Itoa(testMaxRequests)
	get := strconv.Quote(string(limes.OpGet))
	cases := map[string]struct {
		row      string
		requests limes.RequestCeiling
		budget   limes.BudgetCeiling
	}{
		"058 bounded": {
			row:      `{"ops":[` + get + `],"max_requests":` + maxRequests + `,"max_budget":` + strconv.Quote(testBudget) + `,"unit_code":` + unit + `}`,
			requests: limes.RequestCeiling{Max: testMaxRequests},
			budget:   limes.BudgetCeiling{Max: budget},
		},
		"066 unlimited": {
			row:      `{"ops":[` + get + `],"unit_code":` + unit + `,"unlimited_budget":true,"unlimited_requests":true}`,
			requests: limes.RequestCeiling{Unlimited: true},
			budget:   limes.BudgetCeiling{Unlimited: true},
		},
		"066 one ceiling lifted": {
			row:      `{"ops":[` + get + `],"max_requests":` + maxRequests + `,"unit_code":` + unit + `,"unlimited_budget":true}`,
			requests: limes.RequestCeiling{Max: testMaxRequests},
			budget:   limes.BudgetCeiling{Unlimited: true},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := unmarshalCaveats([]byte(tc.row))
			if err != nil {
				t.Fatal(err)
			}
			if got.Requests != tc.requests || got.Budget != tc.budget {
				t.Errorf("ceilings = %+v / %+v, want %+v / %+v", got.Requests, got.Budget, tc.requests, tc.budget)
			}
			if !slices.Equal(got.Ops, []limes.Op{limes.OpGet}) || got.UnitCode != limes.DefaultUnitCode {
				t.Errorf("caveats = %+v, want ops [get] in %s", got, limes.DefaultUnitCode)
			}
		})
	}
}

// A 058-era principal, as limes_principal_v4 wrote it, decodes field for field.
func TestPrincipalDecodesMigratedRow(t *testing.T) {
	want := agentPrincipal()
	a := want.Agent
	row := `{"type":"` + string(want.Type) + `","tenant_id":"` + want.TenantID.String() +
		`","subject":"` + want.Subject + `","agent":{"agent_type":"` + a.AgentType +
		`","agent_version":"` + a.AgentVersion + `","run_id":"` + a.RunID.String() +
		`","parent_agent_id":"` + a.ParentAgentID.String() + `","model":"` + a.Model +
		`","mcp_client":"` + a.MCPClient + `"}}`
	got, err := unmarshalPrincipal([]byte(row))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded = %+v, want %+v", got, want)
	}
}

func TestUnmarshalRefusesMalformedJSON(t *testing.T) {
	const malformed = `{"ops":`
	if _, err := unmarshalCaveats([]byte(malformed)); err == nil {
		t.Error("malformed caveats decoded")
	}
	if _, err := unmarshalPrincipal([]byte(malformed)); err == nil {
		t.Error("malformed principal decoded")
	}
}

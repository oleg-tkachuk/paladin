package auth_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// resourceMatchTable is limes's prefix matching as a table, which the Python
// fake — with no limes to call — is held to
// (sdk/testdata/capability_resource_matches.json).
const resourceMatchTable = "../../../sdk/testdata/capability_resource_matches.json"

// The table is limes's: every row is what limes.MatchResource answers, so a
// limes that reads a prefix differently fails here before the Python fake
// drifts from the server.
func TestTheSharedResourceMatchesAreLimess(t *testing.T) {
	raw, err := os.ReadFile(resourceMatchTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Matches []struct {
			Prefix string `json:"prefix"`
			URI    string `json:"uri"`
			Match  bool   `json:"match"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Matches) == 0 {
		t.Fatal("the table has no rows")
	}
	for _, m := range table.Matches {
		if got := limes.MatchResource(m.Prefix, m.URI); got != m.Match {
			t.Errorf("limes.MatchResource(%q, %q) = %v, the table says %v", m.Prefix, m.URI, got, m.Match)
		}
	}
}

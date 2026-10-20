package auth

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// sharedOps is the table of capability operations both SDKs and the console
// are held to (sdk/testdata/capability_ops.json).
var sharedOps = filepath.Join("..", "..", "..", "sdk", "testdata", "capability_ops.json")

// The table names exactly limes's built-in operations and the data plane's
// own, and each of the latter is one limes accepts in a caveat: a bare name
// it no longer defines would be refused at Issue.
func TestCapabilityOpsAreTheSharedTables(t *testing.T) {
	raw, err := os.ReadFile(sharedOps)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Limes     []limes.Op          `json:"limes"`
		DataPlane map[string]limes.Op `json:"data_plane"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	for _, op := range table.Limes {
		if !op.Builtin() {
			t.Errorf("%q is listed as limes's, and limes does not define it", op)
		}
	}
	for _, op := range []limes.Op{limes.OpGet, limes.OpPut, limes.OpList, limes.OpDelete, limes.OpShare, limes.OpManage} {
		if !slices.Contains(table.Limes, op) {
			t.Errorf("limes's %q is missing from the table", op)
		}
	}
	own := []limes.Op{OpPresign, OpTag}
	if len(table.DataPlane) != len(own) {
		t.Errorf("the table names %d data-plane ops, the server checks %d", len(table.DataPlane), len(own))
	}
	for _, op := range own {
		if err := op.Validate(); err != nil || op.Builtin() {
			t.Errorf("%q: valid %v, built in %v; want a namespaced op of the data plane's own", op, err, op.Builtin())
		}
		if !slices.Contains(slices.Collect(maps.Values(table.DataPlane)), op) {
			t.Errorf("the server checks %q, which the table does not name", op)
		}
	}
}

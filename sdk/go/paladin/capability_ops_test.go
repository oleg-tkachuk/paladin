package paladin_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sharedOps is sdk/testdata/capability_ops.json, which the Python SDK, the
// server and the console are held to as well.
const sharedOps = "../../testdata/capability_ops.json"

func TestCapabilityOpsMatchTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(sharedOps)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		DataPlane map[string]string `json:"data_plane"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"object_presign": paladin.OpObjectPresign, "object_tag": paladin.OpObjectTag}
	if len(table.DataPlane) != len(want) {
		t.Fatalf("the table names %d data-plane ops, the SDK %d", len(table.DataPlane), len(want))
	}
	for key, op := range want {
		if table.DataPlane[key] != op {
			t.Errorf("%s = %q in the SDK, %q in the table", key, op, table.DataPlane[key])
		}
	}
}

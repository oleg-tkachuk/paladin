package paladin_test

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
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
		Limes     []paladin.Op          `json:"limes"`
		DataPlane map[string]paladin.Op `json:"data_plane"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	shared := []paladin.Op{paladin.OpGet, paladin.OpPut, paladin.OpList, paladin.OpDelete, paladin.OpShare, paladin.OpManage}
	if !slices.Equal(table.Limes, shared) {
		t.Errorf("the shared ops are %v in the SDK, %v in the table", shared, table.Limes)
	}
	want := map[string]paladin.Op{"object_presign": paladin.OpObjectPresign, "object_tag": paladin.OpObjectTag}
	if len(table.DataPlane) != len(want) {
		t.Fatalf("the table names %d data-plane ops, the SDK %d", len(table.DataPlane), len(want))
	}
	for key, op := range want {
		if table.DataPlane[key] != op {
			t.Errorf("%s = %q in the SDK, %q in the table", key, op, table.DataPlane[key])
		}
	}
}

// opNames is sdk/testdata/capability_op_names.json, which the Python SDK's
// fake is held to as well.
const opNames = "../../testdata/capability_op_names.json"

// Validate refuses what the server refuses in a caveat, in this package's
// terms: a bare name that is neither built in nor namespaced.
func TestOpValidate(t *testing.T) {
	raw, err := os.ReadFile(opNames)
	if err != nil {
		t.Fatal(err)
	}
	var names struct {
		Valid   []paladin.Op `json:"valid"`
		Invalid []paladin.Op `json:"invalid"`
	}
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatal(err)
	}
	for _, op := range names.Valid {
		if err := op.Validate(); err != nil {
			t.Errorf("%q: %v, want valid", op, err)
		}
	}
	for _, op := range names.Invalid {
		if err := op.Validate(); !errors.Is(err, paladin.ErrInvalidArgument) {
			t.Errorf("%q: %v, want ErrInvalidArgument", op, err)
		}
	}
}

func TestOpNames(t *testing.T) {
	if got := paladin.OpNames(paladin.OpGet, paladin.OpObjectPresign); !slices.Equal(got, []string{"get", "object:presign"}) {
		t.Errorf("OpNames = %q", got)
	}
	if got := paladin.OpNames(); got == nil || len(got) != 0 {
		t.Errorf("OpNames() = %#v, want an empty slice", got)
	}
}

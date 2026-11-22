package storagepath

import (
	"encoding/json"
	"os"
	"testing"
)

// sharedKeys is the table both SDKs' fakes hold their key check to.
const sharedKeys = "../../../../sdk/testdata/object_keys.json"

func TestValidateKeyMatchesTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(sharedKeys)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Canonical []string `json:"canonical"`
		Refused   []string `json:"refused"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Canonical) == 0 || len(table.Refused) == 0 {
		t.Fatalf("the table lists %d canonical and %d refused keys", len(table.Canonical), len(table.Refused))
	}
	for _, k := range table.Canonical {
		if err := ValidateKey(k); err != nil {
			t.Errorf("%q is canonical in the table, refused here: %v", k, err)
		}
	}
	for _, k := range table.Refused {
		if ValidateKey(k) == nil {
			t.Errorf("%q is refused in the table, accepted here", k)
		}
	}
}

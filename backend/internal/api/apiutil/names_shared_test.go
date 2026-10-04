package apiutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sharedNames is the table both SDKs' tests read (sdk/testdata/names.json).
var sharedNames = filepath.Join("..", "..", "..", "..", "sdk", "testdata", "names.json")

// The tenant names the SDKs accept and refuse, ParseTenantNameRef accepts and
// refuses too. It is wider in one way only, on purpose: it also takes a bare
// id with no "tenants/" prefix, which the SDKs never send.
func TestParseTenantNameRefAgreesWithTheSharedNames(t *testing.T) {
	raw, err := os.ReadFile(sharedNames)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Tenant []struct {
			In     string `json:"in"`
			OK     bool   `json:"ok"`
			Tenant string `json:"tenant"`
		} `json:"tenant"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Tenant) == 0 {
		t.Fatal("the table has no tenant names")
	}
	for _, c := range table.Tenant {
		t.Run(c.In, func(t *testing.T) {
			ref, err := ParseTenantNameRef(c.In)
			if !c.OK {
				if err == nil {
					t.Fatalf("accepted a name the SDKs refuse: %+v", ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a name the SDKs accept: %v", err)
			}
			got := ref.Slug
			if ref.HasID() {
				got = ref.ID.String()
			}
			if got != c.Tenant {
				t.Errorf("tenant = %q, want %q", got, c.Tenant)
			}
		})
	}
}

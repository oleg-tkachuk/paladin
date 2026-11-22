//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// keyContractReport is the read-only query docs/upgrading/object-key-contract.md
// gives an operator to find objects stored, before the key contract, under a
// key it refuses. Kept here verbatim so the documented query is the tested one.
const keyContractReport = `
  SELECT tenant_id, collection_id, id, path FROM objects
  WHERE path = '' OR path LIKE '/%' OR path LIKE '%/'
     OR path LIKE '%//%' OR path ~ '(^|/)\.\.?(/|$)'`

// The report finds exactly the keys storagepath.ValidateKey refuses: none it
// accepts, and every one it does not.
func TestKeyContractReportMatchesTheValidator(t *testing.T) {
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "key-contract")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "docs")

	keys := []string{
		"a.txt", "a/b/c.txt", ".hidden", "a/..b", "...", "a/.b/c",
		"", "/a", "a/", "a//b", ".", "..", "./a", "a/./b", "a/../b", "a/..",
	}
	for _, k := range keys {
		mustInsertObject(t, f.h.PoolMigrate, tenant, "docs", k)
	}

	rows, err := f.h.PoolMigrate.Query(context.Background(),
		`SELECT path FROM (`+keyContractReport+`) r WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	found := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[p] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for _, k := range keys {
		refused := storagepath.ValidateKey(k) != nil
		if found[k] != refused {
			t.Errorf("key %q: reported %v, refused by the validator %v", k, found[k], refused)
		}
	}
}

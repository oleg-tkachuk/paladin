package migrations

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// capabilityJSONMigration rewrote stored caveats and principals into the
// stored spelling; explicitCeilingsMigration added the unlimited flags to the
// caveats whose ceilings read 0 as "no ceiling".
const (
	capabilityJSONMigration   = "058_capability_json_v4.sql"
	explicitCeilingsMigration = "066_explicit_ceilings.sql"
)

// storedJSONKeysPath lists the keys the capability store writes for a row's
// caveats, principal and agent; its own tests hold the stored structs to it.
const storedJSONKeysPath = "../internal/capability/postgres/testdata/stored-json-keys.json"

// storedJSONKeys is the shape of storedJSONKeysPath.
type storedJSONKeys struct {
	Caveats   []string `json:"caveats"`
	Principal []string `json:"principal"`
	Agent     []string `json:"agent"`
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	raw, err := FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// builtKey reports whether sql writes key as jsonb_build_object takes it: the
// key, then its value.
func builtKey(sql, key string) bool { return strings.Contains(sql, "'"+key+"',") }

// literalKey reports whether sql writes key inside a JSON literal.
func literalKey(sql, key string) bool { return strings.Contains(sql, `"`+key+`"`) }

// Every key the store reads for caveats and principals is one a migration
// writes: a key spelled otherwise decodes to a zero value, silently — and
// for a ceiling, a zero value now allows nothing.
func TestCapabilityJSONMigrationsSpellStoredKeys(t *testing.T) {
	raw, err := os.ReadFile(storedJSONKeysPath)
	if err != nil {
		t.Fatal(err)
	}
	var keys storedJSONKeys
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys.Caveats) == 0 || len(keys.Principal) == 0 || len(keys.Agent) == 0 {
		t.Fatalf("%s lists no keys for a stored object: %+v", storedJSONKeysPath, keys)
	}
	v4, ceilings := readMigration(t, capabilityJSONMigration), readMigration(t, explicitCeilingsMigration)

	// 058 rewrote every principal and agent key.
	for _, key := range append(append([]string{}, keys.Principal...), keys.Agent...) {
		if !builtKey(v4, key) {
			t.Errorf("principal key %q is not written by %s", key, capabilityJSONMigration)
		}
	}
	// 058 rewrote the caveats; 066 added the unlimited flags it had no
	// spelling for.
	for _, key := range keys.Caveats {
		if !builtKey(v4, key) && !literalKey(ceilings, key) {
			t.Errorf("caveats key %q is written by neither %s nor %s",
				key, capabilityJSONMigration, explicitCeilingsMigration)
		}
	}
}

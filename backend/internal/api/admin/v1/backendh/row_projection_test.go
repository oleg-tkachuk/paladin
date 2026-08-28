package backendh

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
)

// A filter compiles against the SCHEMA and evaluates against the PROJECTION.
// Nothing tied the two together, and the gap between them fails in the one way
// a filter must not: a schema field with no key in the projection yields an
// expression that compiles, runs, and matches nothing — for every row, on
// every request, with no error anywhere. An empty page is a legitimate answer,
// so the caller cannot tell it from a working filter that found nothing.
//
// backendRow was at 0% coverage, which is how it stayed possible.
func TestBackendRowCoversEverySchemaField(t *testing.T) {
	row := backendRow(admindomain.StorageBackend{BackendID: "primary"})

	for _, field := range celpkg.StorageBackendSchema.Fields() {
		if _, ok := row[field]; !ok {
			t.Errorf("StorageBackendSchema declares %q and backendRow does not supply it — a filter on that field compiles and then matches nothing, for every backend, silently", field)
		}
	}
}

// The other direction is worth pinning too, though it is the milder failure:
// a projected key with no schema declaration is dead weight the filter can
// never reference, and usually means a field was added to one side only.
func TestBackendRowProjectsNothingTheSchemaCannotSee(t *testing.T) {
	row := backendRow(admindomain.StorageBackend{BackendID: "primary"})
	declared := map[string]bool{}
	for _, f := range celpkg.StorageBackendSchema.Fields() {
		declared[f] = true
	}

	for key := range row {
		if !declared[key] {
			t.Errorf("backendRow projects %q, which StorageBackendSchema does not declare — no filter can reference it", key)
		}
	}
}

// Credential references are deliberately absent from both sides. A filter that
// could match on them would turn the list endpoint into an oracle for secret
// locations, one CEL predicate at a time.
func TestBackendRowNeverProjectsCredentials(t *testing.T) {
	row := backendRow(admindomain.StorageBackend{
		BackendID:                    "primary",
		CredentialsSecretRef:         "vault://kv/paladin/primary",
		PreviousCredentialsSecretRef: "vault://kv/paladin/primary-old",
	})

	for key, val := range row {
		if s, ok := val.(string); ok && (s == "vault://kv/paladin/primary" || s == "vault://kv/paladin/primary-old") {
			t.Errorf("backendRow projects a credential reference under %q", key)
		}
	}
}

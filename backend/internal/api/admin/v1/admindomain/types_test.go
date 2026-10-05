package admindomain

import (
	"testing"

	"github.com/google/uuid"
)

// The tenant an entry is filed under besides its actor's: read from the
// resource name, which nests a collection under its bucket.
func TestAuditEntryResourceTenant(t *testing.T) {
	tenant := uuid.New()
	cases := map[string]uuid.UUID{
		"tenants/" + tenant.String() + "/quota":                                           tenant,
		"storageBackends/primary/buckets/b/tenants/" + tenant.String() + "/collections/c": tenant,
		"storageBackends/primary/buckets/b":                                               uuid.Nil,
		"":                                                                                uuid.Nil,
	}
	for name, want := range cases {
		if got := (AuditEntry{ResourceName: name}).ResourceTenant(); got != want {
			t.Errorf("ResourceTenant(%q) = %s, want %s", name, got, want)
		}
	}
}

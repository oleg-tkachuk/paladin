package admindomain

import (
	"testing"
	"time"

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

// The cursor is cut by the store and by the audit handler, and read back by
// both; the time is UTC so one instant is one token whatever zone it came in.
func TestAuditCursor(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 3, 4, 5, 6, 7, 890, time.FixedZone("UTC+3", 3*60*60))
	got := AuditCursor(AuditEntry{EntryID: id, At: at})
	if want := "2026-03-04T02:06:07.00000089Z/" + id.String(); got != want {
		t.Errorf("AuditCursor = %q, want %q", got, want)
	}
}

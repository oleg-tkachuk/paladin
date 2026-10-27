package admindomain

import (
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
)

// The tenant an entry is filed under besides its actor's: read from the
// resource name, which nests a collection under its bucket.
func TestAuditEntryResourceTenant(t *testing.T) {
	tenant := uuid.New()
	cases := map[string]uuid.UUID{
		"tenants/" + tenant.String() + "/quota":                                           tenant,
		"storageBackends/primary/buckets/b/tenants/" + tenant.String() + "/collections/c": tenant,
		"storageBackends/primary/buckets/b":                                               uuid.Nil(),
		"":                                                                                uuid.Nil(),
	}
	for name, want := range cases {
		if got := (AuditEntry{ResourceName: name}).ResourceTenant(); got != want {
			t.Errorf("ResourceTenant(%q) = %s, want %s", name, got, want)
		}
	}
}

// The cursor is cut by the store and by the audit handler, and read back by
// the store; the time is UTC so one instant is one token whatever zone it
// came in, and it resumes only the order it was cut for.
func TestAuditCursor(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 3, 4, 5, 6, 7, 890, time.FixedZone("UTC+3", 3*60*60))
	e := AuditEntry{EntryID: id, At: at, Action: "iam.Login"}
	for _, order := range []keyset.Order{DefaultAuditOrder, {Field: AuditOrderByAction}} {
		c, err := keyset.Decode(AuditCursor(order, e), order)
		if err != nil {
			t.Fatalf("%+v: %v", order, err)
		}
		if c.ID != id || c.Value != AuditSortValue(order.Field, e) {
			t.Errorf("%+v: decoded %+v", order, c)
		}
	}
	if got := AuditSortValue(AuditOrderByAt, e); got != "2026-03-04T02:06:07.00000089Z" {
		t.Errorf("at value = %q, want it in UTC", got)
	}
}

// The order list_audit_entry_ids sorts in: text bytewise, at by instant, ties
// by id, the whole reversed when descending.
func TestCompareAuditEntries(t *testing.T) {
	lo, hi := uuid.UUID{1}, uuid.UUID{2}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		order keyset.Order
		a, b  AuditEntry
		want  int
	}{
		{"capitals before lowercase", keyset.Order{Field: AuditOrderByAction}, AuditEntry{Action: "Z"}, AuditEntry{Action: "a"}, -1},
		{"earlier first", keyset.Order{Field: AuditOrderByAt}, AuditEntry{At: t0}, AuditEntry{At: t0.Add(time.Second)}, -1},
		{"newest first", DefaultAuditOrder, AuditEntry{At: t0}, AuditEntry{At: t0.Add(time.Second)}, 1},
		{"a tie goes by id", keyset.Order{Field: AuditOrderByActor}, AuditEntry{ActorSubject: "x", EntryID: lo}, AuditEntry{ActorSubject: "x", EntryID: hi}, -1},
		{"no client before any", keyset.Order{Field: AuditOrderByClient}, AuditEntry{}, AuditEntry{UserAgent: "curl/8"}, -1},
		{"and back when descending", keyset.Order{Field: AuditOrderByResource, Desc: true}, AuditEntry{EntryID: lo}, AuditEntry{EntryID: hi}, 1},
	}
	for _, tc := range cases {
		if got := CompareAuditEntries(tc.order, tc.a, tc.b); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

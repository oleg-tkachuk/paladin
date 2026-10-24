//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// The audit log is the record of what happened, and the write into it is
// thirteen positional arguments — of which actor_audience, action,
// resource_name and request_id are four consecutive strings, and source_ip
// and error_message are two consecutive *string. Transposing any of those
// pairs compiles, passes review, and writes the wrong thing into the log
// permanently. Nothing here can be reconciled against another source later:
// the audit row IS the source.
//
// The existing audit integration tests assert that a row arrives, never what
// is in it, so all of that was unheld. The read mapper is pinned separately
// by TestAuditEntryFromModel; this pins the write, which makes the round trip
// meaningful — a swap on one side alone now shows up here.

func auditFixture(tenantID uuid.UUID) admindomain.AuditEntry {
	return admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            time.Now().UTC().Truncate(time.Microsecond),
		ActorSubject:  "actor-subject",
		ActorTenantID: tenantID,
		ActorAudience: "actor-audience",
		Action:        "action-name",
		ResourceName:  "resource-name",
		RequestID:     "request-id",
		SourceIP:      "203.0.113.7",
		ErrorMessage:  "error-message",
		RequestJSON:   []byte(`{"name":"tenants/t"}`),
		CapabilityID:  uuid.Must(uuid.NewV7()),
		RelayedBy:     "relayed-by",
		UserAgent:     "paladin-sdk-go/0.97.0 gateway/1.4",
	}
}

func assertAuditEntry(t *testing.T, got, want admindomain.AuditEntry, via string) {
	t.Helper()
	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"EntryID", got.EntryID, want.EntryID},
		{"ActorSubject", got.ActorSubject, want.ActorSubject},
		{"ActorTenantID", got.ActorTenantID, want.ActorTenantID},
		{"ActorAudience", got.ActorAudience, want.ActorAudience},
		{"Action", got.Action, want.Action},
		{"ResourceName", got.ResourceName, want.ResourceName},
		{"RequestID", got.RequestID, want.RequestID},
		{"SourceIP", got.SourceIP, want.SourceIP},
		{"ErrorMessage", got.ErrorMessage, want.ErrorMessage},
		{"CapabilityID", got.CapabilityID, want.CapabilityID},
		{"RelayedBy", got.RelayedBy, want.RelayedBy},
		{"UserAgent", got.UserAgent, want.UserAgent},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("via %s: %s = %v, want %v", via, c.field, c.got, c.want)
		}
	}
	if string(got.RequestJSON) != string(want.RequestJSON) {
		t.Errorf("via %s: RequestJSON = %s, want %s", via, got.RequestJSON, want.RequestJSON)
	}
	if !got.At.Equal(want.At) {
		t.Errorf("via %s: At = %v, want %v", via, got.At, want.At)
	}
}

func TestAuditWriteRoundTrip(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "audit-roundtrip")
	want := auditFixture(tenantID)

	if err := repo.Insert(ctx, want); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := repo.Get(ctx, want.EntryID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertAuditEntry(t, got, want, "Get")

	// List is a different query reaching the same columns. A transposition
	// in the SELECT list would show here and not in Get.
	page, _, err := repo.List(ctx, admindomain.ListAuditArgs{ActorTenantID: tenantID, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("list returned %d entries, want 1", len(page))
	}
	assertAuditEntry(t, page[0], want, "List")
}

// insertWith fills two fields the caller may leave unset. Both guards are
// `if x == zero`, and relaxed the other way they would overwrite what the
// caller passed — an audit entry filed under an id nobody can look up, or
// stamped with the time it was written rather than the time it happened.
func TestAuditInsertDefaults(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "audit-defaults")

	t.Run("caller values survive", func(t *testing.T) {
		want := auditFixture(tenantID)
		want.At = time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Microsecond)
		if err := repo.Insert(ctx, want); err != nil {
			t.Fatalf("insert: %v", err)
		}
		got, err := repo.Get(ctx, want.EntryID)
		if err != nil {
			t.Fatalf("get by the caller's id: %v — the id was not kept", err)
		}
		if !got.At.Equal(want.At) {
			t.Errorf("At = %v, want the caller's %v", got.At, want.At)
		}
	})

	t.Run("unset values are filled", func(t *testing.T) {
		before := time.Now().UTC().Add(-time.Minute)
		entry := auditFixture(tenantID)
		entry.EntryID = uuid.Nil
		entry.At = time.Time{}
		entry.RequestID = "unset-defaults-probe"

		if err := repo.Insert(ctx, entry); err != nil {
			t.Fatalf("insert: %v", err)
		}

		page, _, err := repo.List(ctx, admindomain.ListAuditArgs{ActorTenantID: tenantID, PageSize: 50})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var found *admindomain.AuditEntry
		for i := range page {
			if page[i].RequestID == "unset-defaults-probe" {
				found = &page[i]
			}
		}
		if found == nil {
			t.Fatal("the entry inserted without an id is not in the listing")
		}
		if found.EntryID == uuid.Nil {
			t.Error("EntryID = Nil, want a generated id")
		}
		if found.At.Before(before) {
			t.Errorf("At = %v, want a stamp from this test run (after %v)", found.At, before)
		}
	})
}

// The List filters are the operator's way into the log. likePrefixOrNil
// escapes the caller's prefix before it becomes a LIKE pattern; the unit test
// pins the pattern string, and only Postgres can say whether it means what it
// should. An unescaped underscore matches any character, so the operator
// filtering on one action prefix would be shown entries from another.
func TestAuditListFiltersNarrow(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "audit-filters")

	// "a_b." is the prefix an operator types; "axb." is the action that an
	// unescaped underscore would wrongly match.
	for _, action := range []string{"a_b.Create", "axb.Create", "zzz.Create"} {
		e := auditFixture(tenantID)
		e.Action = action
		e.ActorSubject = "subject-" + action
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert %s: %v", action, err)
		}
	}

	cases := map[string]struct {
		args admindomain.ListAuditArgs
		want []string
	}{
		"no filter": {
			args: admindomain.ListAuditArgs{ActorTenantID: tenantID},
			want: []string{"a_b.Create", "axb.Create", "zzz.Create"},
		},
		"exact action": {
			args: admindomain.ListAuditArgs{ActorTenantID: tenantID, ActionEq: "axb.Create"},
			want: []string{"axb.Create"},
		},
		"prefix escapes the underscore": {
			args: admindomain.ListAuditArgs{ActorTenantID: tenantID, ActionPrefix: "a_b."},
			want: []string{"a_b.Create"},
		},
		"actor subject": {
			args: admindomain.ListAuditArgs{ActorTenantID: tenantID, ActorSubject: "subject-zzz.Create"},
			want: []string{"zzz.Create"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.args.PageSize = 50
			page, _, err := repo.List(ctx, tc.args)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			got := make(map[string]bool, len(page))
			for _, e := range page {
				got[e.Action] = true
			}
			if len(got) != len(tc.want) {
				t.Fatalf("matched %d distinct actions %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("%q missing from the page %v", w, got)
				}
			}
		})
	}
}

// auditWrittenColumns is every column InsertAuditEntry writes
// (queries/audit_log.sql).
var auditWrittenColumns = []string{
	"id", "at", "actor_subject", "actor_tenant_id", "actor_audience",
	"action", "resource_name", "request_id", "source_ip",
	"request_json", "error_message", "capability_id",
	"resource_tenant_id", "relayed_by", "user_agent",
}

// A column nothing writes holds its default in every row and reads as data
// the log never had: detail and outcome sat in audit_log from the initial
// schema until 098, and before_json until 094. Every column of the table is
// one the write sets.
func TestAuditLogHoldsOnlyColumnsTheWriteSets(t *testing.T) {
	h := pgharness.Setup(t)
	rows, err := h.PoolMigrate.Query(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = 'audit_log'
		 ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	written := map[string]bool{}
	for _, c := range auditWrittenColumns {
		written[c] = true
	}
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
		if !written[name] {
			t.Errorf("audit_log.%s is a column the write never sets", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(auditWrittenColumns) {
		t.Errorf("audit_log has columns %v, the write sets %v", got, auditWrittenColumns)
	}
}

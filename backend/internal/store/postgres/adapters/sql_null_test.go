package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A family of helpers exists in this package for one reason: an unset domain
// value has to reach Postgres as NULL, not as the zero value. The two are not
// interchangeable. A NULL parameter lets the sqlc `$n IS NULL OR col = $n`
// branch drop out of the plan; the empty string turns the same predicate into
// `col = ''`, which narrows the query to rows nobody asked about. On a write
// the direction reverses: an empty string stored where NULL was meant is a
// value a later reader will find and believe.

func TestStrPtrNonEmpty(t *testing.T) {
	if got := strPtrNonEmpty(""); got != nil {
		t.Errorf("strPtrNonEmpty(\"\") = %q, want nil — an empty filter must not narrow the query", *got)
	}
	if got := strPtrNonEmpty("acme"); got == nil || *got != "acme" {
		t.Errorf("strPtrNonEmpty(\"acme\") = %v, want pointer to \"acme\"", got)
	}
}

// The audit filter is a prefix match, so the prefix has to be escaped before
// it becomes a LIKE pattern. An unescaped `_` matches any character and an
// unescaped `%` matches anything at all, which would hand the operator rows
// outside the actor they filtered on.
func TestLikePrefixOrNil(t *testing.T) {
	if got := likePrefixOrNil(""); got != nil {
		t.Errorf("likePrefixOrNil(\"\") = %q, want nil", *got)
	}

	cases := map[string]string{
		"user":    `user%`,
		"a_b":     `a\_b%`,
		"50%":     `50\%%`,
		`c:\path`: `c:\\path%`,
		`_%\`:     `\_\%\\%`,
	}
	for in, want := range cases {
		got := likePrefixOrNil(in)
		if got == nil {
			t.Errorf("likePrefixOrNil(%q) = nil, want %q", in, want)
			continue
		}
		if *got != want {
			t.Errorf("likePrefixOrNil(%q) = %q, want %q", in, *got, want)
		}
	}
}

func TestTsOptional(t *testing.T) {
	if got := tsOptional(time.Time{}); got.Valid {
		t.Error("tsOptional(zero): Valid = true, want false — an unset bound must not narrow the query")
	}
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	got := tsOptional(at)
	if !got.Valid || !got.Time.Equal(at) {
		t.Errorf("tsOptional(%v) = %+v, want a valid timestamp carrying it", at, got)
	}
}

// sinkKindToSQL guards two different absences with one condition: the field
// was not in the update mask (nil), or it was but carried nothing (""). Both
// mean "leave the column alone"; writing "" instead would fail the enum.
func TestSinkKindToSQL(t *testing.T) {
	empty := ""
	webhook := "webhook"

	for name, in := range map[string]*string{"nil": nil, "empty": &empty} {
		if got := sinkKindToSQL(in); got != nil {
			t.Errorf("sinkKindToSQL(%s) = %+v, want NULL", name, got)
		}
	}
	got := sinkKindToSQL(&webhook)
	if got == nil || *got != sqlc.EventSinkKind("webhook") {
		t.Errorf("sinkKindToSQL(\"webhook\") = %+v, want a valid webhook", got)
	}
}

// "no default retention mode" is NULL in the enum column and "" in the domain,
// so the pair has to round-trip through both representations.
func TestLockModeSQLRoundTrip(t *testing.T) {
	if got := lockModeToSQL(""); got != nil {
		t.Errorf("lockModeToSQL(\"\") = %+v, want NULL", got)
	}
	if got := lockModeFromSQL(nil); got != "" {
		t.Errorf("lockModeFromSQL(NULL) = %q, want \"\"", got)
	}
	// There used to be a third case here: a mode carried in a wrapper whose
	// Valid was false, which had to read back as "" so a bucket with no
	// retention mode could not be given one. sqlc v1.31 replaced the
	// NullObjectLockMode wrapper with *ObjectLockMode, and a pointer cannot
	// hold a value and be absent at the same time — the state the case
	// guarded is now unrepresentable rather than merely handled.
	for _, mode := range []string{"GOVERNANCE", "COMPLIANCE"} {
		sql := lockModeToSQL(mode)
		if sql == nil {
			t.Errorf("lockModeToSQL(%q) = nil, want a value", mode)
			continue
		}
		if got := lockModeFromSQL(sql); got != mode {
			t.Errorf("round trip %q -> %q", mode, got)
		}
	}
}

func TestPgUUIDOptional(t *testing.T) {
	if got := pgUUIDOptional(uuid.Nil); got.Valid {
		t.Error("pgUUIDOptional(Nil): Valid = true, want false")
	}
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	got := pgUUIDOptional(id)
	if !got.Valid || uuid.UUID(got.Bytes) != id {
		t.Errorf("pgUUIDOptional(%v) = %+v", id, got)
	}
}

func TestStrPtrOrNil(t *testing.T) {
	if got := strPtrOrNil(""); got != nil {
		t.Errorf("strPtrOrNil(\"\") = %q, want nil — an unset external_ref must stay absent", *got)
	}
	if got := strPtrOrNil("ref-1"); got == nil || *got != "ref-1" {
		t.Errorf("strPtrOrNil(\"ref-1\") = %v", got)
	}
}

// An unparseable upload id becomes the zero UUID on purpose: it matches no
// row, which is the same answer as an unknown id. The test pins that it is
// the zero *and NULL* value rather than something a row could equal.
func TestPgUUIDFromString(t *testing.T) {
	for _, in := range []string{"", "not-a-uuid", "6ba7b810-9dad-11d1-80b4"} {
		if got := pgUUIDFromString(in); got.Valid {
			t.Errorf("pgUUIDFromString(%q) = %+v, want NULL", in, got)
		}
	}
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	got := pgUUIDFromString(id.String())
	if !got.Valid || uuid.UUID(got.Bytes) != id {
		t.Errorf("pgUUIDFromString(%v) = %+v", id, got)
	}
}

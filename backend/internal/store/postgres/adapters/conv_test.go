package adapters

import (
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The conversions in conv.go decide, for every repository in this package,
// whether a zero domain value reaches Postgres as NULL or as a real value.
// That distinction is not cosmetic: a uuid.Nil written as a valid UUID matches
// no row but satisfies a NOT NULL foreign key, and a zero time written as a
// real timestamp lands in 0001-01-01 rather than leaving the column unset.

func TestPgUUIDAndBack(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	if got := pgUUID(uuid.Nil); got.Valid {
		t.Errorf("pgUUID(Nil): Valid = true, want false — nil must reach SQL as NULL")
	}
	got := pgUUID(id)
	if !got.Valid {
		t.Fatalf("pgUUID(%v): Valid = false, want true", id)
	}
	if uuid.UUID(got.Bytes) != id {
		t.Errorf("pgUUID(%v): Bytes = %v", id, uuid.UUID(got.Bytes))
	}

	if got := uuidFrom(pgtype.UUID{}); got != uuid.Nil {
		t.Errorf("uuidFrom(NULL) = %v, want Nil", got)
	}
	if got := uuidFrom(pgUUID(id)); got != id {
		t.Errorf("round trip = %v, want %v", got, id)
	}
	// A UUID carried in an invalid pgtype value is still NULL: the bytes are
	// there but the column is not set, and reading them back would resurrect
	// an id the writer deliberately left absent.
	if got := uuidFrom(pgtype.UUID{Bytes: id, Valid: false}); got != uuid.Nil {
		t.Errorf("uuidFrom(invalid-with-bytes) = %v, want Nil", got)
	}
}

func TestPgTSAndBack(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)

	if got := pgTS(time.Time{}); got.Valid {
		t.Errorf("pgTS(zero): Valid = true, want false")
	}
	got := pgTS(at)
	if !got.Valid {
		t.Fatalf("pgTS(%v): Valid = false, want true", at)
	}
	if !got.Time.Equal(at) {
		t.Errorf("pgTS(%v): Time = %v", at, got.Time)
	}

	if got := timeFrom(pgtype.Timestamptz{}); !got.IsZero() {
		t.Errorf("timeFrom(NULL) = %v, want zero", got)
	}
	if got := timeFrom(pgTS(at)); !got.Equal(at) {
		t.Errorf("timeFrom round trip = %v, want %v", got, at)
	}

	if got := timePtr(pgtype.Timestamptz{}); got != nil {
		t.Errorf("timePtr(NULL) = %v, want nil", got)
	}
	p := timePtr(pgTS(at))
	if p == nil {
		t.Fatal("timePtr(valid) = nil")
	}
	if !p.Equal(at) {
		t.Errorf("timePtr = %v, want %v", *p, at)
	}
}

func TestStrPtrAndDeref(t *testing.T) {
	if got := strPtr(""); got != nil {
		t.Errorf("strPtr(\"\") = %q, want nil — an unset string must reach SQL as NULL", *got)
	}
	p := strPtr("x")
	if p == nil || *p != "x" {
		t.Fatalf("strPtr(\"x\") = %v, want pointer to \"x\"", p)
	}

	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr(nil) = %q, want \"\"", got)
	}
	if got := derefStr(p); got != "x" {
		t.Errorf("derefStr = %q, want \"x\"", got)
	}
}

func TestEncodeDecodeMap(t *testing.T) {
	// The column is JSONB NOT NULL, so an absent map must still encode to a
	// document — `nil` would be a null literal, not an empty object.
	for name, m := range map[string]map[string]string{"nil": nil, "empty": {}} {
		if got := string(encodeMap(m)); got != `{}` {
			t.Errorf("encodeMap(%s) = %s, want {}", name, got)
		}
	}

	in := map[string]string{"a": "1", "b": "2"}
	round := decodeMap(encodeMap(in))
	if !maps.Equal(round, in) {
		t.Errorf("round trip = %v, want %v", round, in)
	}

	if got := decodeMap(nil); got != nil {
		t.Errorf("decodeMap(nil) = %v, want nil", got)
	}
	if got := decodeMap([]byte(`{not json`)); got != nil {
		t.Errorf("decodeMap(malformed) = %v, want nil", got)
	}
}

// The checksum algorithm is persisted as the int and served as the name, so
// the two halves have to agree; a swapped pair would report every stored
// CRC32C as a SHA256 without any read or write failing.
func TestChecksumAlgoRoundTrip(t *testing.T) {
	for _, name := range []string{"CRC32C", "SHA256", "MD5"} {
		v := checksumAlgoInt(name)
		if v == 0 {
			t.Errorf("checksumAlgoInt(%q) = 0, want a distinct code", name)
		}
		if got := checksumAlgoName(v); got != name {
			t.Errorf("round trip %q -> %d -> %q", name, v, got)
		}
	}
	if got := checksumAlgoInt("nonsense"); got != 0 {
		t.Errorf("checksumAlgoInt(unknown) = %d, want 0", got)
	}
	if got := checksumAlgoName(9); got != "" {
		t.Errorf("checksumAlgoName(unknown) = %q, want \"\"", got)
	}
}

func TestIsNoRows(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":     {nil, false},
		"bare":    {pgx.ErrNoRows, true},
		"wrapped": {fmt.Errorf("get bucket: %w", pgx.ErrNoRows), true},
		"other":   {errors.New("connection refused"), false},
	}
	for name, tc := range cases {
		if got := isNoRows(tc.err); got != tc.want {
			t.Errorf("%s: isNoRows = %v, want %v", name, got, tc.want)
		}
	}
}

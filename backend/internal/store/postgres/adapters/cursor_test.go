package adapters

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The version cursor is the whole of keyset paging: the next page is the rows
// after (created_at, version_id), so a cursor that loses precision or comes
// back as the zero value re-reads rows the caller already has, or steps over
// rows it never sees. Both are silent — the page still returns 200.
func TestVersionCursorRoundTrip(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	cases := map[string]time.Time{
		"nanosecond precision": time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC),
		"whole second":         time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
		// A row read through a connection in another zone must encode to the
		// same instant, or two pages of the same list disagree on ordering.
		"non-UTC": time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.FixedZone("x", 3*60*60)),
	}
	for name, at := range cases {
		t.Run(name, func(t *testing.T) {
			gotAt, gotID, err := decodeVersionCursor(encodeVersionCursor(at, id))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !gotAt.Equal(at) {
				t.Errorf("time round trip = %v, want %v", gotAt, at)
			}
			if gotID != id {
				t.Errorf("id round trip = %v, want %v", gotID, id)
			}
		})
	}
}

func TestDecodeVersionCursorRejects(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	stamp := at.UTC().Format(time.RFC3339Nano)

	cases := map[string]string{
		"empty":          "",
		"no separator":   stamp,
		"leading slash":  "/" + id.String(),
		"bad timestamp":  "not-a-time/" + id.String(),
		"bad uuid":       stamp + "/not-a-uuid",
		"trailing slash": stamp + "/",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			gotAt, gotID, err := decodeVersionCursor(tok)
			if err == nil {
				t.Fatalf("decode(%q) succeeded, want an error — List surfaces it as invalid page_token", tok)
			}
			// The caller aborts on the error, but a decoder that returned a
			// usable-looking bound alongside it would page from a position
			// nobody asked for if a later caller stopped checking.
			if !gotAt.IsZero() {
				t.Errorf("decode(%q) returned time %v alongside the error, want zero", tok, gotAt)
			}
			if gotID != uuid.Nil {
				t.Errorf("decode(%q) returned id %v alongside the error, want Nil", tok, gotID)
			}
		})
	}
}

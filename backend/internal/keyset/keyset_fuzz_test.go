package keyset

import (
	"encoding/base64"
	"errors"
	"testing"
	"unicode/utf8"
	"uuid"
)

// uuidBytes is the length of a UUID.
const uuidBytes = len(uuid.UUID{})

// Any page token a caller sends: refused with one of the two errors the list
// handlers map to InvalidArgument, or a cursor for the order asked for whose
// own token decodes back to it.
func FuzzDecode(f *testing.F) {
	for _, tok := range []string{
		Encode(Cursor{Order: Order{Field: "key", Desc: true}, Value: "a/b c", ID: uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6")}),
		Encode(Cursor{Order: Order{Field: "key"}, Value: "x"}),
		"%%%",
		"bm90LWpzb24",
		base64.RawURLEncoding.EncodeToString([]byte(`{"v": 7, "i": "x"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"f":"key","i":"7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6","x":1}`)),
		uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6").String(),
		"",
	} {
		f.Add(tok, "key", false)
	}
	f.Fuzz(func(t *testing.T, token, field string, desc bool) {
		want := Order{Field: field, Desc: desc}
		c, err := Decode(token, want)
		if err != nil {
			if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrOrderChanged) {
				t.Fatalf("Decode(%q) = %v, want ErrMalformed or ErrOrderChanged", token, err)
			}
			return
		}
		if c.Order != want || c.ID == uuid.Nil() {
			t.Fatalf("Decode(%q) = %+v under %+v", token, c, want)
		}
		if again, err := Decode(Encode(c), want); err != nil || again != c {
			t.Fatalf("Decode(%q) = %+v, re-encoded decodes to (%+v, %v)", token, c, again, err)
		}
	})
}

// Every cursor a list mints decodes back to itself under its own order. The
// field, value and id come from a row of Postgres, whose text is UTF-8, so a
// string that is not is skipped: JSON would carry it as U+FFFD.
func FuzzEncodeRoundTrip(f *testing.F) {
	id := uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6")
	f.Add("key", true, "a/b c", id[:])
	f.Add("size_bytes", false, "", id[:])
	f.Fuzz(func(t *testing.T, field string, desc bool, value string, raw []byte) {
		if len(raw) != uuidBytes || !utf8.ValidString(field) || !utf8.ValidString(value) {
			t.Skip("not a cursor a list mints")
		}
		c := Cursor{Order: Order{Field: field, Desc: desc}, Value: value, ID: uuid.UUID(raw)}
		if c.ID == uuid.Nil() {
			t.Skip("no row has the nil id")
		}
		got, err := Decode(Encode(c), c.Order)
		if err != nil || got != c {
			t.Fatalf("Decode(Encode(%+v)) = (%+v, %v)", c, got, err)
		}
	})
}

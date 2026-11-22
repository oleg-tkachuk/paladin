package adapters

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
)

// nonUTCOffset is the zone of a seed cursor minted from a row read in a zone
// other than UTC.
const nonUTCOffset = 3 * time.Hour

// Any version page token: refused with keyset.ErrMalformed and nothing usable
// beside it — ListVersions answers that as InvalidArgument, where any other
// error is the store failing and answers Internal — or a position whose own
// token decodes back to the same instant and id.
func FuzzDecodeVersionCursor(f *testing.F) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC)
	for _, tok := range []string{
		encodeVersionCursor(stamp, id),
		encodeVersionCursor(stamp.In(time.FixedZone("x", int(nonUTCOffset/time.Second))), id),
		"",
		stamp.Format(time.RFC3339Nano),
		"/" + id.String(),
		stamp.Format(time.RFC3339Nano) + "/",
	} {
		f.Add(tok)
	}
	f.Fuzz(func(t *testing.T, tok string) {
		at, got, err := decodeVersionCursor(tok)
		if err != nil {
			if !errors.Is(err, keyset.ErrMalformed) {
				t.Fatalf("decodeVersionCursor(%q) = %v, want keyset.ErrMalformed", tok, err)
			}
			if !at.IsZero() || got != uuid.Nil() {
				t.Fatalf("decodeVersionCursor(%q) refused but returned (%v, %s)", tok, at, got)
			}
			return
		}
		againAt, againID, err := decodeVersionCursor(encodeVersionCursor(at, got))
		if err != nil || !againAt.Equal(at) || againID != got {
			t.Fatalf("%q decodes to (%v, %s); re-encoded to (%v, %s, %v)", tok, at, got, againAt, againID, err)
		}
	})
}

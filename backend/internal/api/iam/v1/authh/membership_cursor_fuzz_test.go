package authh

import (
	"testing"
	"time"
	"uuid"
)

// Any membership page token: refused with nothing usable beside the error
// (ListMyMemberships answers InvalidArgument), or a position whose own token
// decodes back to it.
func FuzzDecodeMembershipCursor(f *testing.F) {
	id := uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6")
	for _, tok := range []string{
		encodeMembershipCursor(time.Date(2026, 11, 17, 12, 0, 0, 123, time.UTC), id),
		"",
		"no-separator",
		"x:" + id.String(),
		"123:not-a-uuid",
		"+5:" + id.String(),
		"-1:" + id.String(),
		"9223372036854775808:" + id.String(),
	} {
		f.Add(tok)
	}
	f.Fuzz(func(t *testing.T, tok string) {
		at, after, err := decodeMembershipCursor(tok)
		if err != nil {
			if !at.IsZero() || after != uuid.Nil() {
				t.Fatalf("%q: refused but returned (%v, %s)", tok, at, after)
			}
			return
		}
		if tok == "" {
			return
		}
		againAt, againID, err := decodeMembershipCursor(encodeMembershipCursor(at, after))
		if err != nil || !againAt.Equal(at) || againID != after {
			t.Fatalf("%q decodes to (%v, %s); re-encoded to (%v, %s, %v)", tok, at, after, againAt, againID, err)
		}
	})
}

package api_token

import (
	"errors"
	"testing"
)

// Any API token page cursor: the first page, the token id it names, or
// ErrInvalidCursor, which ListAPITokens answers as InvalidArgument.
func FuzzCursorID(f *testing.F) {
	for _, s := range []string{"", "7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6", "not-a-uuid", "{7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cursor string) {
		id, err := ListByTenantArgs{Cursor: cursor}.CursorID()
		switch {
		case err != nil:
			if !errors.Is(err, ErrInvalidCursor) || id != nil {
				t.Fatalf("CursorID(%q) = (%v, %v), want ErrInvalidCursor alone", cursor, id, err)
			}
		case cursor == "":
			if id != nil {
				t.Fatalf("CursorID(\"\") = %v, want the first page", id)
			}
		case id == nil:
			t.Fatalf("CursorID(%q) accepted a cursor and named no token", cursor)
		default:
			if again, err := (ListByTenantArgs{Cursor: id.String()}).CursorID(); err != nil || *again != *id {
				t.Fatalf("CursorID(%q) = %s; its own cursor reads (%v, %v)", cursor, id, again, err)
			}
		}
	})
}

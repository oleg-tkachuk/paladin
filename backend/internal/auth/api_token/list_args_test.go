package api_token

import (
	"errors"
	"testing"
	"uuid"
)

func TestClampListLimit(t *testing.T) {
	for _, tc := range []struct{ asked, want int32 }{
		{-1, DefaultListLimit},
		{0, DefaultListLimit},
		{1, 1},
		{MaxListLimit, MaxListLimit},
		{MaxListLimit + 1, MaxListLimit},
	} {
		if got := ClampListLimit(tc.asked); got != tc.want {
			t.Errorf("ClampListLimit(%d) = %d, want %d", tc.asked, got, tc.want)
		}
	}
}

// A page cursor is the id of the last token of the previous page. Anything
// else is the caller's mistake, refused before it reaches the database.
func TestListCursor(t *testing.T) {
	id := uuid.New()
	if got, err := (ListByTenantArgs{Cursor: id.String()}).CursorID(); err != nil || got == nil || *got != id {
		t.Fatalf("CursorID = %v, %v; want %s", got, err, id)
	}
	if got, err := (ListByTenantArgs{}).CursorID(); err != nil || got != nil {
		t.Fatalf("no cursor = %v, %v; want none", got, err)
	}
	if _, err := (ListByTenantArgs{Cursor: "garbage"}).CursorID(); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("garbage cursor: err = %v, want ErrInvalidCursor", err)
	}
}

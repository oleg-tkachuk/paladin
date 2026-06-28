package store

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

// Guards the ADR-0002 wiring for the IAM store sentinels. ErrNotFound→NotFound
// is the generic mapping; authh overrides it inline to Unauthenticated on the
// login/refresh paths (never via MapError), so both coexist.
func TestErrorRegistration(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"ErrNotFound", ErrNotFound, connect.CodeNotFound},
		{"ErrVersionMismatch", ErrVersionMismatch, connect.CodeAborted},
		{"ErrSubjectTaken", ErrSubjectTaken, connect.CodeAlreadyExists},
	}
	for _, tc := range cases {
		if got := connect.CodeOf(apiutil.MapError(tc.err)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

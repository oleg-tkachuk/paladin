package admindomain

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// Guards the ADR-0002 wiring: admin handlers route their error fallthroughs
// through apiutil.MapError, so these sentinels must resolve to a stable code.
func TestErrorRegistration(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"ErrNotFound", ErrNotFound, connect.CodeNotFound},
		{"ErrVersionMismatch", ErrVersionMismatch, connect.CodeAborted},
		{"ErrConflict", ErrConflict, connect.CodeFailedPrecondition},
	}
	for _, tc := range cases {
		if got := connect.CodeOf(apiutil.MapError(tc.err)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

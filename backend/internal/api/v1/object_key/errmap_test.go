package objectkey

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

// Guards the ADR-0002 wiring: the package init() must register its
// sentinels so apiutil.MapError (and mapVersionErr, which delegates to it)
// resolves them to the expected Connect code instead of CodeInternal.
func TestErrorRegistration(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"ErrVersionMismatch", ErrVersionMismatch, connect.CodeAborted},
		{"ErrObjectKeyHasObjects", ErrObjectKeyHasObjects, connect.CodeFailedPrecondition},
	}
	for _, tc := range cases {
		if got := connect.CodeOf(apiutil.MapError(tc.err)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

package bucket

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

// Guards the ADR-0002 wiring: the package init() must register its
// sentinels so apiutil.MapError resolves them to the expected Connect
// code instead of falling through to CodeInternal.
func TestErrorRegistration(t *testing.T) {
	if got := connect.CodeOf(apiutil.MapError(ErrVersionMismatch)); got != connect.CodeAborted {
		t.Fatalf("ErrVersionMismatch: got %v, want %v", got, connect.CodeAborted)
	}
}

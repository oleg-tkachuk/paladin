package capabilityh

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// A page token handed out for another tenant's listing — which pgstore
// refuses since limes v0.125 — is the caller's to fix, not the server's.
func TestCursorOfAnotherListingIsInvalidArgument(t *testing.T) {
	if got := connect.CodeOf(apiutil.MapError(limes.ErrCursorMismatch)); got != connect.CodeInvalidArgument {
		t.Errorf("ErrCursorMismatch maps to %v, want InvalidArgument", got)
	}
}

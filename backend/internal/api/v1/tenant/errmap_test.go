package tenant

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

// Guards the ADR-0002 wiring: the package init() must register every
// sentinel so apiutil.MapError resolves it to the expected Connect code.
// The handler ladders collapsed to apiutil.MapError(err), so this table is
// the contract that keeps their codes from drifting.
func TestErrorRegistration(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"ErrVersionMismatch", ErrVersionMismatch, connect.CodeAborted},
		{"ErrNotFound", ErrNotFound, connect.CodeNotFound},
		{"ErrSlugConflict", ErrSlugConflict, connect.CodeAlreadyExists},
		{"ErrTenantIDConflict", ErrTenantIDConflict, connect.CodeAlreadyExists},
		{"ErrDisplayNameConflict", ErrDisplayNameConflict, connect.CodeAlreadyExists},
		{"ErrAlreadyDeleted", ErrAlreadyDeleted, connect.CodeFailedPrecondition},
		{"ErrNotTrashed", ErrNotTrashed, connect.CodeFailedPrecondition},
		{"ErrDefaultBindingBucketMissing", ErrDefaultBindingBucketMissing, connect.CodeInvalidArgument},
		{"ErrTenantHasChildren", ErrTenantHasChildren, connect.CodeFailedPrecondition},
	}
	for _, tc := range cases {
		if got := connect.CodeOf(apiutil.MapError(tc.err)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

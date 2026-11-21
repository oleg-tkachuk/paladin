package admindomain

import (
	"regexp"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// packagePrefix is how a Go package names itself at the front of an error,
// "admin: ...": the implementation's name, not anything a caller can act on.
var packagePrefix = regexp.MustCompile(`^[a-z_/]+: `)

// The sentinels' texts are what a caller reads, so they speak of the
// resource and the reason, not of the package that defines them.
func TestSentinelsNameNoPackage(t *testing.T) {
	for _, err := range []error{
		ErrNotFound, ErrVersionMismatch, ErrConflict, ErrAlreadyExists,
		ErrBucketOwnerRequired, ErrBucketNotGranted, ErrBucketGrantInUse,
	} {
		if packagePrefix.MatchString(err.Error()) {
			t.Errorf("%q starts with a package name", err)
		}
	}
}

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

package statemachine

import "testing"

// A batch records a failed object's error as its reason, which the console
// shows: it names what went wrong with the object, not this package.
func TestErrorTextsNameNoPackage(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrConflict, "object version conflict"},
		{ErrNotFound, "object not found or not in the expected state"},
	} {
		if tc.err.Error() != tc.want {
			t.Errorf("%q, want %q", tc.err, tc.want)
		}
	}
}

package store

import "testing"

// These texts reach a caller as a refusal's message: they name the resource
// and the reason, not this package.
func TestErrorTextsNameNoPackage(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrNotFound, "resource not found"},
		{ErrVersionMismatch, "resource_version mismatch"},
		{ErrSubjectTaken, "subject already exists in tenant"},
		{ErrTokenRevoked, "refresh token revoked"},
		{ErrAlreadyRotated, "refresh token already rotated"},
		{ErrInvalidPageToken, "invalid page_token"},
	} {
		if tc.err.Error() != tc.want {
			t.Errorf("%q, want %q", tc.err, tc.want)
		}
	}
}

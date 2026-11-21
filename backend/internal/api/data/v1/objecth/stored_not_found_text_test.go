package objecth

import "testing"

// Storage.Head's answer for a missing object can reach a caller's message
// through a wrap; it names the object's bytes, not a package.
func TestStoredObjectNotFoundNamesNoPackage(t *testing.T) {
	const want = "object not found in storage"
	if got := ErrStoredObjectNotFound.Error(); got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

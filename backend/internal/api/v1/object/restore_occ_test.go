package object

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

// Restore used to accept an empty resource_version and skip the OCC check
// entirely — the handler argued a concurrent mutation of a DELETED row was
// vanishingly rare. "Rare" is not "impossible", and HardDelete is the other
// mutation path on that row: restoring over one is exactly the race worth
// refusing. RestoreObjectVersion was worse — the field was on the request,
// the console sent it, and the handler never read it.
//
// Both handlers validate the argument's shape before touching identity or
// storage, so a zero-value handler is enough to prove the refusal happens
// before any work: if the check moved after a lookup, this panics rather than
// passing.

func TestRestoreVersionRequiresOCC(t *testing.T) {
	t.Parallel()

	h := &VersionHandler{}

	t.Run("empty guard is refused", func(t *testing.T) {
		_, err := h.RestoreVersion(context.Background(),
			"tenants/t1/collections/c1/objects/o1/versions/v1", "")
		requireCode(t, err, connect.CodeInvalidArgument, "resource_version is required")
	})

	t.Run("unparseable guard is refused", func(t *testing.T) {
		_, err := h.RestoreVersion(context.Background(),
			"tenants/t1/collections/c1/objects/o1/versions/v1", "abc")
		requireCode(t, err, connect.CodeInvalidArgument, "invalid resource_version")
	})
}

func TestRestoreObjectRequiresOCC(t *testing.T) {
	t.Parallel()

	h := &Handler{}

	t.Run("empty guard is refused", func(t *testing.T) {
		_, err := h.RestoreObject(context.Background(), "collections/c1", "o1", "")
		requireCode(t, err, connect.CodeInvalidArgument, "resource_version is required")
	})

	t.Run("unparseable guard is refused", func(t *testing.T) {
		_, err := h.RestoreObject(context.Background(), "collections/c1", "o1", "abc")
		requireCode(t, err, connect.CodeInvalidArgument, "invalid resource_version")
	})
}

func requireCode(t *testing.T, err error, want connect.Code, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("call succeeded; want %v containing %q", want, contains)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("code = %v, want %v (err: %v)", got, want, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %q, want it to contain %q", err, contains)
	}
}

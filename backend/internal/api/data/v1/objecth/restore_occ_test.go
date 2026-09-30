package objecth

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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

// restoreParentStub serves one parent Object at a known resource_version, so
// the comparison itself can be exercised — not just the shape checks above.
type restoreParentStub struct {
	fakeObjectRepo
	obj Object
}

func (s *restoreParentStub) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return s.obj, nil
}

// TestRestoreVersionComparesAgainstParent is the assertion the shape checks
// cannot make: that the guard is actually compared, and compared against the
// PARENT object. Before this change the shim called RestoreVersion(ctx, name)
// and the field was never read at all — a test that only proved "empty is
// rejected" would have passed against that too, since protovalidate would
// have caught the empty one.
func TestRestoreVersionComparesAgainstParent(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	objectID := uuid.Must(uuid.NewV7())
	h := &VersionHandler{
		objects: &restoreParentStub{
			obj: Object{ObjectID: objectID, TenantID: tenantID, Collection: "docs", ResourceVersion: 7},
		},
	}
	ctx := auth.WithPrincipal(context.Background(),
		&auth.Principal{Subject: "u1", TenantID: tenantID})
	name := "tenants/" + tenantID.String() + "/collections/docs/objects/" +
		objectID.String() + "/versions/" + uuid.Must(uuid.NewV7()).String()

	t.Run("stale guard is aborted", func(t *testing.T) {
		_, err := h.RestoreVersion(ctx, name, "6")
		requireCode(t, err, connect.CodeAborted, "expected 6, current 7")
	})

	t.Run("matching guard gets past the check", func(t *testing.T) {
		// versions is nil, so a matching guard panics on the next line rather
		// than returning — which is exactly the proof wanted here: the guard
		// did not short-circuit, execution continued past it.
		defer func() {
			if recover() == nil {
				t.Error("a matching guard did not reach the version lookup")
			}
		}()
		_, _ = h.RestoreVersion(ctx, name, "7")
	})
}

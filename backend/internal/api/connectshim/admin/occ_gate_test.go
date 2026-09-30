package admin

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
)

// The servers below are built with a nil handler on purpose. Every case here
// must be rejected by the OCC gate *before* the handler is reached, so a nil
// H is the strongest possible assertion: if a gate ever stops firing, the test
// does not quietly pass with a mocked success — it panics.

// TestDeleteCollectionRequiresOCC covers the gap this gate closed.
// DeleteCollectionRequest already carried a `force` flag, but nothing checked
// the guard it was meant to force past: an omitted resource_version parsed to
// 0, and expected_version=0 disables the check in SQL. So the safe-looking
// call — no version, no force — was the blind one.
func TestDeleteCollectionRequiresOCC(t *testing.T) {
	t.Parallel()

	s := &CollectionServer{}

	t.Run("no version and no force is refused", func(t *testing.T) {
		_, err := s.DeleteCollection(context.Background(), connect.NewRequest(&pb.DeleteCollectionRequest{
			Name: "tenants/01a02416-f9f1-7c9c-90f1-b3d04aa1ea71/collections/c1",
		}))
		requireInvalidArgument(t, err, "resource_version is required")
	})

	t.Run("unparseable version is refused, not silently zeroed", func(t *testing.T) {
		// This is the case min_len=1 alone does not catch: "abc" satisfies the
		// length constraint, and the old `rv, _ := parseRV(...)` discarded the
		// error — landing on 0, which disables the guard.
		_, err := s.DeleteCollection(context.Background(), connect.NewRequest(&pb.DeleteCollectionRequest{
			Name:            "tenants/01a02416-f9f1-7c9c-90f1-b3d04aa1ea71/collections/c1",
			ResourceVersion: "abc",
		}))
		requireInvalidArgument(t, err, "invalid resource_version")
	})
}

// TestDeleteBucketRequiresOCC pins the flag the bypass now hangs off. It used
// to be delete_on_backend, which inverted the risk gradient: the one form of
// the call that also erases the physical bucket was the only one exempt from
// the concurrency check.
func TestDeleteBucketRequiresOCC(t *testing.T) {
	t.Parallel()

	s := &BucketServer{}

	t.Run("no version and no force is refused", func(t *testing.T) {
		_, err := s.DeleteBucket(context.Background(), connect.NewRequest(&pb.DeleteBucketRequest{
			Name: "storageBackends/b1/buckets/bk1",
		}))
		requireInvalidArgument(t, err, "resource_version is required")
	})

	t.Run("delete_on_backend no longer bypasses the guard", func(t *testing.T) {
		_, err := s.DeleteBucket(context.Background(), connect.NewRequest(&pb.DeleteBucketRequest{
			Name:            "storageBackends/b1/buckets/bk1",
			DeleteOnBackend: true,
		}))
		requireInvalidArgument(t, err, "resource_version is required")
	})

	t.Run("unparseable version is refused", func(t *testing.T) {
		_, err := s.DeleteBucket(context.Background(), connect.NewRequest(&pb.DeleteBucketRequest{
			Name:            "storageBackends/b1/buckets/bk1",
			ResourceVersion: "not-a-number",
		}))
		requireInvalidArgument(t, err, "invalid resource_version")
	})
}

// TestDeleteTenantRequiresOCC pins the gate that was already correct, so a
// refactor cannot quietly remove the one the others were modelled on.
func TestDeleteTenantRequiresOCC(t *testing.T) {
	t.Parallel()

	s := &TenantServer{}
	_, err := s.DeleteTenant(context.Background(), connect.NewRequest(&pb.DeleteTenantRequest{
		Name: "tenants/01a02416-f9f1-7c9c-90f1-b3d04aa1ea71",
	}))
	requireInvalidArgument(t, err, "resource_version is required")
}

func requireInvalidArgument(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("call succeeded; want InvalidArgument containing %q", want)
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want %v (err: %v)", got, connect.CodeInvalidArgument, err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}

package objecth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// storedIn is a bucket an object's bytes are in, open to every access.
func storedIn(backendID, bucket string) *BucketRef {
	return &BucketRef{BackendID: backendID, Bucket: bucket, State: openState}
}

// placedIn is obj as the store reads it when its bytes are in
// (backendID, bucket): every object but a PENDING one is placed somewhere
// (objects.placement_bucket_id). A fake that serves objects from one binding
// places them there unless the test placed them itself.
func placedIn(obj Object, backendID, bucket string) Object {
	if obj.Stored == nil && obj.State != statemachine.StatePending {
		obj.Stored = storedIn(backendID, bucket)
	}
	return obj
}

// The bytes of an object are where its row places them, whatever the
// collection is bound to; an object placed nowhere has none to resolve.
func TestStoredAtIsThePlacementNotTheBinding(t *testing.T) {
	tenantID := uuid.New()
	obj := Object{TenantID: tenantID, Collection: "docs", Key: "a.txt", Stored: storedIn("backend-old", "bucket-old")}
	at, err := StoredAt(obj, bucketgate.Read)
	if err != nil {
		t.Fatalf("StoredAt: %v", err)
	}
	want := Location{BackendID: "backend-old", TenantID: tenantID, Bucket: "bucket-old", Collection: "docs", Key: "a.txt"}
	if at != want {
		t.Fatalf("StoredAt = %+v, want %+v", at, want)
	}
	if _, err := StoredAt(Object{TenantID: tenantID, Collection: "docs", Key: "a.txt"}, bucketgate.Read); !errors.Is(err, ErrNotStored) {
		t.Fatalf("an object placed nowhere: err = %v, want ErrNotStored", err)
	}
}

// The gate decides on the state of the bucket the bytes are in.
func TestStoredAtAsksTheGateOfThePlacement(t *testing.T) {
	cases := map[string]struct {
		state  bucketgate.State
		access bucketgate.Access
		refuse bool
	}{
		"a read of a draining bucket":   {bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, bucketgate.Read, false},
		"a delete in a draining bucket": {bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, bucketgate.BytesWrite, true},
		"a read of a disabled backend":  {bucketgate.State{ProvisionState: bucketgate.ProvisionReady}, bucketgate.Read, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			obj := Object{Collection: "docs", Key: "a.txt", Stored: &BucketRef{BackendID: "b", Bucket: "k", State: tc.state}}
			_, err := StoredAt(obj, tc.access)
			if refused := bucketgate.Refused(err); refused != tc.refuse {
				t.Fatalf("err = %v, want refused %v", err, tc.refuse)
			}
		})
	}
}

// bindingRepo answers one binding and counts the lookups of it.
type bindingRepo struct {
	fakeObjectRepo
	lookups int
}

func (r *bindingRepo) LookupBucket(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
	r.lookups++
	return "backend-bound", "bucket-bound", nil
}

// A permanent delete owes the bucket the bytes are in; a PENDING object, placed
// nowhere, the one its URL was signed for; one with neither, its binding.
func TestDeletedBytesAt(t *testing.T) {
	cases := map[string]struct {
		obj        Object
		wantBucket string
		wantLookup int
	}{
		"placed":                {Object{Stored: storedIn("backend-old", "bucket-old"), Upload: storedIn("backend-up", "bucket-up")}, "bucket-old", 0},
		"pending, anchored":     {Object{Upload: storedIn("backend-up", "bucket-up")}, "bucket-up", 0},
		"pending, not anchored": {Object{}, "bucket-bound", 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := &bindingRepo{}
			h := &Handler{repo: repo}
			_, bucket, err := h.deletedBytesAt(context.Background(), uuid.New(), tc.obj)
			if err != nil {
				t.Fatalf("deletedBytesAt: %v", err)
			}
			if bucket != tc.wantBucket || repo.lookups != tc.wantLookup {
				t.Fatalf("bucket %q after %d lookups, want %q after %d", bucket, repo.lookups, tc.wantBucket, tc.wantLookup)
			}
		})
	}
}

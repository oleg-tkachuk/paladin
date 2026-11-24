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
// (objects.placement_bucket_id), and a PENDING one is anchored to the bucket
// its URL was signed for (objects.upload_bucket_id). A fake that serves
// objects from one binding places or anchors them there unless the test did
// itself.
func placedIn(obj Object, backendID, bucket string) Object {
	if obj.State == statemachine.StatePending {
		if obj.Upload == nil {
			obj.Upload = storedIn(backendID, bucket)
		}
		return obj
	}
	if obj.Stored == nil {
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

// A permanent delete owes the bucket the bytes are in; a PENDING object, placed
// nowhere, the one its URL was signed for. One with neither (the schema
// refuses such a row) owes nothing and is ErrNotStored, never its
// collection's binding.
func TestDeletedBytesAt(t *testing.T) {
	cases := map[string]struct {
		obj        Object
		wantBucket string
		wantErr    error
	}{
		"placed":            {Object{Stored: storedIn("backend-old", "bucket-old"), Upload: storedIn("backend-up", "bucket-up")}, "bucket-old", nil},
		"pending, anchored": {Object{Upload: storedIn("backend-up", "bucket-up")}, "bucket-up", nil},
		"neither":           {Object{}, "", ErrNotStored},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, bucket, err := deletedBytesAt(context.Background(), uuid.New(), tc.obj)
			if !errors.Is(err, tc.wantErr) || bucket != tc.wantBucket {
				t.Fatalf("deletedBytesAt = %q, %v; want %q, %v", bucket, err, tc.wantBucket, tc.wantErr)
			}
		})
	}
}

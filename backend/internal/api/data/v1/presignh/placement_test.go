package presignh

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// A GET URL is signed for the bucket the object's bytes are in (ADR-0051),
// whatever its collection is bound to now; the gate decides on that bucket.
func TestPresignGetSignsForTheObjectsPlacement(t *testing.T) {
	tid := uuid.New()
	placed := func(state bucketgate.State) *fakeRepo {
		return &fakeRepo{lookupObjectFn: func(_ context.Context, _ uuid.UUID, c string, _ uuid.UUID) (ObjectRef, error) {
			return ObjectRef{Collection: c, Key: "k", State: "AVAILABLE",
				Stored: &objecth.BucketRef{BackendID: "backend-old", Bucket: "bucket-old", State: state}}, nil
		}}
	}
	t.Run("placed outside the binding", func(t *testing.T) {
		st := &fakeStorage{}
		repo := placed(bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady})
		if _, _, _, err := NewHandler(repo, st, allowPolicy(), testConfig(t)).PresignGet(authedCtx(tid), "obj", validObjectID, 0, "", false); err != nil {
			t.Fatal(err)
		}
		if st.gotBackendID != "backend-old" || st.gotBucket != "bucket-old" {
			t.Fatalf("signed for %s/%s, want backend-old/bucket-old, where the bytes are", st.gotBackendID, st.gotBucket)
		}
	})
	t.Run("its backend disabled", func(t *testing.T) {
		st := &fakeStorage{}
		repo := placed(bucketgate.State{ProvisionState: bucketgate.ProvisionReady})
		_, _, _, err := NewHandler(repo, st, allowPolicy(), testConfig(t)).PresignGet(authedCtx(tid), "obj", validObjectID, 0, "", false)
		wantCode(t, err, connect.CodeFailedPrecondition)
		if st.getCalled {
			t.Fatal("signed a URL for a disabled backend")
		}
	})
}

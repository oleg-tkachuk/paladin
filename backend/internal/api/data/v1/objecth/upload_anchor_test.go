package objecth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// openState is a bucket gate state that admits everything.
var openState = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}

// The buckets an upload's tests anchor it to.
const (
	anchorBackend  = "be-1"
	anchoredBucket = "shared-source"
)

// headAtBucket answers a HEAD in each bucket with what it holds, and records
// where it looked.
type headAtBucket struct {
	noopStorage
	objects map[string]storedBytes
	looked  []string
}

func (s *headAtBucket) Head(_ context.Context, _, bucket string, _ uuid.UUID, _, _, _ string) (string, int64, string, string, error) {
	s.looked = append(s.looked, bucket)
	b, ok := s.objects[bucket]
	if !ok {
		return "", 0, "", "", ErrStoredObjectNotFound
	}
	return b.etag, b.size, b.checksum, b.sequencer, nil
}

func anchoredUpload(state bucketgate.State) Object {
	return Object{
		ObjectID: uuid.NewV7(), TenantID: uuid.New(), Collection: "docs", Key: "a.txt",
		Upload: &BucketRef{BackendID: anchorBackend, Bucket: anchoredBucket, State: state},
	}
}

// A completion reads the upload's bytes in the bucket its URL was signed for
// and nowhere else (ADR-0051), whatever the collection is bound to now.
func TestUploadedBytesReadsTheAnchorOnly(t *testing.T) {
	st := &headAtBucket{objects: map[string]storedBytes{anchoredBucket: {etag: "put-etag", size: 9}}}
	h := &Handler{storage: st}
	at, b, err := h.uploadedBytes(context.Background(), anchoredUpload(openState))
	if err != nil {
		t.Fatalf("uploadedBytes: %v", err)
	}
	if at.Bucket != anchoredBucket || at.BackendID != anchorBackend || b.etag != "put-etag" {
		t.Fatalf("read %+v at %+v, want the anchor's bytes", b, at)
	}
	if len(st.looked) != 1 {
		t.Errorf("looked in %v, want the anchor alone", st.looked)
	}
}

func TestUploadedBytesFailures(t *testing.T) {
	t.Run("no anchor", func(t *testing.T) {
		obj := anchoredUpload(openState)
		obj.Upload = nil
		if _, _, err := (&Handler{storage: &headAtBucket{}}).uploadedBytes(context.Background(), obj); !errors.Is(err, ErrNotAnchored) {
			t.Fatalf("err = %v, want ErrNotAnchored", err)
		}
	})
	t.Run("the anchor's backend disabled", func(t *testing.T) {
		st := &headAtBucket{}
		_, _, err := (&Handler{storage: st}).uploadedBytes(context.Background(), anchoredUpload(bucketgate.State{ProvisionState: bucketgate.ProvisionReady}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || len(st.looked) != 0 {
			t.Fatalf("err = %v after %d HEADs, want the gate's refusal and none", err, len(st.looked))
		}
	})
	t.Run("nothing uploaded", func(t *testing.T) {
		if _, _, err := (&Handler{storage: &headAtBucket{}}).uploadedBytes(context.Background(), anchoredUpload(openState)); !errors.Is(err, ErrStoredObjectNotFound) {
			t.Fatalf("err = %v, want ErrStoredObjectNotFound", err)
		}
	})
}

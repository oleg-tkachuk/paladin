package objecth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// The buckets an upload moves between in these tests: the one its URL was
// signed for before a storage migration's rebind, and the one the collection
// is bound to after it.
const (
	anchorBackend   = "be-1"
	anchoredBucket  = "shared-source"
	reboundBucket   = "dedicated-target"
	otherBackend    = "be-2"
	uploadSizeBytes = int64(9)
)

// openState is a bucket gate state that admits everything.
var openState = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}

func TestUploadOrigin(t *testing.T) {
	bound := Location{BackendID: anchorBackend, TenantID: uuid.New(), Bucket: reboundBucket, Collection: "docs", Key: "a.txt"}
	for name, tc := range map[string]struct {
		anchor       *UploadAnchor
		wantBucket   string
		wantBackend  string
		wantRelocate bool
	}{
		"no anchor: a row from before the anchor": {nil, reboundBucket, anchorBackend, false},
		"anchored to the bound bucket":            {&UploadAnchor{BackendID: anchorBackend, Bucket: reboundBucket}, reboundBucket, anchorBackend, false},
		"anchored to the bucket before a rebind":  {&UploadAnchor{BackendID: anchorBackend, Bucket: anchoredBucket}, anchoredBucket, anchorBackend, true},
		"same bucket name on another backend":     {&UploadAnchor{BackendID: otherBackend, Bucket: reboundBucket}, reboundBucket, otherBackend, true},
	} {
		t.Run(name, func(t *testing.T) {
			origin, relocate := UploadOrigin(tc.anchor, bound)
			if relocate != tc.wantRelocate || origin.Bucket != tc.wantBucket || origin.BackendID != tc.wantBackend {
				t.Fatalf("UploadOrigin = %s/%s relocate=%v, want %s/%s relocate=%v",
					origin.BackendID, origin.Bucket, relocate, tc.wantBackend, tc.wantBucket, tc.wantRelocate)
			}
			if origin.Collection != bound.Collection || origin.Key != bound.Key || origin.TenantID != bound.TenantID {
				t.Errorf("origin %+v moved the key, want the bound key %+v", origin, bound)
			}
		})
	}
}

// bucketStorage holds bytes per bucket, by size and ETag, and copies between
// buckets the way a server-side copy does: a new ETag, and no checksum.
type bucketStorage struct {
	noopStorage
	objects  map[string]storedBytes // bucket -> what it holds at the one key
	copies   []string               // "from->to" per copy
	copyErr  error
	copySize int64 // when set, what a copy lands as
}

func (s *bucketStorage) Head(_ context.Context, _, bucket string, _ uuid.UUID, _, _, _ string) (string, int64, string, string, error) {
	b, ok := s.objects[bucket]
	if !ok {
		return "", 0, "", "", fmt.Errorf("head %s: %w", bucket, ErrStoredObjectNotFound)
	}
	return b.etag, b.size, b.checksum, b.sequencer, nil
}

func (s *bucketStorage) CopyObject(_ context.Context, src, dst Location) error {
	s.copies = append(s.copies, src.Bucket+"->"+dst.Bucket)
	if s.copyErr != nil {
		return s.copyErr
	}
	b := s.objects[src.Bucket]
	b.etag, b.checksum = "copy-etag", ""
	if s.copySize != 0 {
		b.size = s.copySize
	}
	s.objects[dst.Bucket] = b
	return nil
}

func anchoredUpload(state bucketgate.State) Object {
	return Object{
		ObjectID: uuid.NewV7(), Collection: "docs", Key: "a.txt",
		Upload: &UploadAnchor{BackendID: anchorBackend, Bucket: anchoredBucket, State: state},
	}
}

func TestUploadedBytesMovesAnUploadFromTheBucketItsURLNamed(t *testing.T) {
	up := storedBytes{etag: "put-etag", size: uploadSizeBytes, checksum: "crc", sequencer: "seq-1"}
	st := &bucketStorage{objects: map[string]storedBytes{anchoredBucket: up}}
	h := &Handler{storage: st}
	obj := anchoredUpload(openState)
	bound := Location{BackendID: anchorBackend, Bucket: reboundBucket, Collection: obj.Collection, Key: obj.Key}

	b, uploadedETag, err := h.uploadedBytes(context.Background(), obj, bound)
	if err != nil {
		t.Fatalf("uploadedBytes: %v", err)
	}
	if len(st.copies) != 1 || st.copies[0] != anchoredBucket+"->"+reboundBucket {
		t.Fatalf("copies = %v, want one from the anchored bucket into the bound one", st.copies)
	}
	if uploadedETag != up.etag {
		t.Errorf("claims are checked against %q, want the ETag the client's PUT produced %q", uploadedETag, up.etag)
	}
	if b.etag != "copy-etag" || b.size != uploadSizeBytes {
		t.Errorf("promoted to %+v, want what the bound bucket holds", b)
	}
	if b.checksum != up.checksum {
		t.Errorf("checksum = %q, want the upload's %q when the copy carries none", b.checksum, up.checksum)
	}
	if _, ok := st.objects[anchoredBucket]; !ok {
		t.Error("the anchored copy was deleted; the migration's cleanup owns it")
	}
}

func TestUploadedBytesLooksInTheBoundBucketWhenTheAnchoredOneHasNone(t *testing.T) {
	// A URL regenerated after the rebind names the bound bucket.
	st := &bucketStorage{objects: map[string]storedBytes{reboundBucket: {etag: "e", size: uploadSizeBytes}}}
	h := &Handler{storage: st}
	obj := anchoredUpload(openState)
	bound := Location{BackendID: anchorBackend, Bucket: reboundBucket, Collection: obj.Collection, Key: obj.Key}

	b, etag, err := h.uploadedBytes(context.Background(), obj, bound)
	if err != nil || b.size != uploadSizeBytes || etag != "e" {
		t.Fatalf("uploadedBytes = %+v, %q, %v; want the bound bucket's bytes", b, etag, err)
	}
	if len(st.copies) != 0 {
		t.Errorf("copies = %v, want none", st.copies)
	}
}

func TestUploadedBytesFailures(t *testing.T) {
	copyErr := errors.New("copy: connection reset")
	for name, tc := range map[string]struct {
		state    bucketgate.State
		objects  map[string]storedBytes
		copyErr  error
		copySize int64
		want     connect.Code
		wantErr  error
	}{
		"the anchored backend is disabled": {
			state: bucketgate.State{ProvisionState: bucketgate.ProvisionReady}, want: connect.CodeFailedPrecondition,
		},
		"in neither bucket": {
			state: openState, objects: map[string]storedBytes{}, wantErr: ErrStoredObjectNotFound,
		},
		"the copy fails": {
			state: openState, objects: map[string]storedBytes{anchoredBucket: {size: uploadSizeBytes}},
			copyErr: copyErr, want: connect.CodeUnavailable, wantErr: copyErr,
		},
		"the copy lands short": {
			state: openState, objects: map[string]storedBytes{anchoredBucket: {size: uploadSizeBytes}},
			copySize: uploadSizeBytes - 1, want: connect.CodeUnavailable, wantErr: ErrRelocatedSize,
		},
	} {
		t.Run(name, func(t *testing.T) {
			objects := tc.objects
			if objects == nil {
				objects = map[string]storedBytes{anchoredBucket: {size: uploadSizeBytes}}
			}
			st := &bucketStorage{objects: objects, copyErr: tc.copyErr, copySize: tc.copySize}
			h := &Handler{storage: st}
			obj := anchoredUpload(tc.state)
			bound := Location{BackendID: anchorBackend, Bucket: reboundBucket, Collection: obj.Collection, Key: obj.Key}
			_, _, err := h.uploadedBytes(context.Background(), obj, bound)
			if err == nil {
				t.Fatal("uploadedBytes succeeded")
			}
			if tc.want != 0 {
				if got := connect.CodeOf(headFailure(err)); got != tc.want {
					t.Errorf("code = %v (%v), want %v", got, err, tc.want)
				}
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

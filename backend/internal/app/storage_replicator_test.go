package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

type fakeReplicaStore struct {
	err       error
	headErr   error
	deleteErr error
	gotSrc    objecth.Location
	gotDst    objecth.Location
	gotSize   int64
}

func (f *fakeReplicaStore) Replicate(_ context.Context, src, dst objecth.Location, size int64) error {
	f.gotSrc, f.gotDst, f.gotSize = src, dst, size
	return f.err
}

func (f *fakeReplicaStore) DeleteObject(_ context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error {
	f.gotDst = objecth.Location{BackendID: backendID, Bucket: bucket, TenantID: tenantID, Collection: collection, Key: key}
	return f.deleteErr
}

func (f *fakeReplicaStore) Head(context.Context, string, string, uuid.UUID, string, string, string) (string, int64, string, string, error) {
	return "", 0, "", "", f.headErr
}

func TestStorageReplicatorTranslatesErrors(t *testing.T) {
	cases := []struct {
		name                                   string
		err                                    error
		wantMissing, wantRetried, wantMismatch bool
	}{
		{"success", nil, false, false, false},
		{"source missing", fmt.Errorf("replica: %w", objecth.ErrStoredObjectNotFound), true, false, false},
		{"replica mismatch", fmt.Errorf("replica: %w", s3adapter.ErrReplicaMismatch), false, true, true},
		{"destination not found", fmt.Errorf("replica: %w", s3adapter.ErrReplicaDestinationNotFound), false, false, false},
		{"anything else", errors.New("access denied"), false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeReplicaStore{err: tc.err}
			src := worker.ReplicationTarget{BackendID: "a", BucketName: "sb", TenantID: uuid.New(), Collection: "c", Key: "k"}
			dst := worker.ReplicationTarget{BackendID: "b", BucketName: "db", TenantID: src.TenantID, Collection: "c", Key: "k"}
			err := newStorageReplicator(store).Replicate(context.Background(), src, dst, 7)
			if (err == nil) != (tc.err == nil) || !errors.Is(err, tc.err) && tc.err != nil {
				t.Fatalf("Replicate = %v, want %v kept in the chain", err, tc.err)
			}
			if got := errors.Is(err, worker.ErrObjectMissing); got != tc.wantMissing {
				t.Errorf("ErrObjectMissing = %v, want %v", got, tc.wantMissing)
			}
			if got := errors.Is(err, worker.ErrCopyTransient); got != tc.wantRetried {
				t.Errorf("ErrCopyTransient = %v, want %v", got, tc.wantRetried)
			}
			if got := errors.Is(err, worker.ErrCopyMismatch); got != tc.wantMismatch {
				t.Errorf("ErrCopyMismatch = %v, want %v", got, tc.wantMismatch)
			}
			if store.gotSrc.Bucket != "sb" || store.gotDst.BackendID != "b" || store.gotDst.Key != "k" || store.gotSize != 7 {
				t.Errorf("forwarded (%+v, %+v, %d)", store.gotSrc, store.gotDst, store.gotSize)
			}
		})
	}
}

func TestStorageReplicatorSourceMissing(t *testing.T) {
	unreachable := errors.New("bucket unreachable")
	cases := []struct {
		name        string
		headErr     error
		wantMissing bool
		wantErr     error
	}{
		{"present", nil, false, nil},
		{"confirmed missing", fmt.Errorf("head: %w", objecth.ErrStoredObjectNotFound), true, nil},
		{"unconfirmed", unreachable, false, unreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newStorageReplicator(&fakeReplicaStore{headErr: tc.headErr})
			missing, err := r.SourceMissing(context.Background(), worker.ReplicationTarget{})
			if missing != tc.wantMissing || !errors.Is(err, tc.wantErr) {
				t.Errorf("SourceMissing = (%v, %v), want (%v, %v)", missing, err, tc.wantMissing, tc.wantErr)
			}
		})
	}
}

// A replica delete reaches the destination's key; a failure the SDK would
// retry is transient, anything else is returned as it is.
func TestStorageReplicatorDeleteReplica(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantTransient bool
	}{
		{"deleted", nil, false},
		{"throttled", fmt.Errorf("delete: %w", statusErr(http.StatusServiceUnavailable)), true},
		{"refused", errors.New("access denied"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeReplicaStore{deleteErr: tc.err}
			dst := worker.ReplicationTarget{BackendID: "b", BucketName: "db", TenantID: uuid.New(), Collection: "c", Key: "k"}
			err := newStorageReplicator(store).DeleteReplica(context.Background(), dst)
			if !errors.Is(err, tc.err) {
				t.Fatalf("DeleteReplica = %v, want %v kept in the chain", err, tc.err)
			}
			if got := errors.Is(err, worker.ErrCopyTransient); got != tc.wantTransient {
				t.Errorf("ErrCopyTransient = %v, want %v", got, tc.wantTransient)
			}
			want := objecth.Location{BackendID: "b", Bucket: "db", TenantID: dst.TenantID, Collection: "c", Key: "k"}
			if store.gotDst != want {
				t.Errorf("deleted %+v, want %+v", store.gotDst, want)
			}
		})
	}
}

// statusErr is an HTTP response error with the given status, as the SDK
// reports one.
type statusErr int

func (e statusErr) Error() string       { return fmt.Sprintf("status %d", int(e)) }
func (e statusErr) HTTPStatusCode() int { return int(e) }

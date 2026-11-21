package presignh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// gatedLookup resolves through bucketgate.Check against state, as the store
// adapter does; lookupErr, when set, is the lookup itself failing.
func gatedLookup(state bucketgate.State, lookupErr error) func(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
	return func(_ context.Context, _ uuid.UUID, _ string, write bool) (objecth.BucketMeta, error) {
		if lookupErr != nil {
			return objecth.BucketMeta{}, lookupErr
		}
		if err := bucketgate.Check(bucketgate.AccessOf(write), state); err != nil {
			return objecth.BucketMeta{}, err
		}
		return objecth.BucketMeta{BackendID: "backend-1", BucketName: "bucket-1"}, nil
	}
}

// A fresh upload URL is a write: it answers the bucket's state as every
// other write does, before Cedar is asked and before anything is signed.
func TestRegenerateUploadURLAnswersTheBucketGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     bucketgate.State
		lookupErr error
		want      connect.Code
	}{
		{"draining", bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, nil, connect.CodeFailedPrecondition},
		{"provisioning", bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}, nil, connect.CodeUnavailable},
		{"provision failed", bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionFailed}, nil, connect.CodeFailedPrecondition},
		{"disabled", bucketgate.State{ProvisionState: bucketgate.ProvisionReady}, nil, connect.CodeFailedPrecondition},
		{"store unreachable", bucketgate.State{}, errors.New("connection reset"), connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := pendingRepo()
			repo.lookupMetaFn = gatedLookup(tc.state, tc.lookupErr)
			policy := allowPolicy()
			st := &fakeStorage{}
			_, err := NewHandler(repo, st, policy, testConfig(t)).RegenerateUploadURL(authedCtx(uuid.New()), "obj", validObjectID, 0)
			wantCode(t, err, tc.want)
			if policy.gotResource != nil {
				t.Fatalf("Cedar was asked with %+v; a refused write must not reach it", policy.gotResource)
			}
		})
	}
}

// A download URL is a read: a draining or provisioning bucket still serves it.
func TestPresignGetPassesAReadableBucket(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state bucketgate.State
	}{
		{"draining", bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}},
		{"provisioning", bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{lookupMetaFn: gatedLookup(tc.state, nil)}
			if _, _, _, err := NewHandler(repo, &fakeStorage{}, allowPolicy(), testConfig(t)).
				PresignGet(authedCtx(uuid.New()), "obj", validObjectID, 0, "", false); err != nil {
				t.Fatalf("PresignGet on a %s bucket: %v", tc.name, err)
			}
		})
	}
}

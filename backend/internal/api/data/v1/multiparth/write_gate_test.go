package multiparth

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// recordingAuthorizer records every Cedar check and denies it, so a write that
// passes the gate stops at its authorization with the binding recorded.
type recordingAuthorizer struct {
	checks []cedar.Resource
}

func (a *recordingAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.checks = append(a.checks, *r)
	return cedar.DecisionDeny, nil
}

// The (backend, bucket) the collection is bound to now, in gatedRepo; the
// session's anchored bucket is sessionForTenant's.
const (
	collectionBackend = "backend-x"
	collectionBucket  = "bucket-x"
)

// The bucket states the gate tables below run.
var (
	healthyState      = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}
	drainingState     = bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}
	provisioningState = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}
	failedState       = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionFailed}
	disabledState     = bucketgate.State{ProvisionState: bucketgate.ProvisionReady}
)

// gatedRepo is fakeRepo whose collection binding and whose session's anchored
// bucket are both in state. lookupErr fails both reads, as a store that
// cannot answer would.
func gatedRepo(session Session, state bucketgate.State, lookupErr error) *fakeRepo {
	return splitRepo(session, state, state, lookupErr)
}

// splitRepo is gatedRepo with the collection's current binding in one state
// and the session's anchored bucket in another, as after a storage migration
// rebound the collection mid-session.
func splitRepo(session Session, collection, anchored bucketgate.State, lookupErr error) *fakeRepo {
	session.BucketState = anchored
	return &fakeRepo{
		getSessionFn: func(context.Context, string) (Session, error) {
			if lookupErr != nil {
				return Session{}, lookupErr
			}
			return session, nil
		},
		lookupBucketFn: func(_ context.Context, _ uuid.UUID, _ string, access bucketgate.Access) (string, string, error) {
			if lookupErr != nil {
				return "", "", lookupErr
			}
			if err := bucketgate.Check(access, collection); err != nil {
				return "", "", err
			}
			return collectionBackend, collectionBucket, nil
		},
	}
}

// multipartRPC is a multipart RPC called the way a client would.
type multipartRPC struct {
	name string
	call func(h *Handler, ctx context.Context) error
}

var (
	initiateRPC = multipartRPC{"InitiateMultipartUpload", func(h *Handler, ctx context.Context) error {
		_, err := h.InitiateMultipartUpload(ctx, InitiateArgs{Collection: "photos", Key: "cat.jpg", ContentType: "image/jpeg", SizeHint: testObjectSize})
		return err
	}}
	completeRPC = multipartRPC{"CompleteMultipartUpload", func(h *Handler, ctx context.Context) error {
		_, err := h.CompleteMultipartUpload(ctx, CompleteArgs{UploadID: "up-1", Parts: completeParts()})
		return err
	}}
	presignPartRPC = multipartRPC{"PresignPart", func(h *Handler, ctx context.Context) error {
		_, _, _, err := h.PresignPart(ctx, "up-1", 1, time.Minute, testPartChecksum, SessionRef{})
		return err
	}}
	abortRPC = multipartRPC{"AbortMultipartUpload", func(h *Handler, ctx context.Context) error {
		return h.AbortMultipartUpload(ctx, "up-1", SessionRef{})
	}}
	listPartsRPC = multipartRPC{"ListParts", func(h *Handler, ctx context.Context) error {
		_, _, err := h.ListParts(ctx, "up-1", 0, "", SessionRef{})
		return err
	}}
)

// The multipart RPCs that write bytes.
var multipartWrites = []multipartRPC{initiateRPC, completeRPC, presignPartRPC}

// The RPCs on an open session that write bytes into it.
var sessionWrites = []multipartRPC{completeRPC, presignPartRPC}

// The RPCs on an open session that only free or read its parts.
var sessionReads = []multipartRPC{abortRPC, listPartsRPC}

// Every multipart write answers the bucket's state through the same gate as
// the single-object writes and is refused before Cedar is asked.
func TestMultipartWritesAnswerTheBucketGate(t *testing.T) {
	states := []struct {
		name      string
		state     bucketgate.State
		lookupErr error
		want      connect.Code
	}{
		{"draining", drainingState, nil, connect.CodeFailedPrecondition},
		{"provisioning", provisioningState, nil, connect.CodeUnavailable},
		{"failed", failedState, nil, connect.CodeFailedPrecondition},
		{"disabled", disabledState, nil, connect.CodeFailedPrecondition},
		{"store unreachable", bucketgate.State{}, errors.New("connection reset"), connect.CodeInternal},
	}
	for _, rpc := range multipartWrites {
		for _, st := range states {
			t.Run(rpc.name+"/"+st.name, func(t *testing.T) {
				tid := uuid.New()
				authz := &recordingAuthorizer{}
				err := rpc.call(newHandler(gatedRepo(sessionForTenant(tid), st.state, st.lookupErr), &fakeStorage{}, authz), authedCtx(tid))
				wantCode(t, err, st.want)
				if len(authz.checks) != 0 {
					t.Fatalf("Cedar was asked %d times (%+v); a refused write must not reach it", len(authz.checks), authz.checks)
				}
			})
		}
	}
}

// A write on an open session is decided on the bucket the session was
// anchored to, where its parts are, not on the collection's binding: after a
// storage migration rebinds the collection to a healthy backend, a session
// anchored to the old, draining one is refused; and one anchored to a healthy
// bucket goes on while the collection's new binding is provisioning.
func TestSessionWritesGateOnTheAnchoredBucket(t *testing.T) {
	for _, rpc := range sessionWrites {
		t.Run(rpc.name+"/anchored bucket draining, binding healthy", func(t *testing.T) {
			tid := uuid.New()
			authz := &recordingAuthorizer{}
			err := rpc.call(newHandler(splitRepo(sessionForTenant(tid), healthyState, drainingState, nil), &fakeStorage{}, authz), authedCtx(tid))
			wantCode(t, err, connect.CodeFailedPrecondition)
			if len(authz.checks) != 0 {
				t.Fatalf("Cedar was asked (%+v); a refused write must not reach it", authz.checks)
			}
		})
		t.Run(rpc.name+"/anchored bucket healthy, binding provisioning", func(t *testing.T) {
			tid := uuid.New()
			authz := &recordingAuthorizer{}
			err := rpc.call(newHandler(splitRepo(sessionForTenant(tid), provisioningState, healthyState, nil), &fakeStorage{}, authz), authedCtx(tid))
			wantCode(t, err, connect.CodePermissionDenied)
			requireAuthorizedOnAnchor(t, authz)
		})
	}
}

// Aborting a session frees storage and listing its parts reads it: both pass
// the gate as a read, refused only on a disabled backend. On a draining,
// provisioning or failed bucket they reach Cedar, which is asked about the
// anchored bucket.
func TestSessionReadsAnswerTheGateAsReads(t *testing.T) {
	for _, rpc := range sessionReads {
		for _, st := range []struct {
			name    string
			state   bucketgate.State
			refused bool
		}{
			{"healthy", healthyState, false},
			{"draining", drainingState, false},
			{"provisioning", provisioningState, false},
			{"failed", failedState, false},
			{"disabled", disabledState, true},
		} {
			t.Run(rpc.name+"/"+st.name, func(t *testing.T) {
				tid := uuid.New()
				authz := &recordingAuthorizer{}
				storage := &fakeStorage{}
				// The collection's binding is disabled throughout: only the
				// anchored bucket's state may decide.
				err := rpc.call(newHandler(splitRepo(sessionForTenant(tid), disabledState, st.state, nil), storage, authz), authedCtx(tid))
				if st.refused {
					wantCode(t, err, connect.CodeFailedPrecondition)
					if len(authz.checks) != 0 {
						t.Fatalf("Cedar was asked (%+v) on a disabled backend", authz.checks)
					}
					return
				}
				wantCode(t, err, connect.CodePermissionDenied)
				requireAuthorizedOnAnchor(t, authz)
			})
		}
	}
}

// A session naming no bucket is the repository breaking its contract
// (multipart_uploads.bucket_id is NOT NULL): every RPC on it fails as the
// server's fault, before Cedar is asked about an empty binding.
func TestRPCsOnAnUnanchoredSessionFail(t *testing.T) {
	for _, rpc := range append(append([]multipartRPC{}, sessionWrites...), sessionReads...) {
		t.Run(rpc.name, func(t *testing.T) {
			tid := uuid.New()
			sess := sessionForTenant(tid)
			sess.BackendID, sess.Bucket = "", ""
			authz := &recordingAuthorizer{}
			err := rpc.call(newHandler(gatedRepo(sess, healthyState, nil), &fakeStorage{}, authz), authedCtx(tid))
			wantCode(t, err, connect.CodeInternal)
			if !errors.Is(err, errUnanchored) {
				t.Errorf("err = %v, want %v", err, errUnanchored)
			}
			if len(authz.checks) != 0 {
				t.Fatalf("Cedar was asked (%+v) on an empty binding", authz.checks)
			}
		})
	}
}

// requireAuthorizedOnAnchor fails unless Cedar was asked about the session's
// anchored bucket, never the collection's binding.
func requireAuthorizedOnAnchor(t *testing.T, authz *recordingAuthorizer) {
	t.Helper()
	if len(authz.checks) == 0 {
		t.Fatal("Cedar was never asked")
	}
	if r := authz.checks[0]; r.BackendID != testSessionBackend || r.BucketName != testSessionBucket {
		t.Fatalf("authorized on (%q, %q), want the anchored (%q, %q)", r.BackendID, r.BucketName, testSessionBackend, testSessionBucket)
	}
}

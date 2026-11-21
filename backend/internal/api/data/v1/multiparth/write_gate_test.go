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

// gatedRepo is fakeRepo resolving its bucket through bucketgate.Check against
// state, serving session for the session reads.
func gatedRepo(session Session, state bucketgate.State, lookupErr error) *fakeRepo {
	return &fakeRepo{
		getSessionFn: func(context.Context, string) (Session, error) { return session, nil },
		lookupBucketFn: func(_ context.Context, _ uuid.UUID, _ string, write bool) (string, string, error) {
			if lookupErr != nil {
				return "", "", lookupErr
			}
			if err := bucketgate.Check(bucketgate.AccessOf(write), state); err != nil {
				return "", "", err
			}
			return "backend-x", "bucket-x", nil
		},
	}
}

// The multipart RPCs that write, each called the way a client would.
var multipartWrites = []struct {
	name string
	call func(h *Handler, ctx context.Context) error
}{
	{"InitiateMultipartUpload", func(h *Handler, ctx context.Context) error {
		_, err := h.InitiateMultipartUpload(ctx, InitiateArgs{Collection: "photos", Key: "cat.jpg", ContentType: "image/jpeg", SizeHint: testObjectSize})
		return err
	}},
	{"CompleteMultipartUpload", func(h *Handler, ctx context.Context) error {
		_, err := h.CompleteMultipartUpload(ctx, CompleteArgs{UploadID: "up-1", Parts: completeParts()})
		return err
	}},
	{"PresignPart", func(h *Handler, ctx context.Context) error {
		_, _, _, err := h.PresignPart(ctx, "up-1", 1, time.Minute, testPartChecksum, SessionRef{})
		return err
	}},
}

// Every multipart write answers the bucket's state through the same gate as
// the single-object writes — the session's own anchored bucket included,
// which used to let a completion through on a draining backend — and is
// refused before Cedar is asked.
func TestMultipartWritesAnswerTheBucketGate(t *testing.T) {
	states := []struct {
		name      string
		state     bucketgate.State
		lookupErr error
		want      connect.Code
	}{
		{"draining", bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, nil, connect.CodeFailedPrecondition},
		{"provisioning", bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}, nil, connect.CodeUnavailable},
		{"disabled", bucketgate.State{ProvisionState: bucketgate.ProvisionReady}, nil, connect.CodeFailedPrecondition},
		{"store unreachable", bucketgate.State{}, errors.New("connection reset"), connect.CodeInternal},
	}
	for _, rpc := range multipartWrites {
		for _, st := range states {
			for _, anchored := range []bool{true, false} {
				name := rpc.name + "/" + st.name
				if !anchored {
					name += "/unanchored session"
				}
				t.Run(name, func(t *testing.T) {
					tid := uuid.New()
					sess := sessionForTenant(tid)
					if !anchored {
						sess.BackendID, sess.Bucket = "", ""
					}
					authz := &recordingAuthorizer{}
					storage := &fakeStorage{}
					err := rpc.call(newHandler(gatedRepo(sess, st.state, st.lookupErr), storage, authz), authedCtx(tid))
					wantCode(t, err, st.want)
					if len(authz.checks) != 0 {
						t.Fatalf("Cedar was asked %d times (%+v); a refused write must not reach it", len(authz.checks), authz.checks)
					}
				})
			}
		}
	}
}

// An unanchored session — one from before sessions recorded their bucket — is
// authorized on the collection's current binding, never on an empty one.
func TestMultipartWritesAuthorizeAnUnanchoredSessionOnItsBinding(t *testing.T) {
	for _, rpc := range multipartWrites[1:] {
		t.Run(rpc.name, func(t *testing.T) {
			tid := uuid.New()
			sess := sessionForTenant(tid)
			sess.BackendID, sess.Bucket = "", ""
			authz := &recordingAuthorizer{}
			healthy := bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}
			err := rpc.call(newHandler(gatedRepo(sess, healthy, nil), &fakeStorage{}, authz), authedCtx(tid))
			wantCode(t, err, connect.CodePermissionDenied)
			if len(authz.checks) == 0 {
				t.Fatal("Cedar was never asked")
			}
			if r := authz.checks[0]; r.BackendID != "backend-x" || r.BucketName != "bucket-x" {
				t.Fatalf("authorized on (%q, %q), want the collection's binding", r.BackendID, r.BucketName)
			}
		})
	}
}

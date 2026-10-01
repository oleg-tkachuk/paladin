package objecth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// denyingRecorder records what it was asked and refuses it, so the handler
// stops right after authorization.
type denyingRecorder struct{ resource *cedar.Resource }

func (d *denyingRecorder) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	cp := *r
	d.resource = &cp
	return cedar.DecisionDeny, nil
}

type uploadSmallRepo struct{ fakeObjectRepo }

func (*uploadSmallRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	return "backend-1", "bucket-1", nil
}

type initOnlyStream struct{ init StreamInit }

func (s initOnlyStream) RecvInit() (StreamInit, error)   { return s.init, nil }
func (s initOnlyStream) RecvChunk() (StreamChunk, error) { return StreamChunk{}, nil }

// An upload without a key gets the object id as its key. Cedar must see that
// key: authorizing first evaluated the Collection, so a policy reading
// resource.key could not apply to these uploads.
func TestUploadSmallAuthorizesTheKeyItWillWrite(t *testing.T) {
	tenantID := uuid.New()
	authz := &denyingRecorder{}
	h := &Handler{repo: &uploadSmallRepo{}, policy: authz}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

	_, err := h.UploadSmall(ctx, initOnlyStream{StreamInit{Collection: "docs", ContentType: "text/plain"}}, UploadSmallDeps{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied from the recorder", connect.CodeOf(err))
	}
	if authz.resource == nil || authz.resource.Key == "" {
		t.Fatalf("authorized %+v, want a resource carrying the assigned key", authz.resource)
	}
	if _, err := uuid.Parse(authz.resource.Key); err != nil {
		t.Errorf("key = %q, want the object id assigned to a keyless upload", authz.resource.Key)
	}
}

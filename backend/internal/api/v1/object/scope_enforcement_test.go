package object

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// This test pins the scope-enforcement contract: the data-plane write paths
// MUST populate the physical (backend, bucket) on the cedar.Resource BEFORE
// IsAuthorized, so the scope-enforcement built-in (engine.go) can admit a
// bucket:/object_key:-scoped PAT. Without the binding a scoped principal is
// fail-closed (denied) on its own bucket — the bug this guards against.

// recordingAuthorizer captures the last Resource/action handed to the engine.
type recordingAuthorizer struct {
	lastResource *cedar.Resource
	lastAction   string
}

func (a *recordingAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.lastAction = action
	// Copy: the handler may reuse/mutate the pointer after the call returns.
	cp := *r
	a.lastResource = &cp
	return cedar.DecisionAllow, nil
}

// uploadRepo embeds fakeObjectRepo (LookupBucketMeta returns f.meta) and adds
// the CreateObject the UploadObject path needs.
type uploadRepo struct {
	fakeObjectRepo
}

func (r *uploadRepo) CreateObject(_ context.Context, args CreateObjectArgs) (Object, error) {
	return Object{
		ObjectID:  uuid.Must(uuid.NewV7()),
		TenantID:  args.TenantID,
		ObjectKey: args.ObjectKey,
		Key:       args.Key,
	}, nil
}

// noopStorage is an object.Storage that returns benign values; UploadObject
// only reaches PresignPut (TransportPOST=false).
type noopStorage struct{}

func (noopStorage) PresignPut(context.Context, PresignPutArgs) (string, map[string]string, time.Time, error) {
	return "https://s3/put", map[string]string{"h": "v"}, time.Unix(200, 0), nil
}
func (noopStorage) PresignPost(context.Context, PresignPostArgs) (string, map[string]string, time.Time, error) {
	return "", nil, time.Time{}, nil
}
func (noopStorage) PresignGet(context.Context, PresignGetArgs) (string, map[string]string, time.Time, error) {
	return "", nil, time.Time{}, nil
}
func (noopStorage) Head(context.Context, string, string, uuid.UUID, string, string) (string, int64, string, string, error) {
	return "", 0, "", "", nil
}
func (noopStorage) CopyObject(context.Context, Location, Location) error { return nil }
func (noopStorage) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	return nil
}

// UploadObject is the exemplar write path. Its cedar.Resource must carry the
// bucket binding resolved from the object-key so bucket:/object_key: scopes
// enforce here — resolved with ONE LookupBucketMeta, reused for completion
// mode + presign routing.
func TestUploadObjectAuthzResourceCarriesBucket(t *testing.T) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

	authz := &recordingAuthorizer{}
	h := &Handler{
		repo:    &uploadRepo{fakeObjectRepo: fakeObjectRepo{meta: BucketMeta{BackendID: "backend-7", BucketName: "bucket-7"}}},
		storage: noopStorage{},
		policy:  authz,
		presign: PresignConfig{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour},
	}

	if _, err := h.UploadObject(ctx, UploadObjectInput{ObjectKey: "docs", Key: "a.txt", ContentType: "text/plain"}); err != nil {
		t.Fatalf("UploadObject: %v", err)
	}
	if authz.lastResource == nil {
		t.Fatal("authorizer was never called")
	}
	if authz.lastResource.BackendID != "backend-7" || authz.lastResource.BucketName != "bucket-7" {
		t.Fatalf("authz Resource missing physical binding: backend=%q bucket=%q — a bucket:/object_key:-scoped PAT would be fail-closed on upload",
			authz.lastResource.BackendID, authz.lastResource.BucketName)
	}
	if authz.lastResource.ObjectKey != "docs" || authz.lastResource.Key != "a.txt" {
		t.Fatalf("authz Resource identity wrong: object_key=%q key=%q", authz.lastResource.ObjectKey, authz.lastResource.Key)
	}
	if authz.lastAction != cedar.ActionPresignPut {
		t.Fatalf("authz action = %q, want %q", authz.lastAction, cedar.ActionPresignPut)
	}
}

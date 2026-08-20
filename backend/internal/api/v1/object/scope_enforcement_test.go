package object

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// This test pins the scope-enforcement contract: the data-plane write paths
// MUST populate the physical (backend, bucket) on the cedar.Resource BEFORE
// IsAuthorized, so the scope-enforcement built-in (engine.go) can admit a
// bucket:/collection:-scoped PAT. Without the binding a scoped principal is
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
		ObjectID:   uuid.Must(uuid.NewV7()),
		TenantID:   args.TenantID,
		Collection: args.Collection,
		Key:        args.Key,
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
// bucket binding resolved from the object-key so bucket:/collection: scopes
// enforce here — resolved with ONE LookupBucketMeta, reused for completion
// mode + presign routing.
func TestUploadObjectAuthzResourceCarriesBucket(t *testing.T) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

	authz := &recordingAuthorizer{}
	h := &Handler{
		repo:    &uploadRepo{fakeObjectRepo: fakeObjectRepo{meta: BucketMeta{BackendID: "backend-7", BucketId: "bucket-7"}}},
		storage: noopStorage{},
		policy:  authz,
		presign: PresignConfig{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour},
	}

	if _, err := h.UploadObject(ctx, UploadObjectInput{Collection: "docs", Key: "a.txt", ContentType: "text/plain"}); err != nil {
		t.Fatalf("UploadObject: %v", err)
	}
	if authz.lastResource == nil {
		t.Fatal("authorizer was never called")
	}
	if authz.lastResource.BackendID != "backend-7" || authz.lastResource.BucketName != "bucket-7" {
		t.Fatalf("authz Resource missing physical binding: backend=%q bucket=%q — a bucket:/collection:-scoped PAT would be fail-closed on upload",
			authz.lastResource.BackendID, authz.lastResource.BucketName)
	}
	if authz.lastResource.Collection != "docs" || authz.lastResource.Key != "a.txt" {
		t.Fatalf("authz Resource identity wrong: collection=%q key=%q", authz.lastResource.Collection, authz.lastResource.Key)
	}
	if authz.lastAction != cedar.ActionPresignPut {
		t.Fatalf("authz action = %q, want %q", authz.lastAction, cedar.ActionPresignPut)
	}
}

// getRepo embeds fakeObjectRepo and implements the two methods GetObject
// touches: FindByName (returns an object under the requested object-key) and
// LookupBucket (resolves the object-key→bucket binding for the authz scope).
type getRepo struct {
	fakeObjectRepo
	bucket string
}

func (r *getRepo) FindByName(_ context.Context, _ uuid.UUID, collection, _ string) (Object, error) {
	return Object{ObjectID: uuid.Must(uuid.NewV7()), Collection: collection, Key: "k"}, nil
}
func (r *getRepo) LookupBucket(_ context.Context, _ uuid.UUID, _ string, _ bool) (string, string, error) {
	return "be", r.bucket, nil
}

// permitStore is a cedar.Store returning a blanket permit so only the
// scope-enforcement built-in decides — exercises the REAL engine end-to-end.
type permitStore struct{}

func (permitStore) Fetch(context.Context, uuid.UUID, string) (string, []byte, string, error) {
	return "permit(principal, action, resource);", nil, "", nil
}
func (permitStore) Watch(context.Context) (<-chan cedar.ChangeEvent, error) { return nil, nil }

// A read (GetObject) against the real engine: a bucket:/collection:-scoped PAT
// may read within its scoped object-key and is DENIED off-scope. This proves
// the resolved bucket reaches the Resource AND flips the engine's decision.
func TestGetObjectScopeEnforcedByEngine(t *testing.T) {
	tenantID := uuid.New()
	engine := cedar.NewEngine(permitStore{}, time.Minute)
	h := &Handler{repo: &getRepo{bucket: "bkt"}, policy: engine}
	okScope := auth.Scope{Type: auth.ScopeCollection, Value: "bkt/docs"}

	scopedCtx := func(scopes ...auth.Scope) context.Context {
		return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "svc", TenantID: tenantID, Scopes: scopes})
	}

	t.Run("allow on scoped object-key", func(t *testing.T) {
		if _, err := h.GetObject(scopedCtx(okScope), "docs", uuid.New().String()); err != nil {
			t.Fatalf("scoped PAT on its own object-key must be allowed, got %v", err)
		}
	})

	t.Run("deny off scoped object-key", func(t *testing.T) {
		_, err := h.GetObject(scopedCtx(okScope), "secret", uuid.New().String())
		if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("off-scope object-key must be denied, got %v", err)
		}
	})

	t.Run("unscoped principal unaffected", func(t *testing.T) {
		if _, err := h.GetObject(scopedCtx(), "secret", uuid.New().String()); err != nil {
			t.Fatalf("unscoped principal must be unaffected, got %v", err)
		}
	})
}

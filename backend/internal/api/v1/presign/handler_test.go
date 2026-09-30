package presign

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

// --- fakes ---------------------------------------------------------------

// fakeRepo is a configurable in-memory Repository. Each method delegates to
// a func field so a test can drive a specific branch; nil funcs return zero
// values so tests only wire the methods they exercise.
type fakeRepo struct {
	lookupObjectFn    func(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (string, string, string, error)
	lookupMultipartFn func(ctx context.Context, uploadID string) (string, string, string, error)
	lookupBucketFn    func(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error)

	// captured args from the last LookupBucket call.
	lastBucketWrite      bool
	lastBucketCollection string
}

func (f *fakeRepo) LookupObjectByName(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (string, string, string, error) {
	if f.lookupObjectFn == nil {
		return collection, "phys-key", "AVAILABLE", nil
	}
	return f.lookupObjectFn(ctx, tenantID, collection, objectID)
}

func (f *fakeRepo) LookupMultipartSession(ctx context.Context, uploadID string) (string, string, string, error) {
	if f.lookupMultipartFn == nil {
		return "s3-upload", "obj", "phys-key", nil
	}
	return f.lookupMultipartFn(ctx, uploadID)
}

func (f *fakeRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error) {
	f.lastBucketWrite = write
	f.lastBucketCollection = collection
	if f.lookupBucketFn == nil {
		return "backend-1", "bucket-1", nil
	}
	return f.lookupBucketFn(ctx, tenantID, collection, write)
}

// fakeStorage records the args the handler forwards so tests can assert the
// TTL-resolution and arg-plumbing contract without a real S3 presigner.
type fakeStorage struct {
	err error

	getCalled  bool
	putCalled  bool
	partCalled bool

	gotTTL         time.Duration
	gotDisposition string
	gotContentType string
	gotChecksum    string
	gotSizeHint    int64
	gotPartNumber  int32
	gotStorageUp   string
	gotBackendID   string
	gotBucket      string
}

func (f *fakeStorage) PresignGet(_ context.Context, backendID, bucket string, _ uuid.UUID, _, _ string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	f.getCalled = true
	f.gotTTL, f.gotDisposition, f.gotBackendID, f.gotBucket = ttl, disposition, backendID, bucket
	if f.err != nil {
		return "", nil, time.Time{}, f.err
	}
	return "https://s3/get", map[string]string{"h": "get"}, time.Unix(100, 0), nil
}

func (f *fakeStorage) PresignPut(_ context.Context, backendID, bucket string, _ uuid.UUID, _, _, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	f.putCalled = true
	f.gotTTL, f.gotContentType, f.gotChecksum, f.gotSizeHint = ttl, contentType, checksumAlgo, sizeHint
	f.gotBackendID, f.gotBucket = backendID, bucket
	if f.err != nil {
		return "", nil, time.Time{}, f.err
	}
	return "https://s3/put", map[string]string{"h": "put"}, time.Unix(200, 0), nil
}

func (f *fakeStorage) PresignPart(_ context.Context, backendID, bucket string, _ uuid.UUID, storageUploadID, _, _ string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	f.partCalled = true
	f.gotTTL, f.gotPartNumber, f.gotStorageUp = ttl, partNumber, storageUploadID
	f.gotBackendID, f.gotBucket = backendID, bucket
	if f.err != nil {
		return "", nil, time.Time{}, f.err
	}
	return "https://s3/part", map[string]string{"h": "part"}, time.Unix(300, 0), nil
}

// fakePolicy is a cedar.Authorizer whose decision/error are fixed per test.
// It records the action it was asked about so tests can assert the handler
// routes the correct cedar action per RPC.
type fakePolicy struct {
	decision    cedar.Decision
	err         error
	gotAction   string
	gotSubject  string
	gotResource *cedar.Resource
}

func (f *fakePolicy) IsAuthorized(_ context.Context, p *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.gotAction = action
	if r != nil {
		cp := *r
		f.gotResource = &cp
	}
	if p != nil {
		f.gotSubject = p.Subject
	}
	return f.decision, f.err
}

// allowPolicy is the common "authz permits" authorizer.
func allowPolicy() *fakePolicy { return &fakePolicy{decision: cedar.DecisionAllow} }

// --- context helpers -----------------------------------------------------

func authedCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tid})
}

// authedCtxWithCap authorises exactly `ops`; a missing op drives the
// PermissionDenied branch of auth.AssertCapabilityOp.
func authedCtxWithCap(tid uuid.UUID, ops ...capability.Op) context.Context {
	return auth.WithCapability(authedCtx(tid), &capability.Capability{
		Caveats: capability.Caveats{Ops: ops},
	})
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("error code: got %v, want %v (err=%v)", got, want, err)
	}
}

const validObjectID = "11111111-1111-1111-1111-111111111111"

// --- PresignGet ----------------------------------------------------------

func TestPresignGet(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(context.Background(), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("missing collection → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "", validObjectID, 0, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("missing object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", "", 0, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("unparseable object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", "not-a-uuid", 0, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("repo lookup error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupObjectFn: func(context.Context, uuid.UUID, string, uuid.UUID) (string, string, string, error) {
			return "", "", "", errors.New("no such object")
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("non-AVAILABLE state → failed precondition", func(t *testing.T) {
		repo := &fakeRepo{lookupObjectFn: func(_ context.Context, _ uuid.UUID, ok string, _ uuid.UUID) (string, string, string, error) {
			return ok, "phys", "PENDING", nil
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("capability lacks presign op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		ctx := authedCtxWithCap(tid, capability.OpGet) // has OpGet, missing OpPresign
		_, _, _, err := h.PresignGet(ctx, "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("capability has presign but lacks get op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		ctx := authedCtxWithCap(tid, capability.OpPresign) // missing OpGet
		_, _, _, err := h.PresignGet(ctx, "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, &fakePolicy{decision: cedar.DecisionDeny}, Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, &fakePolicy{err: errors.New("engine down")}, Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("bucket resolve error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "", "", errors.New("route missing")
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("disabled backend → failed precondition", func(t *testing.T) {
		repo := &fakeRepo{lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "", "", object.ErrBackendDisabled
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("ok forwards resolved TTL + disposition, reads from bucket", func(t *testing.T) {
		repo := &fakeRepo{}
		st := &fakeStorage{}
		pol := allowPolicy()
		// requested TTL over the max ceiling → clamped to MaxTTL.
		h := NewHandler(repo, st, pol, Config{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour})
		url, headers, exp, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 5*time.Hour, "inline")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !st.getCalled {
			t.Fatal("storage.PresignGet was not called")
		}
		if url != "https://s3/get" || headers["h"] != "get" || !exp.Equal(time.Unix(100, 0)) {
			t.Fatalf("passthrough mismatch: url=%q headers=%v exp=%v", url, headers, exp)
		}
		if st.gotTTL != 2*time.Hour {
			t.Fatalf("TTL not clamped to MaxTTL: got %v want %v", st.gotTTL, 2*time.Hour)
		}
		if st.gotDisposition != "inline" {
			t.Fatalf("disposition not forwarded: got %q", st.gotDisposition)
		}
		if repo.lastBucketWrite {
			t.Fatal("GET must resolve bucket with write=false")
		}
		// Read-scoped tokens need the bucket on the authz Resource too —
		// resolved BEFORE the Cedar check — or a bucket:/collection:-scoped
		// read PAT is fail-closed on presign-GET.
		if pol.gotResource == nil || pol.gotResource.BucketName != "bucket-1" || pol.gotResource.BackendID != "backend-1" {
			t.Fatalf("authz Resource missing physical binding: %+v", pol.gotResource)
		}
	})
}

// --- PresignPut ----------------------------------------------------------

func TestPresignPut(t *testing.T) {
	tid := uuid.New()

	// PresignPut requires PENDING state; the default fakeRepo returns
	// AVAILABLE, so callers that must clear the state gate use pendingRepo.
	pendingRepo := func() *fakeRepo {
		return &fakeRepo{lookupObjectFn: func(_ context.Context, _ uuid.UUID, ok string, _ uuid.UUID) (string, string, string, error) {
			return ok, "phys", "PENDING", nil
		}}
	}

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPut(context.Background(), "obj", validObjectID, "", "", 0, 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("missing object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPut(authedCtx(tid), "obj", "", "", "", 0, 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("unparseable object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPut(authedCtx(tid), "obj", "nope", "", "", 0, 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("AVAILABLE state rejects PUT → failed precondition", func(t *testing.T) {
		// default fakeRepo returns AVAILABLE; PUT only allowed on PENDING.
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPut(authedCtx(tid), "obj", validObjectID, "", "", 0, 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, allowPolicy(), Config{})
		ctx := authedCtxWithCap(tid, capability.OpPresign) // missing OpPut
		_, _, _, err := h.PresignPut(ctx, "obj", validObjectID, "", "", 0, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, &fakePolicy{decision: cedar.DecisionDeny}, Config{})
		_, _, _, err := h.PresignPut(authedCtx(tid), "obj", validObjectID, "", "", 0, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("bucket resolve error → not found", func(t *testing.T) {
		repo := pendingRepo()
		repo.lookupBucketFn = func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "", "", errors.New("route missing")
		}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPut(authedCtx(tid), "obj", validObjectID, "", "", 0, 0)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok forwards content-type/checksum/size + default TTL + write bucket", func(t *testing.T) {
		repo := pendingRepo()
		st := &fakeStorage{}
		pol := allowPolicy()
		// requested TTL 0 → DefaultTTL.
		h := NewHandler(repo, st, pol, Config{DefaultTTL: 30 * time.Minute, MaxTTL: time.Hour})
		url, headers, exp, err := h.PresignPut(authedCtx(tid), "obj", validObjectID, "image/png", "SHA256", 0, 4096)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !st.putCalled {
			t.Fatal("storage.PresignPut was not called")
		}
		if url != "https://s3/put" || headers["h"] != "put" || !exp.Equal(time.Unix(200, 0)) {
			t.Fatalf("passthrough mismatch: url=%q headers=%v exp=%v", url, headers, exp)
		}
		if st.gotTTL != 30*time.Minute {
			t.Fatalf("TTL: got %v want DefaultTTL 30m", st.gotTTL)
		}
		if st.gotContentType != "image/png" || st.gotChecksum != "SHA256" || st.gotSizeHint != 4096 {
			t.Fatalf("args not forwarded: ct=%q sum=%q size=%d", st.gotContentType, st.gotChecksum, st.gotSizeHint)
		}
		if !repo.lastBucketWrite {
			t.Fatal("PUT must resolve bucket with write=true")
		}
		if pol.gotAction != cedar.ActionPresignPut {
			t.Fatalf("cedar action: got %q want %q", pol.gotAction, cedar.ActionPresignPut)
		}
		// The bucket must be on the authz Resource — resolved BEFORE the Cedar
		// check — or a bucket:/collection:-scoped PAT is fail-closed on PUT.
		if pol.gotResource == nil || pol.gotResource.BucketName != "bucket-1" || pol.gotResource.BackendID != "backend-1" {
			t.Fatalf("authz Resource missing physical binding: %+v", pol.gotResource)
		}
	})
}

// --- PresignPart ---------------------------------------------------------

func TestPresignPart(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPart(context.Background(), "up-1", 1, 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("session lookup error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupMultipartFn: func(context.Context, string) (string, string, string, error) {
			return "", "", "", errors.New("no session")
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, 0)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("capability lacks presign op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		ctx := authedCtxWithCap(tid, capability.OpPut) // missing OpPresign
		_, _, _, err := h.PresignPart(ctx, "up-1", 1, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), Config{})
		ctx := authedCtxWithCap(tid, capability.OpPresign) // missing OpPut
		_, _, _, err := h.PresignPart(ctx, "up-1", 1, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, &fakePolicy{decision: cedar.DecisionDeny}, Config{})
		_, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("bucket resolve error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "", "", errors.New("route missing")
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), Config{})
		_, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, 0)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok forwards storage upload id + part number + resolved TTL", func(t *testing.T) {
		repo := &fakeRepo{lookupMultipartFn: func(context.Context, string) (string, string, string, error) {
			return "s3-up-99", "obj", "phys", nil
		}}
		st := &fakeStorage{}
		h := NewHandler(repo, st, allowPolicy(), Config{DefaultTTL: time.Hour, MaxTTL: 3 * time.Hour})
		url, headers, exp, err := h.PresignPart(authedCtx(tid), "up-1", 7, 2*time.Hour)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !st.partCalled {
			t.Fatal("storage.PresignPart was not called")
		}
		if url != "https://s3/part" || headers["h"] != "part" || !exp.Equal(time.Unix(300, 0)) {
			t.Fatalf("passthrough mismatch: url=%q headers=%v exp=%v", url, headers, exp)
		}
		if st.gotStorageUp != "s3-up-99" {
			t.Fatalf("storage upload id not forwarded: got %q", st.gotStorageUp)
		}
		if st.gotPartNumber != 7 {
			t.Fatalf("part number not forwarded: got %d want 7", st.gotPartNumber)
		}
		if st.gotTTL != 2*time.Hour { // within bounds → passthrough
			t.Fatalf("TTL: got %v want 2h", st.gotTTL)
		}
		if !repo.lastBucketWrite {
			t.Fatal("part upload must resolve bucket with write=true")
		}
	})

	t.Run("storage presigner error propagates", func(t *testing.T) {
		st := &fakeStorage{err: errors.New("sigv4 failed")}
		h := NewHandler(&fakeRepo{}, st, allowPolicy(), Config{})
		_, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, 0)
		if err == nil {
			t.Fatal("expected storage error to propagate")
		}
	})
}

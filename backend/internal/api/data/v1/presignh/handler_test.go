package presignh

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/capability"
)

// Lifetimes the tests sign under: distinct per operation, so a handler that
// resolved the wrong operation's default cannot pass by coincidence.
const (
	testGetTTL  = 10 * time.Minute
	testPutTTL  = 20 * time.Minute
	testPartTTL = 30 * time.Minute
	testMaxTTL  = 2 * time.Hour
)

func testConfig(t *testing.T) Config {
	t.Helper()
	p, err := presignttl.New(testGetTTL, testPutTTL, testPartTTL, testMaxTTL)
	if err != nil {
		t.Fatal(err)
	}
	return Config{TTL: p}
}

// --- fakes ---------------------------------------------------------------

// fakeRepo is a configurable in-memory Repository. Each method delegates to
// a func field so a test can drive a specific branch; nil funcs return a
// healthy default so tests only wire the methods they exercise.
type fakeRepo struct {
	lookupObjectFn func(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (ObjectRef, error)
	lookupMetaFn   func(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (objecth.BucketMeta, error)
	extendErr      error

	// captured args.
	lastBucketWrite bool
	extendCalled    bool
	extendTenant    uuid.UUID
	extendObject    uuid.UUID
	extendTo        time.Time
}

func (f *fakeRepo) LookupObject(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (ObjectRef, error) {
	if f.lookupObjectFn == nil {
		return ObjectRef{Collection: collection, Key: "phys-key", State: "AVAILABLE", ContentType: "image/png"}, nil
	}
	return f.lookupObjectFn(ctx, tenantID, collection, objectID)
}

func (f *fakeRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (objecth.BucketMeta, error) {
	f.lastBucketWrite = write
	if f.lookupMetaFn == nil {
		return objecth.BucketMeta{BackendID: "backend-1", BucketName: "bucket-1"}, nil
	}
	return f.lookupMetaFn(ctx, tenantID, collection, write)
}

func (f *fakeRepo) ExtendPendingPresign(_ context.Context, tenantID, objectID uuid.UUID, expiresAt time.Time) error {
	f.extendCalled = true
	f.extendTenant, f.extendObject, f.extendTo = tenantID, objectID, expiresAt
	return f.extendErr
}

// pendingRepo serves a PENDING object with a stored Content-Type.
func pendingRepo() *fakeRepo {
	return &fakeRepo{lookupObjectFn: func(_ context.Context, _ uuid.UUID, ok string, _ uuid.UUID) (ObjectRef, error) {
		return ObjectRef{Collection: ok, Key: "phys", State: "PENDING", ContentType: "image/png"}, nil
	}}
}

// fakeStorage records the args the handler forwards so tests can assert the
// TTL-resolution and arg-plumbing contract without a real S3 presigner.
type fakeStorage struct {
	err error

	getCalled bool
	putCalled bool

	gotTTL         time.Duration
	gotDisposition string
	gotContentType string
	gotBackendID   string
	gotBucket      string
}

// putExpiry is what the fake presigner reports as the PUT URL's expiry; the
// reaper deadline must be moved to exactly this instant.
var putExpiry = time.Unix(200, 0)

func (f *fakeStorage) PresignGet(_ context.Context, backendID, bucket string, _ uuid.UUID, _, _ string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	f.getCalled = true
	f.gotTTL, f.gotDisposition, f.gotBackendID, f.gotBucket = ttl, disposition, backendID, bucket
	if f.err != nil {
		return "", nil, time.Time{}, f.err
	}
	return "https://s3/get", map[string]string{"h": "get"}, time.Unix(100, 0), nil
}

func (f *fakeStorage) PresignPut(_ context.Context, backendID, bucket string, _ uuid.UUID, _, _, contentType, _ string, ttl time.Duration, _ int64) (string, map[string]string, time.Time, error) {
	f.putCalled = true
	f.gotTTL, f.gotContentType = ttl, contentType
	f.gotBackendID, f.gotBucket = backendID, bucket
	if f.err != nil {
		return "", nil, time.Time{}, f.err
	}
	return "https://s3/put", map[string]string{"h": "put"}, putExpiry, nil
}

// fakePolicy is a cedar.Authorizer whose decision/error are fixed per test.
// It records the action it was asked about so tests can assert the handler
// routes the correct cedar action per RPC.
type fakePolicy struct {
	decision    cedar.Decision
	err         error
	gotAction   string
	gotResource *cedar.Resource
}

func (f *fakePolicy) IsAuthorized(_ context.Context, _ *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.gotAction = action
	if r != nil {
		cp := *r
		f.gotResource = &cp
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
	cfg := testConfig(t)

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(context.Background(), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("missing collection → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "", validObjectID, 0, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("missing object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", "", 0, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("unparseable object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", "not-a-uuid", 0, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("repo lookup error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupObjectFn: func(context.Context, uuid.UUID, string, uuid.UUID) (ObjectRef, error) {
			return ObjectRef{}, errors.New("no such object")
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("non-AVAILABLE state → failed precondition", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("capability lacks presign op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		ctx := authedCtxWithCap(tid, capability.OpGet) // has OpGet, missing OpPresign
		_, _, _, err := h.PresignGet(ctx, "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("capability has presign but lacks get op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		ctx := authedCtxWithCap(tid, capability.OpPresign) // missing OpGet
		_, _, _, err := h.PresignGet(ctx, "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, &fakePolicy{decision: cedar.DecisionDeny}, cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, &fakePolicy{err: errors.New("engine down")}, cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("bucket resolve error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupMetaFn: func(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
			return objecth.BucketMeta{}, errors.New("route missing")
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("disabled backend → failed precondition", func(t *testing.T) {
		repo := &fakeRepo{lookupMetaFn: func(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
			return objecth.BucketMeta{}, objecth.ErrBackendDisabled
		}}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	// The proto has always said a TTL above max_ttl is InvalidArgument; the
	// handler clamped it instead, so a caller asking for five hours got two
	// and was not told. Nothing is signed for a refused TTL.
	t.Run("ttl above max_ttl → invalid argument, nothing signed", func(t *testing.T) {
		st := &fakeStorage{}
		h := NewHandler(&fakeRepo{}, st, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, testMaxTTL+time.Second, "")
		wantCode(t, err, connect.CodeInvalidArgument)
		if st.getCalled {
			t.Fatal("a URL was signed for a refused TTL")
		}
	})

	t.Run("negative ttl → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, -time.Second, "")
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("absent ttl → get_ttl", func(t *testing.T) {
		st := &fakeStorage{}
		h := NewHandler(&fakeRepo{}, st, allowPolicy(), cfg)
		if _, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, ""); err != nil {
			t.Fatal(err)
		}
		if st.gotTTL != testGetTTL {
			t.Fatalf("TTL = %v, want get_ttl %v", st.gotTTL, testGetTTL)
		}
	})

	t.Run("an unconfigured policy refuses rather than signs unbounded", func(t *testing.T) {
		st := &fakeStorage{}
		h := NewHandler(&fakeRepo{}, st, allowPolicy(), Config{})
		_, _, _, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "")
		wantCode(t, err, connect.CodeInternal)
		if st.getCalled {
			t.Fatal("signed with no TTL policy")
		}
	})

	t.Run("ok forwards requested TTL + disposition, reads from bucket", func(t *testing.T) {
		repo := &fakeRepo{}
		st := &fakeStorage{}
		pol := allowPolicy()
		h := NewHandler(repo, st, pol, cfg)
		url, headers, exp, err := h.PresignGet(authedCtx(tid), "obj", validObjectID, time.Hour, "inline")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if url != "https://s3/get" || headers["h"] != "get" || !exp.Equal(time.Unix(100, 0)) {
			t.Fatalf("passthrough mismatch: url=%q headers=%v exp=%v", url, headers, exp)
		}
		if st.gotTTL != time.Hour {
			t.Fatalf("TTL = %v, want the requested 1h", st.gotTTL)
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

// --- RegenerateUploadURL -------------------------------------------------

func TestRegenerateUploadURL(t *testing.T) {
	tid := uuid.New()
	cfg := testConfig(t)

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(context.Background(), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("missing object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", "", 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("unparseable object_id → invalid argument", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", "nope", 0)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("AVAILABLE state rejects PUT → failed precondition", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, allowPolicy(), cfg)
		ctx := authedCtxWithCap(tid, capability.OpPresign) // missing OpPut
		_, err := h.RegenerateUploadURL(ctx, "obj", validObjectID, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(pendingRepo(), &fakeStorage{}, &fakePolicy{decision: cedar.DecisionDeny}, cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("bucket resolve error → not found", func(t *testing.T) {
		repo := pendingRepo()
		repo.lookupMetaFn = func(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
			return objecth.BucketMeta{}, errors.New("route missing")
		}
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ttl above max_ttl → invalid argument", func(t *testing.T) {
		st := &fakeStorage{}
		h := NewHandler(pendingRepo(), st, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, testMaxTTL+time.Minute)
		wantCode(t, err, connect.CodeInvalidArgument)
		if st.putCalled {
			t.Fatal("a URL was signed for a refused TTL")
		}
	})

	t.Run("storage presigner error → internal, deadline untouched", func(t *testing.T) {
		repo := pendingRepo()
		h := NewHandler(repo, &fakeStorage{err: errors.New("sigv4 failed")}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeInternal)
		if repo.extendCalled {
			t.Fatal("the reaper deadline moved for a URL that was never issued")
		}
	})

	// The object can leave PENDING between the lookup and the deadline
	// update — completed by an event, failed by the reaper. The URL just
	// signed must not reach the caller then.
	t.Run("object left PENDING before the deadline moved → failed precondition", func(t *testing.T) {
		repo := pendingRepo()
		repo.extendErr = ErrNotPending
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("deadline store error → internal", func(t *testing.T) {
		repo := pendingRepo()
		repo.extendErr = errors.New("db down")
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		_, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		wantCode(t, err, connect.CodeInternal)
	})

	// The regression this RPC was broken by: the connect shim passed an empty
	// Content-Type, which the SDK signs as an empty header, so the
	// regenerated URL refused the Content-Type the original accepted. The
	// URL is signed with the type stored on the row.
	t.Run("signs the stored Content-Type", func(t *testing.T) {
		st := &fakeStorage{}
		h := NewHandler(pendingRepo(), st, allowPolicy(), cfg)
		if _, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0); err != nil {
			t.Fatal(err)
		}
		if st.gotContentType != "image/png" {
			t.Fatalf("signed Content-Type = %q, want the row's image/png", st.gotContentType)
		}
	})

	// The second regression: presign_expires_at stayed at the FIRST URL's
	// expiry, so the reaper failed the row under a client holding a fresh,
	// valid URL.
	t.Run("moves the reaper deadline to the new URL's expiry", func(t *testing.T) {
		repo := pendingRepo()
		h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
		if _, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0); err != nil {
			t.Fatal(err)
		}
		if !repo.extendCalled {
			t.Fatal("presign_expires_at was not moved")
		}
		if repo.extendTenant != tid || repo.extendObject.String() != validObjectID {
			t.Fatalf("deadline moved for tenant=%s object=%s, want %s/%s", repo.extendTenant, repo.extendObject, tid, validObjectID)
		}
		if !repo.extendTo.Equal(putExpiry) {
			t.Fatalf("deadline moved to %v, want the URL's expiry %v", repo.extendTo, putExpiry)
		}
	})

	completion := []struct {
		name   string
		events bool
		want   objecth.CompletionMode
	}{
		{"events backend → implicit", true, objecth.CompletionModeImplicit},
		{"no events → explicit", false, objecth.CompletionModeExplicit},
	}
	for _, tc := range completion {
		t.Run("completion mode: "+tc.name, func(t *testing.T) {
			repo := pendingRepo()
			repo.lookupMetaFn = func(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
				return objecth.BucketMeta{BackendID: "backend-1", BucketName: "bucket-1", EventsEnabled: tc.events}, nil
			}
			h := NewHandler(repo, &fakeStorage{}, allowPolicy(), cfg)
			out, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if out.CompletionMode != tc.want {
				t.Fatalf("completion mode = %v, want %v", out.CompletionMode, tc.want)
			}
		})
	}

	t.Run("ok: put_ttl by default, write bucket, presign-put action", func(t *testing.T) {
		repo := pendingRepo()
		st := &fakeStorage{}
		pol := allowPolicy()
		h := NewHandler(repo, st, pol, cfg)
		out, err := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if out.URL != "https://s3/put" || out.Headers["h"] != "put" || !out.ExpiresAt.Equal(putExpiry) {
			t.Fatalf("passthrough mismatch: %+v", out)
		}
		if st.gotTTL != testPutTTL {
			t.Fatalf("TTL = %v, want put_ttl %v", st.gotTTL, testPutTTL)
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

package multiparth

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
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/capability"
)

// --- fakes -----------------------------------------------------------------

// fakeRepo is a configurable in-memory Repository. Unset hooks fall back to
// benign defaults so happy-path tests only wire what they assert on; captured
// fields verify the tenant-stamping / routing contract without a database.
type fakeRepo struct {
	initiateSessionFn func(ctx context.Context, args InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (Session, error)
	getSessionFn      func(ctx context.Context, uploadID string) (Session, error)
	deleteSessionFn   func(ctx context.Context, uploadID string) error
	lookupBucketFn    func(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error)
	// constraints are the bucket constraints LookupBucketMeta reports.
	constraints uploadpolicy.BucketConstraints

	lastInitiate struct {
		args            InitiateArgs
		objectID        uuid.UUID
		storageUploadID string
		backendID       string
		bucket          string
	}
	lastLookup struct {
		tenantID   uuid.UUID
		collection string
		write      bool
	}
	deleteCalled bool
}

func (f *fakeRepo) InitiateSession(ctx context.Context, args InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (Session, error) {
	f.lastInitiate.args = args
	f.lastInitiate.objectID = objectID
	f.lastInitiate.storageUploadID = storageUploadID
	f.lastInitiate.backendID = backendID
	f.lastInitiate.bucket = bucket
	if f.initiateSessionFn != nil {
		return f.initiateSessionFn(ctx, args, objectID, storageUploadID, backendID, bucket)
	}
	return Session{
		UploadID:        "up-1",
		ObjectID:        objectID,
		TenantID:        args.TenantID,
		BackendID:       backendID,
		Bucket:          bucket,
		Collection:      args.Collection,
		Key:             args.Key,
		StorageUploadID: storageUploadID,
	}, nil
}

func (f *fakeRepo) GetSession(ctx context.Context, uploadID string) (Session, error) {
	if f.getSessionFn != nil {
		return f.getSessionFn(ctx, uploadID)
	}
	return Session{}, nil
}

func (f *fakeRepo) DeleteSession(ctx context.Context, uploadID string) error {
	f.deleteCalled = true
	if f.deleteSessionFn != nil {
		return f.deleteSessionFn(ctx, uploadID)
	}
	return nil
}

func (f *fakeRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (objecth.BucketMeta, error) {
	backendID, bucket, err := f.LookupBucket(ctx, tenantID, collection, write)
	return objecth.BucketMeta{BackendID: backendID, BucketName: bucket, Constraints: f.constraints}, err
}

func (f *fakeRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error) {
	f.lastLookup.tenantID = tenantID
	f.lastLookup.collection = collection
	f.lastLookup.write = write
	if f.lookupBucketFn != nil {
		return f.lookupBucketFn(ctx, tenantID, collection, write)
	}
	return "backend-x", "bucket-x", nil
}

// fakeStorage is a configurable Storage. Captured fields verify that the
// handler routes calls to the (backend, bucket) it resolved and forwards
// the storage upload id / parts / part-number / ttl unchanged.
type fakeStorage struct {
	initiateFn  func() (string, error)
	completeFn  func(parts []PartETag) (string, int64, error)
	abortFn     func() error
	presignFn   func() (string, map[string]string, time.Time, error)
	listPartsFn func(maxParts, after int32) ([]Part, int32, error)

	lastInitiate struct {
		backendID, bucket string
		tenantID          uuid.UUID
		collection, key   string
		contentType       string
	}
	lastComplete struct {
		backendID, bucket string
		storageUploadID   string
		parts             []PartETag
	}
	lastAbort struct {
		backendID, bucket string
		storageUploadID   string
	}
	lastPresign struct {
		backendID, bucket string
		storageUploadID   string
		partNumber        int32
		ttl               time.Duration
	}
	lastListParts struct {
		backendID, bucket string
		storageUploadID   string
		maxParts, after   int32
	}
	abortCalled bool
}

func (f *fakeStorage) ListMultipartParts(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, maxParts, after int32) ([]Part, int32, error) {
	f.lastListParts.backendID = backendID
	f.lastListParts.bucket = bucket
	f.lastListParts.storageUploadID = storageUploadID
	f.lastListParts.maxParts = maxParts
	f.lastListParts.after = after
	if f.listPartsFn != nil {
		return f.listPartsFn(maxParts, after)
	}
	return nil, 0, nil
}

func (f *fakeStorage) InitiateMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, contentType string) (string, error) {
	f.lastInitiate.backendID = backendID
	f.lastInitiate.bucket = bucket
	f.lastInitiate.tenantID = tenantID
	f.lastInitiate.collection = collection
	f.lastInitiate.key = key
	f.lastInitiate.contentType = contentType
	if f.initiateFn != nil {
		return f.initiateFn()
	}
	return "storage-up-1", nil
}

func (f *fakeStorage) CompleteMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, parts []PartETag) (string, int64, error) {
	f.lastComplete.backendID = backendID
	f.lastComplete.bucket = bucket
	f.lastComplete.storageUploadID = storageUploadID
	f.lastComplete.parts = parts
	if f.completeFn != nil {
		return f.completeFn(parts)
	}
	return "etag-1", 123, nil
}

func (f *fakeStorage) AbortMultipart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string) error {
	f.abortCalled = true
	f.lastAbort.backendID = backendID
	f.lastAbort.bucket = bucket
	f.lastAbort.storageUploadID = storageUploadID
	if f.abortFn != nil {
		return f.abortFn()
	}
	return nil
}

func (f *fakeStorage) PresignPart(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	f.lastPresign.backendID = backendID
	f.lastPresign.bucket = bucket
	f.lastPresign.storageUploadID = storageUploadID
	f.lastPresign.partNumber = partNumber
	f.lastPresign.ttl = ttl
	if f.presignFn != nil {
		return f.presignFn()
	}
	return "https://example/presigned", map[string]string{"h": "v"}, time.Unix(1700000000, 0), nil
}

// fakeAuthorizer is a configurable cedar.Authorizer.
type fakeAuthorizer struct {
	decision cedar.Decision
	err      error

	lastAction   string
	lastResource *cedar.Resource
}

func (f *fakeAuthorizer) IsAuthorized(ctx context.Context, p *cedar.Principal, action string, r *cedar.Resource, rc cedar.RequestContext) (cedar.Decision, error) {
	f.lastAction = action
	f.lastResource = r
	return f.decision, f.err
}

// --- helpers ---------------------------------------------------------------

func allow() *fakeAuthorizer { return &fakeAuthorizer{decision: cedar.DecisionAllow} }

// newHandler wires a Handler with a nil-pool Transitioner. The statemachine is
// only reached on the DB-coupled success tails of Complete/Abort, which these
// unit tests deliberately stop short of, so it is never dereferenced.
func newHandler(repo Repository, storage Storage, authz cedar.Authorizer) *Handler {
	return NewHandler(repo, storage, authz, statemachine.New(nil), testTTLPolicy(), testUploadLimits)
}

// testUploadLimits are S3's own limits: permissive, so a test that exercises
// a limit sets it on the bucket.
var testUploadLimits = uploadpolicy.Limits{
	MaxObjectSize: uploadpolicy.S3MaxPartSize, MaxMultipartSize: 1 << 40,
	MinPartSize: uploadpolicy.S3MinPartSize, MaxPartSize: uploadpolicy.S3MaxPartSize,
	MaxParts: uploadpolicy.S3MaxParts,
}

// Part URL lifetimes the tests sign under: part_ttl distinct from the other
// operations' defaults so the handler cannot pass by resolving the wrong one.
const (
	testPartTTL = 25 * time.Minute
	testMaxTTL  = time.Hour
)

func testTTLPolicy() presignttl.Policy {
	p, err := presignttl.New(time.Minute, 2*time.Minute, testPartTTL, testMaxTTL)
	if err != nil {
		panic(err)
	}
	return p
}

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

func sessionForTenant(tid uuid.UUID) Session {
	return Session{
		UploadID:        "up-1",
		ObjectID:        uuid.Must(uuid.NewV7()),
		TenantID:        tid,
		BackendID:       "backend-sess",
		Bucket:          "bucket-sess",
		Collection:      "photos",
		Key:             "cat.jpg",
		StorageUploadID: "storage-up-9",
		TotalParts:      5,
	}
}

// --- InitiateMultipartUpload -----------------------------------------------

func TestInitiateMultipartUpload(t *testing.T) {
	tid := uuid.New()
	base := InitiateArgs{Collection: "photos", Key: "cat.jpg", ContentType: "image/jpeg", SizeHint: 1024}

	t.Run("unauthenticated", func(t *testing.T) {
		_, err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			InitiateMultipartUpload(context.Background(), base)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("missing size hint → invalid argument", func(t *testing.T) {
		args := base
		args.SizeHint = 0
		_, err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			InitiateMultipartUpload(authedCtx(tid), args)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpPut
		_, err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			InitiateMultipartUpload(ctx, base)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		authz := &fakeAuthorizer{decision: cedar.DecisionDeny}
		_, err := newHandler(&fakeRepo{}, &fakeStorage{}, authz).
			InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodePermissionDenied)
		if authz.lastAction != cedar.ActionPutObject {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionPutObject)
		}
	})

	t.Run("policy error → internal", func(t *testing.T) {
		authz := &fakeAuthorizer{err: errors.New("engine boom")}
		_, err := newHandler(&fakeRepo{}, &fakeStorage{}, authz).
			InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("bucket resolve error → not found", func(t *testing.T) {
		repo := &fakeRepo{lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "", "", errors.New("no bucket")
		}}
		_, err := newHandler(repo, &fakeStorage{}, allow()).
			InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("bucket disabled → failed precondition", func(t *testing.T) {
		repo := &fakeRepo{lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "", "", objecth.ErrBackendDisabled
		}}
		_, err := newHandler(repo, &fakeStorage{}, allow()).
			InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("storage initiate error → internal", func(t *testing.T) {
		storage := &fakeStorage{initiateFn: func() (string, error) { return "", errors.New("s3 down") }}
		_, err := newHandler(&fakeRepo{}, storage, allow()).
			InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("session persist error rolls back storage → internal", func(t *testing.T) {
		storage := &fakeStorage{initiateFn: func() (string, error) { return "SUP-42", nil }}
		repo := &fakeRepo{initiateSessionFn: func(context.Context, InitiateArgs, uuid.UUID, string, string, string) (Session, error) {
			return Session{}, errors.New("insert failed")
		}}
		_, err := newHandler(repo, storage, allow()).
			InitiateMultipartUpload(authedCtx(tid), base)
		wantCode(t, err, connect.CodeInternal)
		if !storage.abortCalled {
			t.Fatal("expected orphan storage session to be aborted on persist failure")
		}
		if storage.lastAbort.storageUploadID != "SUP-42" {
			t.Fatalf("rollback aborted wrong upload: got %q want SUP-42", storage.lastAbort.storageUploadID)
		}
	})

	t.Run("ok stamps tenant and routes storage + persist", func(t *testing.T) {
		storage := &fakeStorage{initiateFn: func() (string, error) { return "SUP-77", nil }}
		repo := &fakeRepo{lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
			return "backend-a", "bucket-a", nil
		}}
		args := base
		args.TenantID = uuid.New() // must be overwritten by ctx tenant
		authz := allow()
		sess, err := newHandler(repo, storage, authz).
			InitiateMultipartUpload(authedCtx(tid), args)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if sess == nil {
			t.Fatal("expected session, got nil")
		}
		if repo.lastInitiate.args.TenantID != tid {
			t.Fatalf("tenant not stamped from ctx: got %v want %v", repo.lastInitiate.args.TenantID, tid)
		}
		if !repo.lastLookup.write {
			t.Fatal("bucket lookup should request write routing for a mutation")
		}
		// The bucket must be resolved BEFORE authz and stamped on the Resource,
		// or a bucket:/collection:-scoped PAT is fail-closed on multipart init.
		if authz.lastResource == nil || authz.lastResource.BucketName != "bucket-a" || authz.lastResource.BackendID != "backend-a" {
			t.Fatalf("authz Resource missing physical binding: %+v", authz.lastResource)
		}
		if repo.lastLookup.collection != base.Collection {
			t.Fatalf("lookup object key: got %q want %q", repo.lastLookup.collection, base.Collection)
		}
		if storage.lastInitiate.backendID != "backend-a" || storage.lastInitiate.bucket != "bucket-a" {
			t.Fatalf("storage not routed to resolved backend/bucket: got %q/%q", storage.lastInitiate.backendID, storage.lastInitiate.bucket)
		}
		if repo.lastInitiate.storageUploadID != "SUP-77" {
			t.Fatalf("persist got wrong storage upload id: %q", repo.lastInitiate.storageUploadID)
		}
		if repo.lastInitiate.backendID != "backend-a" || repo.lastInitiate.bucket != "bucket-a" {
			t.Fatalf("persist got wrong backend/bucket: %q/%q", repo.lastInitiate.backendID, repo.lastInitiate.bucket)
		}
	})
}

// --- CompleteMultipartUpload -----------------------------------------------
//
// The success tail (sm.PromoteToAvailable) is DB-coupled, so these cover the
// auth + session + storage error branches that precede it.

func TestCompleteMultipartUpload(t *testing.T) {
	tid := uuid.New()
	okSession := func() *fakeRepo {
		return &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return sessionForTenant(tid), nil
		}}
	}

	t.Run("unauthenticated", func(t *testing.T) {
		err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			CompleteMultipartUpload(context.Background(), CompleteArgs{UploadID: "up-1"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("session missing → not found", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return Session{}, errors.New("no session")
		}}
		err := newHandler(repo, &fakeStorage{}, allow()).
			CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1"})
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpPut
		err := newHandler(okSession(), &fakeStorage{}, allow()).
			CompleteMultipartUpload(ctx, CompleteArgs{UploadID: "up-1"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		err := newHandler(okSession(), &fakeStorage{}, &fakeAuthorizer{decision: cedar.DecisionDeny}).
			CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("bucket resolve error (empty session bucket) → not found", func(t *testing.T) {
		repo := &fakeRepo{
			getSessionFn: func(context.Context, string) (Session, error) {
				s := sessionForTenant(tid)
				s.Bucket = "" // force LookupBucket path
				return s, nil
			},
			lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
				return "", "", errors.New("gone")
			},
		}
		err := newHandler(repo, &fakeStorage{}, allow()).
			CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1"})
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("storage complete error → internal, forwards parts", func(t *testing.T) {
		parts := []PartETag{{PartNumber: 1, ETag: "e1"}, {PartNumber: 2, ETag: "e2"}}
		storage := &fakeStorage{completeFn: func([]PartETag) (string, int64, error) {
			return "", 0, errors.New("multipart complete rejected")
		}}
		authz := allow()
		err := newHandler(okSession(), storage, authz).
			CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: parts})
		wantCode(t, err, connect.CodeInternal)
		if len(storage.lastComplete.parts) != 2 || storage.lastComplete.parts[1].ETag != "e2" {
			t.Fatalf("parts not forwarded to storage: %+v", storage.lastComplete.parts)
		}
		if storage.lastComplete.storageUploadID != "storage-up-9" {
			t.Fatalf("storage upload id not forwarded: %q", storage.lastComplete.storageUploadID)
		}
		if storage.lastComplete.bucket != "bucket-sess" {
			t.Fatalf("storage not routed to session bucket: %q", storage.lastComplete.bucket)
		}
		// The session-anchored (backend, bucket) must reach the authz Resource
		// so a bucket:/collection:-scoped PAT enforces on complete.
		if authz.lastResource == nil || authz.lastResource.BucketName != "bucket-sess" || authz.lastResource.BackendID != "backend-sess" {
			t.Fatalf("authz Resource missing session binding: %+v", authz.lastResource)
		}
	})
}

// --- AbortMultipartUpload --------------------------------------------------
//
// Success tail (sm.MarkFailed) is DB-coupled; these cover the branches before.

func TestAbortMultipartUpload(t *testing.T) {
	tid := uuid.New()
	okSession := func() *fakeRepo {
		return &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return sessionForTenant(tid), nil
		}}
	}

	t.Run("unauthenticated", func(t *testing.T) {
		err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			AbortMultipartUpload(context.Background(), "up-1", SessionRef{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("session missing → not found", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return Session{}, errors.New("no session")
		}}
		err := newHandler(repo, &fakeStorage{}, allow()).
			AbortMultipartUpload(authedCtx(tid), "up-1", SessionRef{})
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("capability lacks delete op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpDelete
		err := newHandler(okSession(), &fakeStorage{}, allow()).
			AbortMultipartUpload(ctx, "up-1", SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		authz := &fakeAuthorizer{decision: cedar.DecisionDeny}
		err := newHandler(okSession(), &fakeStorage{}, authz).
			AbortMultipartUpload(authedCtx(tid), "up-1", SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
		if authz.lastAction != cedar.ActionDeleteObject {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionDeleteObject)
		}
	})

	t.Run("storage abort error → internal, forwards upload id", func(t *testing.T) {
		storage := &fakeStorage{abortFn: func() error { return errors.New("abort rejected") }}
		err := newHandler(okSession(), storage, allow()).
			AbortMultipartUpload(authedCtx(tid), "up-1", SessionRef{})
		wantCode(t, err, connect.CodeInternal)
		if storage.lastAbort.storageUploadID != "storage-up-9" {
			t.Fatalf("storage upload id not forwarded: %q", storage.lastAbort.storageUploadID)
		}
	})
}

// --- PresignPart -----------------------------------------------------------
//
// No sm dependency: fully unit-testable including the happy path.

func TestPresignPart(t *testing.T) {
	tid := uuid.New()
	okSession := func() *fakeRepo {
		return &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return sessionForTenant(tid), nil
		}}
	}

	t.Run("unauthenticated", func(t *testing.T) {
		_, _, _, err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			PresignPart(context.Background(), "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("session missing → not found", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return Session{}, errors.New("no session")
		}}
		_, _, _, err := newHandler(repo, &fakeStorage{}, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("tenant mismatch → permission denied", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return sessionForTenant(uuid.New()), nil // different tenant
		}}
		_, _, _, err := newHandler(repo, &fakeStorage{}, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("part number zero → invalid argument", func(t *testing.T) {
		_, _, _, err := newHandler(okSession(), &fakeStorage{}, allow()).
			PresignPart(authedCtx(tid), "up-1", 0, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("part number above total → invalid argument", func(t *testing.T) {
		_, _, _, err := newHandler(okSession(), &fakeStorage{}, allow()).
			PresignPart(authedCtx(tid), "up-1", 6, time.Minute, SessionRef{}) // TotalParts=5
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks presign op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpPut) // has put, not presign
		_, _, _, err := newHandler(okSession(), &fakeStorage{}, allow()).
			PresignPart(ctx, "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpPresign) // has presign, not put
		_, _, _, err := newHandler(okSession(), &fakeStorage{}, allow()).
			PresignPart(ctx, "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		authz := &fakeAuthorizer{decision: cedar.DecisionDeny}
		_, _, _, err := newHandler(okSession(), &fakeStorage{}, authz).
			PresignPart(authedCtx(tid), "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
		if authz.lastAction != cedar.ActionPresignPut {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionPresignPut)
		}
	})

	t.Run("bucket resolve error (empty session bucket) → not found", func(t *testing.T) {
		repo := &fakeRepo{
			getSessionFn: func(context.Context, string) (Session, error) {
				s := sessionForTenant(tid)
				s.Bucket = ""
				return s, nil
			},
			lookupBucketFn: func(context.Context, uuid.UUID, string, bool) (string, string, error) {
				return "", "", errors.New("gone")
			},
		}
		_, _, _, err := newHandler(repo, &fakeStorage{}, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, time.Minute, SessionRef{})
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok returns storage url and routes to session bucket", func(t *testing.T) {
		storage := &fakeStorage{presignFn: func() (string, map[string]string, time.Time, error) {
			return "https://s3/presigned-part", map[string]string{"Content-Length": "5"}, time.Unix(1700000123, 0), nil
		}}
		url, headers, exp, err := newHandler(okSession(), storage, allow()).
			PresignPart(authedCtx(tid), "up-1", 3, 7*time.Minute, SessionRef{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if url != "https://s3/presigned-part" {
			t.Fatalf("url passthrough: got %q", url)
		}
		if headers["Content-Length"] != "5" {
			t.Fatalf("headers passthrough: got %+v", headers)
		}
		if exp != time.Unix(1700000123, 0) {
			t.Fatalf("expiry passthrough: got %v", exp)
		}
		if storage.lastPresign.partNumber != 3 {
			t.Fatalf("part number not forwarded: got %d", storage.lastPresign.partNumber)
		}
		if storage.lastPresign.ttl != 7*time.Minute {
			t.Fatalf("ttl not forwarded: got %v", storage.lastPresign.ttl)
		}
		if storage.lastPresign.backendID != "backend-sess" || storage.lastPresign.bucket != "bucket-sess" {
			t.Fatalf("not routed to session backend/bucket: %q/%q", storage.lastPresign.backendID, storage.lastPresign.bucket)
		}
	})

	t.Run("absent ttl → part_ttl", func(t *testing.T) {
		storage := &fakeStorage{}
		_, _, _, err := newHandler(okSession(), storage, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, 0, SessionRef{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if storage.lastPresign.ttl != testPartTTL {
			t.Fatalf("default ttl: got %v want part_ttl %v", storage.lastPresign.ttl, testPartTTL)
		}
	})

	// Part URLs were the one presign with no ceiling: the requested TTL went
	// to the signer as-is, so a caller could hold a week-long part URL under
	// an operator's max_ttl of an hour. Above the ceiling, or negative, is
	// now InvalidArgument, and nothing is signed.
	for _, tc := range []struct {
		name string
		ttl  time.Duration
	}{
		{"above max_ttl", testMaxTTL + time.Second},
		{"a week", 7 * 24 * time.Hour},
		{"negative", -time.Second},
	} {
		t.Run("ttl "+tc.name+" → invalid argument", func(t *testing.T) {
			storage := &fakeStorage{}
			_, _, _, err := newHandler(okSession(), storage, allow()).
				PresignPart(authedCtx(tid), "up-1", 1, tc.ttl, SessionRef{})
			wantCode(t, err, connect.CodeInvalidArgument)
			if storage.lastPresign.ttl != 0 {
				t.Fatalf("a part URL was signed (ttl %v) despite the refusal", storage.lastPresign.ttl)
			}
		})
	}

	t.Run("ttl at max_ttl is honoured", func(t *testing.T) {
		storage := &fakeStorage{}
		if _, _, _, err := newHandler(okSession(), storage, allow()).
			PresignPart(authedCtx(tid), "up-1", 1, testMaxTTL, SessionRef{}); err != nil {
			t.Fatal(err)
		}
		if storage.lastPresign.ttl != testMaxTTL {
			t.Fatalf("ttl = %v, want %v", storage.lastPresign.ttl, testMaxTTL)
		}
	})
}

// --- ListParts -------------------------------------------------------------
//
// No sm dependency: fully unit-testable including the happy path.

func TestListParts(t *testing.T) {
	tid := uuid.New()
	okSession := func(extra func(*fakeRepo)) *fakeRepo {
		r := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return sessionForTenant(tid), nil
		}}
		if extra != nil {
			extra(r)
		}
		return r
	}

	t.Run("unauthenticated", func(t *testing.T) {
		_, _, err := newHandler(&fakeRepo{}, &fakeStorage{}, allow()).
			ListParts(context.Background(), "up-1", 10, "", SessionRef{})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("session missing → not found", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return Session{}, errors.New("no session")
		}}
		_, _, err := newHandler(repo, &fakeStorage{}, allow()).
			ListParts(authedCtx(tid), "up-1", 10, "", SessionRef{})
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("tenant mismatch → permission denied", func(t *testing.T) {
		repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
			return sessionForTenant(uuid.New()), nil
		}}
		_, _, err := newHandler(repo, &fakeStorage{}, allow()).
			ListParts(authedCtx(tid), "up-1", 10, "", SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("capability lacks list op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpList
		_, _, err := newHandler(okSession(nil), &fakeStorage{}, allow()).
			ListParts(ctx, "up-1", 10, "", SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		authz := &fakeAuthorizer{decision: cedar.DecisionDeny}
		_, _, err := newHandler(okSession(nil), &fakeStorage{}, authz).
			ListParts(authedCtx(tid), "up-1", 10, "", SessionRef{})
		wantCode(t, err, connect.CodePermissionDenied)
		if authz.lastAction != cedar.ActionGetObject {
			t.Fatalf("cedar action: got %q want %q", authz.lastAction, cedar.ActionGetObject)
		}
	})

	t.Run("backend error → internal", func(t *testing.T) {
		storage := &fakeStorage{listPartsFn: func(int32, int32) ([]Part, int32, error) {
			return nil, 0, errors.New("list failed")
		}}
		_, _, err := newHandler(okSession(nil), storage, allow()).
			ListParts(authedCtx(tid), "up-1", 10, "", SessionRef{})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok passes through result and routes to the session's backend", func(t *testing.T) {
		storage := &fakeStorage{listPartsFn: func(int32, int32) ([]Part, int32, error) {
			return []Part{{PartNumber: 1, ETag: "e1"}}, 7, nil
		}}
		parts, next, err := newHandler(okSession(nil), storage, allow()).
			ListParts(authedCtx(tid), "up-1", 25, "3", SessionRef{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(parts) != 1 || parts[0].ETag != "e1" {
			t.Fatalf("passthrough failed: parts=%+v", parts)
		}
		// The next token is the backend's last part number, not an opaque
		// cursor — a resuming client can read it as "everything up to N".
		if next != "7" {
			t.Fatalf("next token: got %q want %q", next, "7")
		}
		if storage.lastListParts.maxParts != 25 || storage.lastListParts.after != 3 {
			t.Fatalf("paging not forwarded: max=%d after=%d",
				storage.lastListParts.maxParts, storage.lastListParts.after)
		}
		// Listing must go to the backend the session was opened against,
		// never to a default — a session on backend B listed against A would
		// report an upload that does not exist there.
		sess := sessionForTenant(tid)
		if storage.lastListParts.backendID != sess.BackendID ||
			storage.lastListParts.bucket != sess.Bucket ||
			storage.lastListParts.storageUploadID != sess.StorageUploadID {
			t.Fatalf("not routed to the session's backend: %+v", storage.lastListParts)
		}
	})

	t.Run("no next token when the backend has no more parts", func(t *testing.T) {
		storage := &fakeStorage{listPartsFn: func(int32, int32) ([]Part, int32, error) {
			return []Part{{PartNumber: 1}}, 0, nil
		}}
		_, next, err := newHandler(okSession(nil), storage, allow()).
			ListParts(authedCtx(tid), "up-1", 10, "", SessionRef{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if next != "" {
			t.Fatalf("next token %q on an untruncated listing", next)
		}
	})

	t.Run("malformed page token → invalid argument", func(t *testing.T) {
		for _, tok := range []string{"cursor-in", "-1", "1.5", "99999999999999999999"} {
			_, _, err := newHandler(okSession(nil), &fakeStorage{}, allow()).
				ListParts(authedCtx(tid), "up-1", 10, tok, SessionRef{})
			wantCode(t, err, connect.CodeInvalidArgument)
		}
	})

	t.Run("page size clamped when non-positive", func(t *testing.T) {
		storage := &fakeStorage{}
		if _, _, err := newHandler(okSession(nil), storage, allow()).
			ListParts(authedCtx(tid), "up-1", 0, "", SessionRef{}); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if storage.lastListParts.maxParts != 100 {
			t.Fatalf("page size clamp: got %d want 100", storage.lastListParts.maxParts)
		}
	})

	t.Run("page size clamped when over max", func(t *testing.T) {
		storage := &fakeStorage{}
		if _, _, err := newHandler(okSession(nil), storage, allow()).
			ListParts(authedCtx(tid), "up-1", 5000, "", SessionRef{}); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if storage.lastListParts.maxParts != 100 {
			t.Fatalf("page size clamp: got %d want 100", storage.lastListParts.maxParts)
		}
	})
}

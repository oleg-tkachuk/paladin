package objecth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// CompleteObject and CopyObject were the last two of the thirteen handlers at
// 0.0% (BACKLOG: "Half the admin API's RPCs have no behavioural test").
//
// Both end in the promote path. What is covered HERE is everything up to it:
// the argument guards, the state guards, and the authz Resource each hands
// the engine — the half where a mistake is a security bug rather than a
// broken write. For CompleteObject that includes a genuine terminal path: the
// already-AVAILABLE no-op that makes a client's at-least-once retry safe.
//
// The promote path itself and everything after it live in
// promote_tail_test.go; they became reachable when the handler stopped
// holding a concrete *statemachine.Transitioner.

type completeRepo struct {
	fakeObjectRepo

	obj       Object
	findErr   error
	bucketErr error
	writeErr  error // returned from LookupBucket when write=true
	createErr error
	created   *CreateObjectArgs
	// constraints are the destination bucket's upload constraints.
	constraints uploadpolicy.BucketConstraints
}

func (r *completeRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	if r.findErr != nil {
		return Object{}, r.findErr
	}
	return r.obj, nil
}

func (r *completeRepo) LookupBucket(_ context.Context, _ uuid.UUID, _ string, write bool) (string, string, error) {
	if write && r.writeErr != nil {
		return "", "", r.writeErr
	}
	if r.bucketErr != nil {
		return "", "", r.bucketErr
	}
	return "backend-7", "bucket-7", nil
}

// LookupBucketMeta answers the embedded meta (versioning flags), falling back
// to LookupBucket's binding when the test set none, plus the test's
// constraints.
func (r *completeRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (BucketMeta, error) {
	meta, err := r.fakeObjectRepo.LookupBucketMeta(ctx, tenantID, collection, write)
	if err != nil {
		return BucketMeta{}, err
	}
	if meta.BucketName == "" {
		if meta.BackendID, meta.BucketName, err = r.LookupBucket(ctx, tenantID, collection, write); err != nil {
			return BucketMeta{}, err
		}
	}
	meta.Constraints = r.constraints
	return meta, nil
}

func (r *completeRepo) CreateObject(_ context.Context, a CreateObjectArgs) (Object, error) {
	cp := a
	r.created = &cp
	if r.createErr != nil {
		return Object{}, r.createErr
	}
	return Object{ObjectID: uuid.Must(uuid.NewV7()), Collection: a.Collection, Key: a.Key}, nil
}

// headStorage answers HEAD with values the test chooses.
type headStorage struct {
	noopStorage
	etag string
	size int64
	err  error
}

func (s headStorage) Head(context.Context, string, string, uuid.UUID, string, string, string) (string, int64, string, string, error) {
	if s.err != nil {
		return "", 0, "", "", s.err
	}
	return s.etag, s.size, "", "", nil
}

func completeHandler(repo *completeRepo, storage Storage) (*Handler, *recordingAuthorizer, context.Context, uuid.UUID) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	authz := &recordingAuthorizer{}
	h := &Handler{
		repo: repo, storage: storage, policy: authz,
		presign: testPresignConfig(),
	}
	return h, authz, ctx, tenantID
}

// ─── CompleteObject ─────────────────────────────────────────────────────────

func TestCompleteObjectIsANoOpOnAnAlreadyAvailableObject(t *testing.T) {
	// The event-driven path may have promoted the object before the client's
	// explicit Complete arrives — or the client may simply be retrying. Either
	// way the answer is the object, not an error: this is what makes
	// at-least-once delivery safe on this RPC.
	obj := Object{
		ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", Key: "a.txt",
		State: statemachine.StateAvailable, SizeBytes: 9,
	}
	h, _, ctx, _ := completeHandler(&completeRepo{obj: obj}, headStorage{})

	got, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()})
	if err != nil {
		t.Fatalf("CompleteObject on an AVAILABLE object: %v", err)
	}
	if got.ObjectID != obj.ObjectID || got.State != statemachine.StateAvailable {
		t.Errorf("got %+v, want the already-promoted object back unchanged", got)
	}
}

func TestCompleteObjectAuthzResourceCarriesBucket(t *testing.T) {
	obj := Object{
		ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", Key: "a.txt",
		State: statemachine.StateAvailable, SizeBytes: 9, ContentType: "text/plain",
	}
	h, authz, ctx, _ := completeHandler(&completeRepo{obj: obj}, headStorage{})

	if _, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()}); err != nil {
		t.Fatalf("CompleteObject: %v", err)
	}
	if authz.lastResource == nil || authz.lastResource.BucketName != "bucket-7" {
		t.Error("authz Resource missing the bucket binding — a scoped PAT would be fail-closed on completion")
	}
	if authz.lastAction != cedar.ActionPutObject {
		t.Errorf("action = %q, want %q — completion is a write", authz.lastAction, cedar.ActionPutObject)
	}
}

func TestCompleteObjectSurvivesAnUnresolvableBucketOnTheNoOpPath(t *testing.T) {
	// The idempotent no-op predates the bucket binding and must not start
	// failing for callers whose collection resolves to nothing: the read-side
	// lookup error is dropped on purpose.
	obj := Object{ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", State: statemachine.StateAvailable}
	h, authz, ctx, _ := completeHandler(&completeRepo{obj: obj, bucketErr: errors.New("no binding")}, headStorage{})

	if _, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()}); err != nil {
		t.Fatalf("an unresolvable binding newly failed an idempotent completion: %v", err)
	}
	if authz.lastResource.BucketName != "" {
		t.Errorf("bucket = %q, want empty when nothing resolved", authz.lastResource.BucketName)
	}
}

func TestCompleteObjectRefusesATerminalState(t *testing.T) {
	for _, state := range []statemachine.State{statemachine.StateDeleted, statemachine.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			obj := Object{ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", State: state}
			h, _, ctx, _ := completeHandler(&completeRepo{obj: obj}, headStorage{})

			_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()})
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition for %s", connect.CodeOf(err), state)
			}
		})
	}
}

func TestCompleteObjectRefusesWhenNothingWasUploaded(t *testing.T) {
	// PENDING plus a HEAD that finds nothing means the client is completing an
	// upload it never made. FailedPrecondition, not Internal: the caller can
	// fix it by uploading.
	obj := Object{ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", State: statemachine.StatePending}
	h, _, ctx, _ := completeHandler(&completeRepo{obj: obj}, headStorage{err: errors.New("404")})

	_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestCompleteObjectRefusesAnETagMismatch(t *testing.T) {
	// The client says what it uploaded; the backend says what it holds. A
	// mismatch means the bytes are not the ones being promised.
	obj := Object{ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", State: statemachine.StatePending}
	h, _, ctx, _ := completeHandler(&completeRepo{obj: obj}, headStorage{etag: "actual", size: 4})

	_, err := h.CompleteObject(ctx, CompleteObjectInput{
		Collection: "docs", ObjectID: obj.ObjectID.String(), ETag: "claimed",
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestCompleteObjectRequiresCollectionAndObjectID(t *testing.T) {
	h, _, ctx, _ := completeHandler(&completeRepo{}, headStorage{})

	if _, err := h.CompleteObject(ctx, CompleteObjectInput{ObjectID: "x"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty collection: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
	if _, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty object id: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestCompleteObjectUnknownObjectIsNotFound(t *testing.T) {
	h, _, ctx, _ := completeHandler(&completeRepo{findErr: ErrObjectNotFound}, headStorage{})

	_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: uuid.NewString()})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// ─── CopyObject ─────────────────────────────────────────────────────────────

func availableSource() Object {
	return Object{
		ObjectID: uuid.Must(uuid.NewV7()), Collection: "src", Key: "original.txt",
		State: statemachine.StateAvailable, SizeBytes: 33, ContentType: "text/plain",
		Tags: map[string]string{"env": "prod"},
	}
}

func TestCopyObjectAuthorizesAgainstTheDestination(t *testing.T) {
	// The check that must not drift: a copy is a WRITE to the destination, so
	// the Resource handed to Cedar is the destination — its collection, its
	// key and its bucket — not the source the caller is reading from.
	repo := &completeRepo{obj: availableSource(), createErr: errors.New("stop here")}
	h, authz, ctx, _ := completeHandler(repo, headStorage{})

	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(),
		DestCollection: "dst", DestKey: "copy.txt",
	})
	if err == nil {
		t.Fatal("expected the fake CreateObject failure — this test stops short of the promote")
	}
	if authz.lastResource == nil {
		t.Fatal("authorizer was never called")
	}
	if authz.lastResource.Collection != "dst" || authz.lastResource.Key != "copy.txt" {
		t.Errorf("authz Resource = %s/%s, want the DESTINATION",
			authz.lastResource.Collection, authz.lastResource.Key)
	}
	if authz.lastResource.BucketName != "bucket-7" {
		t.Error("authz Resource missing the destination bucket binding")
	}
	if authz.lastAction != cedar.ActionCopyObject {
		t.Errorf("action = %q, want %q", authz.lastAction, cedar.ActionCopyObject)
	}
}

func TestCopyObjectDefaultsTheDestinationKeyToTheSourceKey(t *testing.T) {
	repo := &completeRepo{obj: availableSource(), createErr: errors.New("stop here")}
	h, _, ctx, _ := completeHandler(repo, headStorage{})

	_, _ = h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	})
	if repo.created == nil {
		t.Fatal("CreateObject was never reached")
	}
	if repo.created.Key != "original.txt" {
		t.Errorf("dest key = %q, want the source key when none is given", repo.created.Key)
	}
	// Metadata and tags fall back to the source's rather than being dropped.
	if repo.created.Tags["env"] != "prod" {
		t.Errorf("tags = %v, want the source's carried over", repo.created.Tags)
	}
}

func TestCopyObjectRefusesANonAvailableSource(t *testing.T) {
	for _, state := range []statemachine.State{
		statemachine.StatePending, statemachine.StateFailed, statemachine.StateDeleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			src := availableSource()
			src.State = state
			h, _, ctx, _ := completeHandler(&completeRepo{obj: src}, headStorage{})

			_, err := h.CopyObject(ctx, CopyObjectInput{
				SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
			})
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition copying from %s", connect.CodeOf(err), state)
			}
		})
	}
}

func TestCopyObjectRequiresBothEnds(t *testing.T) {
	h, _, ctx, _ := completeHandler(&completeRepo{obj: availableSource()}, headStorage{})

	for _, tc := range []struct {
		name string
		in   CopyObjectInput
	}{
		{"no source collection", CopyObjectInput{SourceObjectID: "x", DestCollection: "dst"}},
		{"no source object", CopyObjectInput{SourceCollection: "src", DestCollection: "dst"}},
		{"no destination", CopyObjectInput{SourceCollection: "src", SourceObjectID: "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.CopyObject(ctx, tc.in); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
		})
	}
}

func TestCopyObjectUnknownSourceIsNotFound(t *testing.T) {
	h, _, ctx, _ := completeHandler(&completeRepo{findErr: ErrObjectNotFound}, headStorage{})

	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func (headStorage) PublicURL(context.Context, string, string, string, uuid.UUID, string, string) (string, error) {
	return "", nil
}

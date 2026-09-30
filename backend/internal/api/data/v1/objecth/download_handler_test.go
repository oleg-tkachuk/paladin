package objecth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// DownloadObject had no behavioural test of any kind. The RPC-surface gate
// (tests/integration/rpc_surface_test.go) pins that it refuses an
// unauthenticated or malformed call, which is the whole surface and none of
// the semantics: what it returns when it succeeds, which states it refuses,
// and whether the authorization it performs can see the bucket it is about to
// hand out a URL for.
//
// That last one is the reason these tests exist rather than a happy-path
// smoke. DownloadObject resolves the (backend, bucket) binding BEFORE the
// Cedar check specifically so a bucket:- or collection:-scoped token enforces
// on download; the handler comment says so. Nothing held it to that, and the
// sibling write path (UploadObject) has a dedicated test for exactly this
// property because the binding was once missing there and every scoped
// principal was fail-closed on its own bucket.

// downloadRepo serves one object whose state the test chooses.
type downloadRepo struct {
	fakeObjectRepo
	state  statemachine.State
	bucket string
	// findErr, when set, stands in for "no such object under this key".
	findErr error
}

func (r *downloadRepo) FindByName(_ context.Context, tenantID uuid.UUID, collection, _ string) (Object, error) {
	if r.findErr != nil {
		return Object{}, r.findErr
	}
	return Object{
		ObjectID:    uuid.Must(uuid.NewV7()),
		TenantID:    tenantID,
		Collection:  collection,
		Key:         "report.pdf",
		State:       r.state,
		SizeBytes:   4096,
		ContentType: "application/pdf",
	}, nil
}

func (r *downloadRepo) LookupBucket(_ context.Context, _ uuid.UUID, _ string, _ bool) (string, string, error) {
	return "backend-7", r.bucket, nil
}

// presignGetStorage records the args it was presigned with and returns a URL
// distinguishable from every other field, so a handler that returned the
// wrong string cannot pass by coincidence.
type presignGetStorage struct {
	noopStorage
	got PresignGetArgs
}

func (s *presignGetStorage) PresignGet(_ context.Context, a PresignGetArgs) (string, map[string]string, time.Time, error) {
	s.got = a
	return "https://s3/get/report.pdf", map[string]string{"X-Amz-Signature": "sig"}, time.Unix(1500, 0), nil
}

func downloadHandler(t *testing.T, state statemachine.State) (*Handler, *presignGetStorage, *recordingAuthorizer, context.Context) {
	t.Helper()
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	storage := &presignGetStorage{}
	authz := &recordingAuthorizer{}
	h := &Handler{
		repo:    &downloadRepo{state: state, bucket: "bucket-7"},
		storage: storage,
		policy:  authz,
		presign: PresignConfig{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour},
	}
	return h, storage, authz, ctx
}

func TestDownloadObjectReturnsThePresignedURL(t *testing.T) {
	h, storage, _, ctx := downloadHandler(t, statemachine.StateAvailable)

	out, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "attachment")
	if err != nil {
		t.Fatalf("DownloadObject: %v", err)
	}
	if out.URL != "https://s3/get/report.pdf" {
		t.Errorf("URL = %q, want the presigner's", out.URL)
	}
	if out.Headers["X-Amz-Signature"] != "sig" {
		t.Errorf("Headers = %v, want the presigner's signing headers carried through", out.Headers)
	}
	if !out.ExpiresAt.Equal(time.Unix(1500, 0)) {
		t.Errorf("ExpiresAt = %v, want the presigner's expiry", out.ExpiresAt)
	}
	if out.ContentDisposition != "attachment" {
		t.Errorf("ContentDisposition = %q, want the caller's", out.ContentDisposition)
	}
	if out.Object.Key != "report.pdf" {
		t.Errorf("Object.Key = %q — the metadata half of the response is missing", out.Object.Key)
	}
	// ttl=0 means "use the configured default", not "a URL that expires now".
	if storage.got.TTL != time.Hour {
		t.Errorf("presigned TTL = %v, want the configured default (1h) when the caller passes 0", storage.got.TTL)
	}
	if storage.got.Bucket != "bucket-7" || storage.got.BackendID != "backend-7" {
		t.Errorf("presign routed to backend=%q bucket=%q, want the resolved binding", storage.got.BackendID, storage.got.Bucket)
	}
}

func TestDownloadObjectAuthzResourceCarriesBucket(t *testing.T) {
	h, _, authz, ctx := downloadHandler(t, statemachine.StateAvailable)

	if _, err := h.DownloadObject(ctx, "docs", "report.pdf", time.Minute, ""); err != nil {
		t.Fatalf("DownloadObject: %v", err)
	}
	if authz.lastResource == nil {
		t.Fatal("authorizer was never called — the presigned URL was issued without a Cedar decision")
	}
	if authz.lastResource.BackendID != "backend-7" || authz.lastResource.BucketName != "bucket-7" {
		t.Errorf("authz Resource missing physical binding: backend=%q bucket=%q — a bucket:/collection:-scoped PAT would be fail-closed on its own download",
			authz.lastResource.BackendID, authz.lastResource.BucketName)
	}
	if authz.lastAction != cedar.ActionPresignGet {
		t.Errorf("authz action = %q, want %q", authz.lastAction, cedar.ActionPresignGet)
	}
}

func TestDownloadObjectRefusesUnavailableStates(t *testing.T) {
	// A presigned GET for a PENDING object would 404 at the backend; for a
	// DELETED one it would hand out bytes the tenant believes are gone. Both
	// are refusals the caller can act on, not Internal.
	for _, state := range []statemachine.State{
		statemachine.StatePending,
		statemachine.StateFailed,
		statemachine.StateDeleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			h, storage, _, ctx := downloadHandler(t, state)

			_, err := h.DownloadObject(ctx, "docs", "report.pdf", 0, "")
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition for a %s object", connect.CodeOf(err), state)
			}
			if storage.got.Key != "" {
				t.Error("the presigner was called anyway — the state guard must run before the URL is minted")
			}
		})
	}
}

func TestDownloadObjectRequiresCollectionAndObjectID(t *testing.T) {
	h, _, _, ctx := downloadHandler(t, statemachine.StateAvailable)

	if _, err := h.DownloadObject(ctx, "", "report.pdf", 0, ""); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty collection: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
	if _, err := h.DownloadObject(ctx, "docs", "", 0, ""); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty object id: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestDownloadObjectUnknownObjectIsNotFound(t *testing.T) {
	h, _, _, ctx := downloadHandler(t, statemachine.StateAvailable)
	h.repo = &downloadRepo{state: statemachine.StateAvailable, findErr: errors.New("no rows")}

	_, err := h.DownloadObject(ctx, "docs", "missing.pdf", 0, "")
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

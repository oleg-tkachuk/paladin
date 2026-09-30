package object

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// CountObjects, ListDistinctTags and LookupObject were three of the thirteen
// handlers at 0.0% across unit and both integration suites (BACKLOG: "Half the
// admin API's RPCs have no behavioural test").
//
// They share a property worth pinning together rather than one file at a time:
// each resolves the collection→bucket binding BEFORE the Cedar check so a
// bucket:- or collection:-scoped token enforces on them. The binding lookup is
// deliberately best-effort on the two aggregates — a collection with no
// binding still counts — which makes it exactly the kind of code that can lose
// the binding in a refactor without any test noticing.

type aggregateRepo struct {
	fakeObjectRepo

	count      int64
	exact      bool
	countArgs  CountObjectsArgs
	countErr   error
	tagPage    DistinctTagPage
	tagErr     error
	found      Object
	findErr    error
	bucketErr  error
	lookupCall int
}

func (r *aggregateRepo) CountObjects(_ context.Context, a CountObjectsArgs) (int64, bool, error) {
	r.countArgs = a
	return r.count, r.exact, r.countErr
}

func (r *aggregateRepo) ListDistinctTags(_ context.Context, _ uuid.UUID, _ string, _ string, _ int32, _ int32) (DistinctTagPage, error) {
	return r.tagPage, r.tagErr
}

func (r *aggregateRepo) FindByPath(_ context.Context, _ uuid.UUID, _ string, _ string) (Object, error) {
	if r.findErr != nil {
		return Object{}, r.findErr
	}
	return r.found, nil
}

func (r *aggregateRepo) LookupBucket(_ context.Context, _ uuid.UUID, _ string, _ bool) (string, string, error) {
	r.lookupCall++
	if r.bucketErr != nil {
		return "", "", r.bucketErr
	}
	return "backend-7", "bucket-7", nil
}

func aggregateHandler(repo *aggregateRepo) (*Handler, *recordingAuthorizer, context.Context, uuid.UUID) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	authz := &recordingAuthorizer{}
	return &Handler{repo: repo, policy: authz, filter: cel.NewEvaluator()}, authz, ctx, tenantID
}

func TestCountObjectsReturnsTheRepoCount(t *testing.T) {
	repo := &aggregateRepo{count: 42, exact: true}
	h, _, ctx, _ := aggregateHandler(repo)

	out, err := h.CountObjects(ctx, CountObjectsInput{Collection: "docs"})
	if err != nil {
		t.Fatalf("CountObjects: %v", err)
	}
	if out.ApproximateCount != 42 || !out.Exact {
		t.Errorf("got count=%d exact=%v, want 42/true", out.ApproximateCount, out.Exact)
	}
	// No filter means no compiled program, which is what lets the adapter take
	// the cheap COUNT(*) path instead of walking the table. An always-true
	// program here would be indistinguishable in the result and quietly
	// expensive, so this is the assertion that protects it.
	if repo.countArgs.CompiledCEL != nil {
		t.Error("an unfiltered count carried a compiled CEL program — the adapter cannot take the COUNT(*) path")
	}
}

func TestCountObjectsPassesTheFilterThrough(t *testing.T) {
	repo := &aggregateRepo{count: 3}
	h, _, ctx, _ := aggregateHandler(repo)

	if _, err := h.CountObjects(ctx, CountObjectsInput{
		Collection: "docs", Filter: `content_type == "application/pdf"`,
	}); err != nil {
		t.Fatalf("CountObjects: %v", err)
	}
	if repo.countArgs.CompiledCEL == nil {
		t.Error("a filtered count reached the repo without its program — every row would match")
	}
}

func TestCountObjectsRejectsAnUncompilableFilter(t *testing.T) {
	h, _, ctx, _ := aggregateHandler(&aggregateRepo{})

	_, err := h.CountObjects(ctx, CountObjectsInput{Collection: "docs", Filter: "this is not CEL"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestCountObjectsAuthzResourceCarriesBucket(t *testing.T) {
	repo := &aggregateRepo{count: 1}
	h, authz, ctx, tenantID := aggregateHandler(repo)

	if _, err := h.CountObjects(ctx, CountObjectsInput{Collection: "docs"}); err != nil {
		t.Fatalf("CountObjects: %v", err)
	}
	if authz.lastResource == nil {
		t.Fatal("authorizer was never called")
	}
	if authz.lastResource.BackendID != "backend-7" || authz.lastResource.BucketName != "bucket-7" {
		t.Errorf("authz Resource missing binding: backend=%q bucket=%q — a scoped token would be fail-closed on count",
			authz.lastResource.BackendID, authz.lastResource.BucketName)
	}
	if authz.lastResource.TenantID != tenantID {
		t.Errorf("authz Resource tenant = %v, want the caller's %v", authz.lastResource.TenantID, tenantID)
	}
}

func TestCountObjectsSurvivesAnUnboundCollection(t *testing.T) {
	// The binding lookup is best-effort on purpose: an aggregate over a
	// collection with no bucket bound is still a legitimate count of zero
	// rows, not an error. The handler drops the lookup error on the floor —
	// deliberately — and this pins that it keeps doing so.
	repo := &aggregateRepo{count: 0, exact: true, bucketErr: errors.New("no binding")}
	h, authz, ctx, _ := aggregateHandler(repo)

	if _, err := h.CountObjects(ctx, CountObjectsInput{Collection: "unbound"}); err != nil {
		t.Fatalf("CountObjects refused an unbound collection: %v", err)
	}
	if authz.lastResource.BucketName != "" {
		t.Errorf("bucket = %q, want empty when nothing is bound", authz.lastResource.BucketName)
	}
}

func TestListDistinctTagsReturnsTheFacets(t *testing.T) {
	repo := &aggregateRepo{tagPage: DistinctTagPage{
		Keys:      []string{"env", "team"},
		Values:    map[string][]string{"env": {"prod", "staging"}, "team": {"platform"}},
		Truncated: map[string]bool{"env": true},
		NextKey:   "team",
	}}
	h, authz, ctx, _ := aggregateHandler(repo)

	page, err := h.ListDistinctTags(ctx, "docs", "", 50)
	if err != nil {
		t.Fatalf("ListDistinctTags: %v", err)
	}
	if len(page.Keys) != 2 || page.Keys[0] != "env" {
		t.Errorf("Keys = %v, want the repo's ascending pair", page.Keys)
	}
	// Truncated is the difference between "these are the values" and "these
	// are some of them" — a facet UI that loses it silently lies.
	if !page.Truncated["env"] {
		t.Error("Truncated was dropped — the caller cannot tell a sample from the set")
	}
	if page.NextKey != "team" {
		t.Errorf("NextKey = %q, want the cursor to advance", page.NextKey)
	}
	if authz.lastResource == nil || authz.lastResource.BucketName != "bucket-7" {
		t.Error("authz Resource missing the bucket binding on the tag facet")
	}
}

func TestListDistinctTagsMapsRepoFailureToInternal(t *testing.T) {
	h, _, ctx, _ := aggregateHandler(&aggregateRepo{tagErr: errors.New("boom")})

	_, err := h.ListDistinctTags(ctx, "docs", "", 50)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
}

func TestLookupObjectFindsByKey(t *testing.T) {
	repo := &aggregateRepo{found: Object{
		ObjectID: uuid.Must(uuid.NewV7()), Collection: "docs", Key: "a/b/c.txt",
		State: statemachine.StateAvailable, SizeBytes: 11, ContentType: "text/plain",
	}}
	h, authz, ctx, _ := aggregateHandler(repo)

	obj, err := h.LookupObject(ctx, "docs", "a/b/c.txt")
	if err != nil {
		t.Fatalf("LookupObject: %v", err)
	}
	if obj.Key != "a/b/c.txt" {
		t.Errorf("Key = %q, want the looked-up key", obj.Key)
	}
	// LookupObject addresses by KEY where GetObject addresses by id, so the
	// authz resource has to be built from what the repo returned rather than
	// from the request — including the size and content type the policy may
	// condition on.
	if authz.lastResource.Key != "a/b/c.txt" || authz.lastResource.SizeBytes != 11 {
		t.Errorf("authz Resource = key %q size %d, want the found object's",
			authz.lastResource.Key, authz.lastResource.SizeBytes)
	}
	if authz.lastResource.BucketName != "bucket-7" {
		t.Error("authz Resource missing the bucket binding on lookup")
	}
	if authz.lastAction != cedar.ActionGetObject {
		t.Errorf("action = %q, want %q", authz.lastAction, cedar.ActionGetObject)
	}
}

func TestLookupObjectRequiresCollectionAndKey(t *testing.T) {
	h, _, ctx, _ := aggregateHandler(&aggregateRepo{})

	if _, err := h.LookupObject(ctx, "", "k"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty collection: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
	if _, err := h.LookupObject(ctx, "docs", ""); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty key: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestLookupObjectUnknownKeyIsNotFound(t *testing.T) {
	h, _, ctx, _ := aggregateHandler(&aggregateRepo{findErr: errors.New("no rows")})

	_, err := h.LookupObject(ctx, "docs", "missing")
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

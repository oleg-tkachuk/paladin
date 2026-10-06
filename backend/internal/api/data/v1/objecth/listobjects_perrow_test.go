package objecth

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// listRepo returns a fixed page + next token, so the test drives the handler's
// post-fetch behaviour (CEL is a no-op with an empty filter).
type listRepo struct {
	fakeObjectRepo
	page []Object
	next string
}

func (r *listRepo) ListObjects(context.Context, ListObjectsArgs) ([]Object, string, error) {
	// Return a fresh slice per call — the production adapter does the same
	// (out := make(...)), and the handler filters it in place.
	return append([]Object(nil), r.page...), r.next, nil
}

// ListObjects now resolves the collection→bucket binding before authz so
// bucket:/collection: scopes enforce; supply a stable binding here.
func (r *listRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	return "backend-list", "bucket-list", nil
}

// perRowAuthorizer allows the up-front collection-scoped check (Resource.Key
// empty) and, per object, denies any object carrying tags[denyTag]=="true". It
// implements the optional cedar.PerObjectEvaluator so the handler's per-row path
// is exercised (or skipped) under test control.
type perRowAuthorizer struct {
	perObject bool
	denyTag   string
}

func (a perRowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	if r.Key == "" { // the up-front collection-scoped check
		return cedar.DecisionAllow, nil
	}
	if a.denyTag != "" && r.Tags[a.denyTag] == "true" {
		return cedar.DecisionDeny, nil
	}
	return cedar.DecisionAllow, nil
}

func (a perRowAuthorizer) NeedsPerObjectEval(context.Context, uuid.UUID, string) (bool, error) {
	return a.perObject, nil
}

func TestListObjectsPerRowCedar(t *testing.T) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

	page := []Object{
		{ObjectID: uuid.New(), Key: "a", Tags: map[string]string{}},
		{ObjectID: uuid.New(), Key: "b", Tags: map[string]string{"classified": "true"}},
		{ObjectID: uuid.New(), Key: "c", Tags: map[string]string{"classified": "false"}},
	}
	const cursor = "next-page-cursor"

	newHandler := func(authz cedar.Authorizer) *Handler {
		return &Handler{
			repo:   &listRepo{page: page, next: cursor},
			policy: authz,
			filter: cel.NewEvaluator(),
		}
	}
	in := ListObjectsInput{Collection: "docs", PageSize: 10}

	t.Run("per-row mode drops policy-declined objects, cursor stable", func(t *testing.T) {
		h := newHandler(perRowAuthorizer{perObject: true, denyTag: "classified"})
		objs, next, err := h.ListObjects(ctx, in)
		if err != nil {
			t.Fatalf("ListObjects: %v", err)
		}
		if next != cursor {
			t.Errorf("next token = %q, want %q (cursor must key on fetched rows, not survivors)", next, cursor)
		}
		gotKeys := keysOf(objs)
		want := []string{"a", "c"} // "b" is classified → declined per-row
		if !equalStrings(gotKeys, want) {
			t.Errorf("kept objects = %v, want %v", gotKeys, want)
		}
	})

	t.Run("constant-policy fast path returns the whole page unfiltered", func(t *testing.T) {
		h := newHandler(perRowAuthorizer{perObject: false, denyTag: "classified"})
		objs, next, err := h.ListObjects(ctx, in)
		if err != nil {
			t.Fatalf("ListObjects: %v", err)
		}
		if next != cursor {
			t.Errorf("next token = %q, want %q", next, cursor)
		}
		if got := keysOf(objs); !equalStrings(got, []string{"a", "b", "c"}) {
			t.Errorf("fast path filtered objects (%v) — per-row must be skipped when NeedsPerObjectEval is false", got)
		}
	})
}

func keysOf(objs []Object) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.Key
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

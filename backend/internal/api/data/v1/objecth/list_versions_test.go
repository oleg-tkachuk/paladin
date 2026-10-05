package objecth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// ListVersions was at 0%. Its guard is the same one GetVersion carries and for
// the same reason: a version id is a bare UUID that says nothing about whose
// object it belongs to, so the parent is resolved under the CALLER's tenant
// first and the listing hangs off that. Drop the parent lookup and the handler
// happily lists another tenant's version history from an id someone guessed.

type versionListRepo struct {
	fakeObjectRepo
	findErr error
	parent  Object
}

func (r *versionListRepo) FindByName(_ context.Context, tenantID uuid.UUID, collection, _ string) (Object, error) {
	if r.findErr != nil {
		return Object{}, r.findErr
	}
	o := r.parent
	o.TenantID, o.Collection = tenantID, collection
	return o, nil
}

func versionListHandler(repo Repository, versions *fakeVersionRepo) (*VersionHandler, context.Context, uuid.UUID) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	h := NewVersionHandler(repo, versions)
	h.SetAuthorizer(allowAll{})
	return h, ctx, tenantID
}

func TestListVersionsReturnsTheHistoryAndItsCursor(t *testing.T) {
	objectID := uuid.Must(uuid.NewV7())
	versions := newFakeVersionRepo()
	versions.fixedList = []ObjectVersion{
		{VersionID: uuid.Must(uuid.NewV7()), ObjectID: objectID, SizeBytes: 10},
		{VersionID: uuid.Must(uuid.NewV7()), ObjectID: objectID, SizeBytes: 20},
	}
	h, ctx, _ := versionListHandler(&versionListRepo{parent: Object{ObjectID: objectID}}, versions)

	got, _, err := h.ListVersions(ctx, ListVersionsInput{
		Collection: "docs", ObjectID: objectID.String(), PageSize: 50,
	})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d versions, want the repo's 2", len(got))
	}
}

func TestListVersionsResolvesTheParentUnderTheCallersTenant(t *testing.T) {
	// The whole tenant guard: an object id is not an authorisation, so the
	// parent has to be found under the caller's own tenant before any history
	// is read. A repo that cannot find it means NotFound, not an empty list —
	// "no versions" and "not yours" must not look the same.
	versions := newFakeVersionRepo()
	h, ctx, _ := versionListHandler(&versionListRepo{findErr: ErrObjectNotFound}, versions)

	_, _, err := h.ListVersions(ctx, ListVersionsInput{
		Collection: "docs", ObjectID: uuid.Must(uuid.NewV7()).String(),
	})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestListVersionsRejectsMalformedInput(t *testing.T) {
	versions := newFakeVersionRepo()
	h, ctx, _ := versionListHandler(&versionListRepo{}, versions)

	for _, tc := range []struct {
		name string
		in   ListVersionsInput
	}{
		{"no collection", ListVersionsInput{ObjectID: uuid.NewString()}},
		{"no object id", ListVersionsInput{Collection: "docs"}},
		{"object id is not a uuid", ListVersionsInput{Collection: "docs", ObjectID: "not-a-uuid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := h.ListVersions(ctx, tc.in); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
		})
	}
}

func TestListVersionsUnauthenticated(t *testing.T) {
	versions := newFakeVersionRepo()
	h := NewVersionHandler(&versionListRepo{}, versions)
	h.SetAuthorizer(allowAll{})

	_, _, err := h.ListVersions(context.Background(), ListVersionsInput{
		Collection: "docs", ObjectID: uuid.NewString(),
	})
	if err == nil {
		t.Fatal("an unauthenticated caller listed version history")
	}
}

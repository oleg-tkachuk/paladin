package objecth

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// VersionHandler.GetVersion was one of the thirteen handlers at 0.0% across
// unit and both integration suites (BACKLOG: "Half the admin API's RPCs have
// no behavioural test").
//
// It carries a guard that is easy to lose and expensive to lose: a version id
// is a bare UUID, so nothing in it says which object — or which TENANT — it
// belongs to. The handler resolves the parent object under the caller's tenant
// first and then refuses any version whose ObjectID does not match. Drop that
// comparison and a caller can read any version in the deployment by guessing a
// UUID, while every other assertion here still passes.

// parentRepo resolves the named parent object under the caller's tenant.
type parentRepo struct {
	fakeObjectRepo
	parent  Object
	findErr error
}

func (r *parentRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	if r.findErr != nil {
		return Object{}, r.findErr
	}
	return r.parent, nil
}

func versionName(tenantID, objectID, versionID uuid.UUID) string {
	return "tenants/" + tenantID.String() + "/collections/docs/objects/" +
		objectID.String() + "/versions/" + versionID.String()
}

func versionHandler(parent Object, versions *fakeVersionRepo) (*VersionHandler, context.Context, uuid.UUID) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	h := NewVersionHandler(&parentRepo{parent: parent}, versions)
	h.SetAuthorizer(allowAll{})
	return h, ctx, tenantID
}

func TestGetVersionReturnsTheVersionAndMarksTheCurrentOne(t *testing.T) {
	objectID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	versions := newFakeVersionRepo()
	versions.fixedGet = map[uuid.UUID]ObjectVersion{
		versionID: {VersionID: versionID, ObjectID: objectID, SizeBytes: 12},
	}
	versions.current[objectID] = versionID

	h, ctx, tenantID := versionHandler(Object{ObjectID: objectID, Collection: "docs"}, versions)

	got, err := h.GetVersion(ctx, versionName(tenantID, objectID, versionID))
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got.VersionID != versionID || got.SizeBytes != 12 {
		t.Errorf("got %+v, want the stored version", got)
	}
	// IsCurrent is computed, not stored — the repo returned it false.
	if !got.IsCurrent {
		t.Error("IsCurrent = false for the version the current pointer names")
	}
}

func TestGetVersionLeavesAnOlderVersionUnmarked(t *testing.T) {
	objectID := uuid.Must(uuid.NewV7())
	older := uuid.Must(uuid.NewV7())
	newer := uuid.Must(uuid.NewV7())
	versions := newFakeVersionRepo()
	versions.fixedGet = map[uuid.UUID]ObjectVersion{
		older: {VersionID: older, ObjectID: objectID},
	}
	versions.current[objectID] = newer

	h, ctx, tenantID := versionHandler(Object{ObjectID: objectID, Collection: "docs"}, versions)

	got, err := h.GetVersion(ctx, versionName(tenantID, objectID, older))
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got.IsCurrent {
		t.Error("IsCurrent = true for a superseded version")
	}
}

func TestGetVersionRefusesAVersionBelongingToAnotherObject(t *testing.T) {
	// The guard that matters. The version exists and the parent exists; they
	// are simply unrelated, and a UUID carries no evidence either way.
	parentID := uuid.Must(uuid.NewV7())
	strangerID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	versions := newFakeVersionRepo()
	versions.fixedGet = map[uuid.UUID]ObjectVersion{
		versionID: {VersionID: versionID, ObjectID: strangerID},
	}

	h, ctx, tenantID := versionHandler(Object{ObjectID: parentID, Collection: "docs"}, versions)

	_, err := h.GetVersion(ctx, versionName(tenantID, parentID, versionID))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound — a version id alone must not address a version", connect.CodeOf(err))
	}
}

func TestGetVersionRejectsMalformedNames(t *testing.T) {
	versions := newFakeVersionRepo()
	h, ctx, tenantID := versionHandler(Object{ObjectID: uuid.Must(uuid.NewV7())}, versions)

	for _, name := range []string{
		"",
		"collections/docs/objects/x/versions/y",
		"tenants/" + tenantID.String() + "/collections/docs/objects/" + uuid.NewString(),
		"tenants/" + tenantID.String() + "/collections/docs/objects/" + uuid.NewString() + "/versions/not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.GetVersion(ctx, name); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
		})
	}
}

func TestGetVersionUnknownParentIsNotFound(t *testing.T) {
	versions := newFakeVersionRepo()
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	h := NewVersionHandler(&parentRepo{findErr: ErrObjectNotFound}, versions)
	h.SetAuthorizer(allowAll{})

	_, err := h.GetVersion(ctx, versionName(tenantID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestGetVersionUnknownVersionIsNotFound(t *testing.T) {
	objectID := uuid.Must(uuid.NewV7())
	versions := newFakeVersionRepo() // fixedGet empty → ErrVersionNotFound
	h, ctx, tenantID := versionHandler(Object{ObjectID: objectID, Collection: "docs"}, versions)

	_, err := h.GetVersion(ctx, versionName(tenantID, objectID, uuid.Must(uuid.NewV7())))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

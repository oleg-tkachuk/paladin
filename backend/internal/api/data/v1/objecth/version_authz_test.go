package objecth

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

// The versioning RPCs ran with no Cedar check and no capability check: inside
// its tenant, any authenticated caller — a read-only capability included —
// could list and read versions and repoint an object's current version. These
// pin the double gate the other object RPCs apply.

// parentResourceVersion is the OCC guard the restore fixture carries.
const parentResourceVersion = 7

type versionFixture struct {
	h          *VersionHandler
	versions   *fakeVersionRepo
	ctx        context.Context
	tenantID   uuid.UUID
	objectID   uuid.UUID
	currentID  uuid.UUID
	restoredID uuid.UUID
}

// newVersionFixture is an object with two versions, the first current.
func newVersionFixture(t *testing.T, pe cedar.Authorizer) versionFixture {
	t.Helper()
	f := versionFixture{
		objectID:   uuid.Must(uuid.NewV7()),
		currentID:  uuid.Must(uuid.NewV7()),
		restoredID: uuid.Must(uuid.NewV7()),
	}
	f.versions = newFakeVersionRepo()
	f.versions.fixedGet = map[uuid.UUID]ObjectVersion{
		f.currentID:  {VersionID: f.currentID, ObjectID: f.objectID},
		f.restoredID: {VersionID: f.restoredID, ObjectID: f.objectID},
	}
	f.versions.fixedList = []ObjectVersion{f.versions.fixedGet[f.currentID], f.versions.fixedGet[f.restoredID]}
	f.versions.current[f.objectID] = f.currentID
	f.h, f.ctx, f.tenantID = versionHandler(Object{
		ObjectID: f.objectID, Collection: "docs", Key: "a.txt", ResourceVersion: parentResourceVersion,
	}, f.versions)
	f.h.SetAuthorizer(pe)
	return f
}

func (f versionFixture) list() error {
	_, _, err := f.h.ListVersions(f.ctx, ListVersionsInput{Collection: "docs", ObjectID: f.objectID.String()})
	return err
}

func (f versionFixture) get() error {
	_, err := f.h.GetVersion(f.ctx, versionName(f.tenantID, f.objectID, f.restoredID))
	return err
}

func (f versionFixture) restore() error {
	_, err := f.h.RestoreVersion(f.ctx, versionName(f.tenantID, f.objectID, f.restoredID),
		strconv.Itoa(parentResourceVersion))
	return err
}

func wantPermissionDenied(t *testing.T, rpc string, err error) {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodePermissionDenied {
		t.Errorf("%s: err = %v, want PermissionDenied", rpc, err)
	}
}

func TestVersionRPCsAreRefusedWhenPolicyDenies(t *testing.T) {
	f := newVersionFixture(t, denyAll{})
	wantPermissionDenied(t, "ListVersions", f.list())
	wantPermissionDenied(t, "GetVersion", f.get())
	wantPermissionDenied(t, "RestoreVersion", f.restore())
	if got := f.versions.current[f.objectID]; got != f.currentID {
		t.Errorf("a denied restore moved the current version to %s", got)
	}
}

func TestVersionRPCsAreRefusedWithoutAnAuthorizer(t *testing.T) {
	f := newVersionFixture(t, nil)
	wantPermissionDenied(t, "ListVersions", f.list())
	wantPermissionDenied(t, "GetVersion", f.get())
	wantPermissionDenied(t, "RestoreVersion", f.restore())
}

func TestVersionRPCsAskCedarForTheObjectActions(t *testing.T) {
	cases := []struct {
		name   string
		call   func(versionFixture) error
		action cedar.Action
	}{
		{"ListVersions", versionFixture.list, cedar.ActionGetObject},
		{"GetVersion", versionFixture.get, cedar.ActionGetObject},
		{"RestoreVersion", versionFixture.restore, cedar.ActionPutObject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &actionRecorder{}
			f := newVersionFixture(t, rec)
			if err := tc.call(f); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if rec.last != tc.action {
				t.Errorf("Cedar asked about %q, want %q", rec.last, tc.action)
			}
		})
	}
}

// A capability that may only read cannot restore, even when Cedar allows the
// principal everything: the capability narrows, it never widens.
func TestReadOnlyCapabilityCannotRestoreAVersion(t *testing.T) {
	f := newVersionFixture(t, allowAll{})
	f.ctx = auth.WithCapability(f.ctx, &capability.Capability{
		ID:      uuid.New(),
		Subject: capability.Principal{TenantID: f.tenantID, Type: capability.PrincipalAgent},
		Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet, capability.OpList}},
	})
	if err := f.get(); err != nil {
		t.Fatalf("GetVersion with a read capability: %v", err)
	}
	wantPermissionDenied(t, "RestoreVersion", f.restore())
	if got := f.versions.current[f.objectID]; got != f.currentID {
		t.Errorf("a read-only capability moved the current version to %s", got)
	}
}

package objecth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// errVersionStore is the version store failing under a read.
var errVersionStore = errors.New("version store unreachable")

// failingVersionRepo is fakeVersionRepo with its pointer and version reads
// able to fail.
type failingVersionRepo struct {
	*fakeVersionRepo
	currentErr error
	getErr     error
}

func (f *failingVersionRepo) CurrentVersionID(ctx context.Context, objectID uuid.UUID) (uuid.UUID, error) {
	if f.currentErr != nil {
		return uuid.Nil(), f.currentErr
	}
	return f.fakeVersionRepo.CurrentVersionID(ctx, objectID)
}

func (f *failingVersionRepo) Get(ctx context.Context, id uuid.UUID) (ObjectVersion, error) {
	if f.getErr != nil {
		return ObjectVersion{}, f.getErr
	}
	return f.fakeVersionRepo.Get(ctx, id)
}

// authedCtxFor is a context whose caller acts for tenantID.
func authedCtxFor(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
}

// GetVersion marked a version not current when the pointer could not be
// read, answering a store failure with a wrong is_current. It fails instead.
func TestGetVersionFailsWhenTheCurrentPointerIsUnreadable(t *testing.T) {
	objectID, versionID := uuid.NewV7(), uuid.NewV7()
	inner := newFakeVersionRepo()
	inner.fixedGet = map[uuid.UUID]ObjectVersion{versionID: {VersionID: versionID, ObjectID: objectID}}
	inner.current[objectID] = versionID
	versions := &failingVersionRepo{fakeVersionRepo: inner, currentErr: errVersionStore}

	tenantID := uuid.New()
	h, ctx := NewVersionHandler(&parentRepo{parent: Object{ObjectID: objectID, Collection: "docs"}}, versions), authedCtxFor(tenantID)
	h.SetAuthorizer(allowAll{})
	_, err := h.GetVersion(ctx, versionName(tenantID, objectID, versionID))
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("GetVersion = %v (code %v), want Internal", err, got)
	}
}

// A version RPC's bucket, resolved only to scope its authorization, is
// warned about when the lookup fails, as the object RPCs' is.
func TestVersionRPCWarnsWhenItsScopeBucketFails(t *testing.T) {
	objectID, versionID := uuid.NewV7(), uuid.NewV7()
	tenantID := uuid.New()
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewVersionHandler(&unresolvedRepo{}, newFakeVersionRepo())
	h.SetAuthorizer(denyAll{})
	h.SetLogger(zap.New(core))
	_, _ = h.GetVersion(authedCtxFor(tenantID), versionName(tenantID, objectID, versionID))
	warnedOnce(t, logs, opGetVersion, tenantID, "docs")
}

// Restoring a trashed object whose current version is a delete marker moves
// the pointer back to the last real version. A read that failed on the way
// used to skip that step and restore anyway, leaving a visible object whose
// current version is the marker. Each failure now fails the restore.
func TestUnsetDeleteMarkerCurrentFailsWhenItCannotRead(t *testing.T) {
	objectID := uuid.NewV7()
	marker := ObjectVersion{VersionID: uuid.NewV7(), ObjectID: objectID, IsDeleteMarker: true}
	seeded := func() *fakeVersionRepo {
		v := newFakeVersionRepo()
		v.fixedGet = map[uuid.UUID]ObjectVersion{marker.VersionID: marker}
		v.current[objectID] = marker.VersionID
		return v
	}
	for _, tc := range []struct {
		name     string
		repo     *fakeObjectRepo
		versions VersionRepository
	}{
		{"versioning config", &fakeObjectRepo{err: errBucketStore}, seeded()},
		{"current pointer", &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: true}}, &failingVersionRepo{fakeVersionRepo: seeded(), currentErr: errVersionStore}},
		{"current version", &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: true}}, &failingVersionRepo{fakeVersionRepo: seeded(), getErr: errVersionStore}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewVersionHandler(tc.repo, tc.versions)
			if err := h.UnsetDeleteMarkerCurrent(context.Background(), Object{ObjectID: objectID, Collection: "k"}); err == nil {
				t.Fatal("UnsetDeleteMarkerCurrent = nil, want the read's failure")
			}
		})
	}
}

// A pointer naming a version that is not there is no delete marker: nothing
// to move back from, so the restore goes on.
func TestUnsetDeleteMarkerCurrentIgnoresAMissingCurrentVersion(t *testing.T) {
	objectID := uuid.NewV7()
	versions := newFakeVersionRepo()
	versions.current[objectID] = uuid.NewV7()
	h := NewVersionHandler(&fakeObjectRepo{meta: BucketMeta{VersioningEnabled: true}}, versions)
	if err := h.UnsetDeleteMarkerCurrent(context.Background(), Object{ObjectID: objectID, Collection: "k"}); err != nil {
		t.Fatalf("UnsetDeleteMarkerCurrent = %v, want nil", err)
	}
}

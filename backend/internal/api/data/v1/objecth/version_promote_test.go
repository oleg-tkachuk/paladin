package objecth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeObjectRepo is a minimal Repository stand-in that only implements the
// methods OnPromote / OnSoftDelete actually call. Other methods would panic
// if invoked — keeps the test honest about what we're exercising.
type fakeObjectRepo struct {
	meta BucketMeta
	err  error
}

func (f *fakeObjectRepo) LookupBucketMeta(_ context.Context, _ uuid.UUID, _ string, _ bool) (BucketMeta, error) {
	return f.meta, f.err
}

// All other methods unimplemented — tests never reach them.
func (*fakeObjectRepo) CreateObject(context.Context, CreateObjectArgs) (Object, error) {
	panic("not used")
}
func (*fakeObjectRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	panic("not used")
}
func (*fakeObjectRepo) FindByPath(context.Context, uuid.UUID, string, string) (Object, error) {
	panic("not used")
}
func (*fakeObjectRepo) UpdateMetadata(context.Context, UpdateMetadataArgs) (Object, error) {
	panic("not used")
}
func (*fakeObjectRepo) ListObjects(context.Context, ListObjectsArgs) ([]Object, string, error) {
	panic("not used")
}
func (*fakeObjectRepo) CountObjects(context.Context, CountObjectsArgs) (int64, bool, error) {
	panic("not used")
}
func (*fakeObjectRepo) ListDistinctTags(context.Context, uuid.UUID, string, string, int32, int32) (DistinctTagPage, error) {
	panic("not used")
}
func (*fakeObjectRepo) FindByIDs(context.Context, uuid.UUID, []uuid.UUID) ([]Object, error) {
	panic("not used")
}
func (*fakeObjectRepo) ObjectLock(context.Context, uuid.UUID, uuid.UUID) (ObjectLock, error) {
	// GetObject / LookupObject read this to populate Object.Lock, so it is no
	// longer an unreached method. Unlocked is the state every test that does
	// not say otherwise intends.
	return ObjectLock{}, nil
}
func (*fakeObjectRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	panic("not used")
}
func (*fakeObjectRepo) UpdateMetadataTx(context.Context, pgx.Tx, UpdateMetadataArgs) (Object, error) {
	panic("not used")
}
func (*fakeObjectRepo) HardDeleteTx(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int64) error {
	panic("not used")
}
func (*fakeObjectRepo) HardDeleteWithBypassTx(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int64) error {
	panic("not used")
}
func (*fakeObjectRepo) RunInTx(context.Context, func(context.Context, pgx.Tx) error) error {
	panic("not used")
}

// Purge debt is a no-op in these fakes: the permanent-delete path is
// covered end-to-end in tests/integration/components, where a real pending_purges
// row is the assertion.
func (*fakeObjectRepo) EnqueuePurgeTx(context.Context, pgx.Tx, PurgeDebt) error { return nil }
func (*fakeObjectRepo) SettlePurgeTx(context.Context, pgx.Tx, uuid.UUID) error  { return nil }
func (*fakeObjectRepo) LiveCollision(context.Context, uuid.UUID, string, string) (bool, error) {
	panic("not used")
}

// fakeVersionRepo records inserts + current pointer flips for inspection.
// fixedList / fixedGet drive the read paths used by UnsetDeleteMarkerCurrent.
type fakeVersionRepo struct {
	mu        sync.Mutex
	inserted  []ObjectVersion
	current   map[uuid.UUID]uuid.UUID
	fixedList []ObjectVersion
	fixedGet  map[uuid.UUID]ObjectVersion
}

func newFakeVersionRepo() *fakeVersionRepo {
	return &fakeVersionRepo{current: map[uuid.UUID]uuid.UUID{}}
}
func (f *fakeVersionRepo) Insert(_ context.Context, v ObjectVersion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserted = append(f.inserted, v)
	return nil
}
func (f *fakeVersionRepo) Get(_ context.Context, id uuid.UUID) (ObjectVersion, error) {
	if v, ok := f.fixedGet[id]; ok {
		return v, nil
	}
	return ObjectVersion{}, ErrVersionNotFound
}
func (f *fakeVersionRepo) List(context.Context, uuid.UUID, int32, string) ([]ObjectVersion, string, error) {
	return f.fixedList, "", nil
}
func (f *fakeVersionRepo) CurrentVersionID(_ context.Context, objectID uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current[objectID], nil
}
func (f *fakeVersionRepo) SetCurrentVersionID(_ context.Context, objectID, versionID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current[objectID] = versionID
	return nil
}

func TestOnPromoteSkipsWhenVersioningOff(t *testing.T) {
	repo := &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: false}}
	vrepo := newFakeVersionRepo()
	h := NewVersionHandler(repo, vrepo)

	obj := Object{ObjectID: uuid.Must(uuid.NewV7())}
	if err := h.OnPromote(context.Background(), obj); err != nil {
		t.Fatalf("OnPromote: %v", err)
	}
	if len(vrepo.inserted) != 0 {
		t.Errorf("expected no insert when versioning off, got %d", len(vrepo.inserted))
	}
}

func TestOnPromoteRecordsWhenVersioningOn(t *testing.T) {
	repo := &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: true}}
	vrepo := newFakeVersionRepo()
	h := NewVersionHandler(repo, vrepo)

	objectID := uuid.Must(uuid.NewV7())
	obj := Object{
		ObjectID:    objectID,
		Collection:  "photos",
		Key:         "k1",
		ETag:        "abc",
		SizeBytes:   42,
		ContentType: "image/jpeg",
		CommittedAt: timePtr(time.Now()),
	}
	if err := h.OnPromote(context.Background(), obj); err != nil {
		t.Fatalf("OnPromote: %v", err)
	}
	if len(vrepo.inserted) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(vrepo.inserted))
	}
	v := vrepo.inserted[0]
	if v.ObjectID != objectID {
		t.Errorf("ObjectID: got %v want %v", v.ObjectID, objectID)
	}
	if v.IsDeleteMarker {
		t.Error("promote inserted a delete marker")
	}
	if v.ETag != "abc" || v.SizeBytes != 42 {
		t.Errorf("body: got %+v", v)
	}
	if cur := vrepo.current[objectID]; cur != v.VersionID {
		t.Errorf("current pointer: got %v want %v", cur, v.VersionID)
	}
}

func TestOnSoftDeleteRecordsDeleteMarker(t *testing.T) {
	repo := &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: true}}
	vrepo := newFakeVersionRepo()
	h := NewVersionHandler(repo, vrepo)

	objectID := uuid.Must(uuid.NewV7())
	if err := h.OnSoftDelete(context.Background(), Object{ObjectID: objectID, Collection: "k", Key: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(vrepo.inserted) != 1 || !vrepo.inserted[0].IsDeleteMarker {
		t.Errorf("expected one delete marker, got %+v", vrepo.inserted)
	}
}

func TestOnPromoteMetaErrPropagates(t *testing.T) {
	repo := &fakeObjectRepo{err: errors.New("db down")}
	vrepo := newFakeVersionRepo()
	h := NewVersionHandler(repo, vrepo)

	err := h.OnPromote(context.Background(), Object{ObjectID: uuid.Must(uuid.NewV7())})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOnPromoteNilHandlerSafe(t *testing.T) {
	var h *VersionHandler
	if err := h.OnPromote(context.Background(), Object{}); err != nil {
		t.Errorf("nil-handler OnPromote should be no-op, got %v", err)
	}
}

func TestUnsetDeleteMarkerCurrentFlipsToPrev(t *testing.T) {
	repo := &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: true}}
	vrepo := newFakeVersionRepo()
	objectID := uuid.Must(uuid.NewV7())

	// History (newest first): delete-marker (current), regular v1.
	regular := ObjectVersion{
		VersionID: uuid.Must(uuid.NewV7()),
		ObjectID:  objectID,
		ETag:      "regular",
		CreatedAt: time.Now().Add(-2 * time.Second),
	}
	marker := ObjectVersion{
		VersionID:      uuid.Must(uuid.NewV7()),
		ObjectID:       objectID,
		IsDeleteMarker: true,
		CreatedAt:      time.Now(),
	}
	vrepo.inserted = []ObjectVersion{marker, regular}
	vrepo.fixedList = []ObjectVersion{marker, regular}
	vrepo.fixedGet = map[uuid.UUID]ObjectVersion{
		marker.VersionID:  marker,
		regular.VersionID: regular,
	}
	vrepo.current[objectID] = marker.VersionID

	h := NewVersionHandler(repo, vrepo)
	if err := h.UnsetDeleteMarkerCurrent(context.Background(), Object{ObjectID: objectID, Collection: "k", TenantID: uuid.Must(uuid.NewV7())}); err != nil {
		t.Fatal(err)
	}
	if vrepo.current[objectID] != regular.VersionID {
		t.Errorf("current should flip to regular, got %v", vrepo.current[objectID])
	}
}

func TestUnsetDeleteMarkerCurrentNoOpWhenVersioningOff(t *testing.T) {
	repo := &fakeObjectRepo{meta: BucketMeta{VersioningEnabled: false}}
	vrepo := newFakeVersionRepo()
	objectID := uuid.Must(uuid.NewV7())
	vrepo.current[objectID] = uuid.Must(uuid.NewV7()) // wouldn't matter

	h := NewVersionHandler(repo, vrepo)
	prior := vrepo.current[objectID]
	if err := h.UnsetDeleteMarkerCurrent(context.Background(), Object{ObjectID: objectID, Collection: "k"}); err != nil {
		t.Fatal(err)
	}
	if vrepo.current[objectID] != prior {
		t.Errorf("current should not change with versioning off")
	}
}

func timePtr(t time.Time) *time.Time { return &t }

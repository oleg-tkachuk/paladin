package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
)

// fakeMigRepo is an in-memory MigrationRepo: the migration, the provision
// states of its buckets, the movable objects, and what each call did.
type fakeMigRepo struct {
	m           StorageMigration
	provision   map[string]string
	movable     []MoveCandidate
	cleanable   []ObjectRef
	holds       MigrationHolds
	failedRuns  int32
	moveErr     error
	repointErr  error
	moved       []uuid.UUID
	repoints    []string
	transitions []string
	reasons     []string
	cleanedPast []string
}

func (f *fakeMigRepo) ListActive(context.Context, int) ([]StorageMigration, error) {
	return []StorageMigration{f.m}, nil
}

func (f *fakeMigRepo) BucketProvisionState(_ context.Context, _, bucket string) (string, error) {
	if s, ok := f.provision[bucket]; ok {
		return s, nil
	}
	return bucketgate.ProvisionReady, nil
}

func (f *fakeMigRepo) EnterMoving(context.Context, MigrationRead) error {
	f.transitions = append(f.transitions, MigStateMoving)
	return nil
}

func (f *fakeMigRepo) Repoint(_ context.Context, _ MigrationRead, from, to MigrationBucket, layout string, _ time.Duration) error {
	f.repoints = append(f.repoints, from.Bucket+">"+to.Bucket+":"+layout)
	return f.repointErr
}

func (f *fakeMigRepo) ListMovable(_ context.Context, _ MigrationRead, _ MigrationBucket, after uuid.UUID, limit int) ([]MoveCandidate, error) {
	var out []MoveCandidate
	for _, c := range f.movable {
		if strings.Compare(c.ID.String(), after.String()) > 0 && len(out) < limit {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeMigRepo) MoveObject(ctx context.Context, _ MigrationRead, _, _ MigrationBucket, c MoveCandidate, fn func(context.Context, MoveCandidate) (string, error)) (bool, error) {
	if f.moveErr != nil {
		return false, f.moveErr
	}
	if _, err := fn(ctx, c); err != nil {
		return false, err
	}
	f.moved = append(f.moved, c.ID)
	return true, nil
}

func (f *fakeMigRepo) Holds(context.Context, MigrationRead, MigrationBucket, time.Duration) (MigrationHolds, error) {
	return f.holds, nil
}

func (f *fakeMigRepo) RecordPass(_ context.Context, _ MigrationRead, failed bool, _ int64) (int32, error) {
	if failed {
		f.failedRuns++
	} else {
		f.failedRuns = 0
	}
	return f.failedRuns, nil
}

func (f *fakeMigRepo) Complete(context.Context, MigrationRead, time.Duration) error {
	f.transitions = append(f.transitions, MigStateCompleted)
	return nil
}

func (f *fakeMigRepo) ListCleanable(_ context.Context, _ MigrationRead, _ MigrationBucket, afterCollection, afterKey string, limit int) ([]ObjectRef, error) {
	var out []ObjectRef
	for _, o := range f.cleanable {
		if o.Collection+"/"+o.Key > afterCollection+"/"+afterKey && len(out) < limit {
			out = append(out, o)
		}
	}
	return out, nil
}

func (f *fakeMigRepo) AdvanceCleanup(_ context.Context, _ MigrationRead, collection, key string) error {
	f.cleanedPast = append(f.cleanedPast, collection+"/"+key)
	f.m.CursorCollection, f.m.CursorKey = collection, key
	return nil
}

func (f *fakeMigRepo) MarkCleaned(context.Context, MigrationRead) error {
	f.transitions = append(f.transitions, MigStateCleaned)
	return nil
}

func (f *fakeMigRepo) StartRollback(_ context.Context, _ MigrationRead, reason string) error {
	f.transitions = append(f.transitions, MigStateRollingBack)
	f.reasons = append(f.reasons, reason)
	return nil
}

func (f *fakeMigRepo) FinishRollback(context.Context, MigrationRead) error {
	f.transitions = append(f.transitions, MigStateFailed)
	return nil
}

func (f *fakeMigRepo) Fail(_ context.Context, _ MigrationRead, reason string) error {
	f.transitions = append(f.transitions, MigStateFailed)
	f.reasons = append(f.reasons, reason)
	return nil
}

// fakeBytes is a store: the ETag and size at each location, and a log of
// copies and deletes.
type fakeBytes struct {
	etag    map[string]string
	size    map[string]int64
	copies  []string
	deletes []string
	// copySize, when set, is what a copy comes out as.
	copySize *int64
	// copyETag, when set, is the ETag a copy reports.
	copyETag string
}

func locKey(l CopyLocation) string { return l.Bucket + "|" + l.Collection + "/" + l.Key }

func (s *fakeBytes) CopyObject(_ context.Context, src, dst CopyLocation) error {
	s.copies = append(s.copies, locKey(src)+">"+locKey(dst))
	size, etag := s.size[locKey(src)], s.etag[locKey(src)]
	if s.copySize != nil {
		size = *s.copySize
	}
	if s.copyETag != "" {
		etag = s.copyETag
	}
	s.size[locKey(dst)], s.etag[locKey(dst)] = size, etag
	return nil
}

func (s *fakeBytes) DeleteObject(_ context.Context, l CopyLocation) error {
	s.deletes = append(s.deletes, locKey(l))
	return nil
}

func (s *fakeBytes) HeadObject(_ context.Context, l CopyLocation) (int64, error) {
	size, ok := s.size[locKey(l)]
	if !ok {
		return 0, fmt.Errorf("head %s: %w", locKey(l), ErrObjectMissing)
	}
	return size, nil
}

func (s *fakeBytes) ObjectETag(_ context.Context, l CopyLocation) (string, error) {
	etag, ok := s.etag[locKey(l)]
	if !ok {
		return "", fmt.Errorf("etag %s: %w", locKey(l), ErrObjectMissing)
	}
	return etag, nil
}

// The digests the tests put in buckets.
const (
	etagA = "0123456789abcdef0123456789abcdef"
	etagB = "fedcba9876543210fedcba9876543210"
)

func newMigration(state string) StorageMigration {
	return StorageMigration{
		TenantID: uuid.New(),
		Source:   MigrationBucket{ID: uuid.New(), BackendID: "be", Bucket: "shared"},
		Target:   MigrationBucket{ID: uuid.New(), BackendID: "be", Bucket: "dedicated"},
		State:    state,
	}
}

// candidate is an object of size 3 in collection c with ETag etag in the
// source, put there in s.
func candidate(s *fakeBytes, bucket, key, etag string) MoveCandidate {
	c := MoveCandidate{ID: uuid.NewV7(), Collection: "c", Key: key, SizeBytes: 3, ETag: etag}
	s.size[bucket+"|c/"+key], s.etag[bucket+"|c/"+key] = 3, etag
	return c
}

func newStore() *fakeBytes {
	return &fakeBytes{etag: map[string]string{}, size: map[string]int64{}}
}

func newMigWorker(repo *fakeMigRepo, s *fakeBytes) *StorageMigrationWorker {
	return &StorageMigrationWorker{Repo: repo, Copier: s, Deleter: s, Header: s, Identity: s, CopyBatch: 2}
}

// A ready target starts the moves; a failed one fails the migration with
// nothing changed; a pending one is waited for.
func TestStepProvisioning(t *testing.T) {
	for state, want := range map[string][]string{
		bucketgate.ProvisionReady:   {MigStateMoving},
		bucketgate.ProvisionFailed:  {MigStateFailed},
		bucketgate.ProvisionPending: nil,
	} {
		repo := &fakeMigRepo{m: newMigration(MigStateProvisioning), provision: map[string]string{"dedicated": state}}
		if err := newMigWorker(repo, newStore()).Tick(context.Background()); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if fmt.Sprint(repo.transitions) != fmt.Sprint(want) {
			t.Errorf("target %s: transitions %v, want %v", state, repo.transitions, want)
		}
	}
}

// A pass points the tenant's uploads at the target, moves every movable
// object over as many ticks as its batches take, and completes the
// migration when nothing holds the source.
func TestMovingPassMovesEveryObjectAndCompletes(t *testing.T) {
	s := newStore()
	m := newMigration(MigStateMoving)
	repo := &fakeMigRepo{m: m, holds: MigrationHolds{WritesClosed: true}}
	for _, k := range []string{"a", "b", "c"} {
		repo.movable = append(repo.movable, candidate(s, "shared", k, etagA))
	}
	w := newMigWorker(repo, s)
	for range 2 {
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.moved) != 3 {
		t.Fatalf("moved %d objects, want 3", len(repo.moved))
	}
	if repo.repoints[0] != "shared>dedicated:"+LayoutDedicated {
		t.Errorf("repointed %v, want the source at the target", repo.repoints)
	}
	if fmt.Sprint(repo.transitions) != fmt.Sprint([]string{MigStateCompleted}) {
		t.Errorf("transitions %v, want completed", repo.transitions)
	}
	if s.size["dedicated|c/a"] != 3 {
		t.Error("the copy is not in the target")
	}
}

// Anything that still holds the source — an object placed there, an
// anchor, a session, a binding, a URL not yet expired — keeps the migration
// moving.
func TestMovingWaitsForWhatHoldsTheSource(t *testing.T) {
	for name, h := range map[string]MigrationHolds{
		"an object placed there": {Placed: 1, WritesClosed: true},
		"an upload anchored":     {Anchored: 1, WritesClosed: true},
		"a multipart session":    {Sessions: 1, WritesClosed: true},
		"a collection bound":     {Bound: 1, WritesClosed: true},
		"a URL still valid":      {},
	} {
		repo := &fakeMigRepo{m: newMigration(MigStateMoving), holds: h}
		if err := newMigWorker(repo, newStore()).Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(repo.transitions) != 0 {
			t.Errorf("%s: transitions %v, want none", name, repo.transitions)
		}
	}
}

// A target that leaves ready rolls the migration back; a schema that refuses
// to move a collection does too.
func TestMovingRollsBackWhenTheTargetCannotTakeTheObjects(t *testing.T) {
	repo := &fakeMigRepo{m: newMigration(MigStateMoving), provision: map[string]string{"dedicated": bucketgate.ProvisionDeleting}}
	if err := newMigWorker(repo, newStore()).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(repo.transitions) != fmt.Sprint([]string{MigStateRollingBack}) || !strings.Contains(repo.reasons[0], bucketgate.ProvisionDeleting) {
		t.Fatalf("transitions %v reasons %v, want a rollback naming the target's state", repo.transitions, repo.reasons)
	}

	repo = &fakeMigRepo{m: newMigration(MigStateMoving), repointErr: RebindRefused("docs", errors.New("check violation"))}
	if err := newMigWorker(repo, newStore()).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(repo.transitions) != fmt.Sprint([]string{MigStateRollingBack}) || !strings.Contains(repo.reasons[0], `"docs"`) {
		t.Fatalf("transitions %v reasons %v, want a rollback naming the collection", repo.transitions, repo.reasons)
	}
}

// Moves that keep failing their verification roll the migration back after
// migstate.MaxFailedPasses passes in a row; fewer do not.
func TestMovingRollsBackAfterPassesOfFailedMoves(t *testing.T) {
	s := newStore()
	repo := &fakeMigRepo{m: newMigration(MigStateMoving), holds: MigrationHolds{Placed: 1}}
	c := candidate(s, "shared", "bad", etagA)
	s.etag["shared|c/bad"] = etagB // the source holds other bytes than the row records
	repo.movable = []MoveCandidate{c}
	w := newMigWorker(repo, s)
	for pass := 1; pass <= migstate.MaxFailedPasses; pass++ {
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		rolledBack := len(repo.transitions) > 0
		if want := pass == migstate.MaxFailedPasses; rolledBack != want {
			t.Fatalf("after %d failed passes rolled back = %v, want %v", pass, rolledBack, want)
		}
	}
	if !strings.Contains(repo.reasons[0], "c/bad") {
		t.Errorf("reason %q does not name the object", repo.reasons[0])
	}
}

// A rollback moves the objects placed in the target back into the source,
// points uploads at the source with the shared layout, and fails the
// migration once nothing holds the target.
func TestRollingBackMovesBackAndFails(t *testing.T) {
	s := newStore()
	repo := &fakeMigRepo{m: newMigration(MigStateRollingBack), holds: MigrationHolds{WritesClosed: true}}
	repo.movable = []MoveCandidate{candidate(s, "dedicated", "a", etagA)}
	if err := newMigWorker(repo, s).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.repoints[0] != "dedicated>shared:"+LayoutShared {
		t.Errorf("repointed %v, want the target at the source", repo.repoints)
	}
	if s.size["shared|c/a"] != 3 {
		t.Error("the object was not copied back into the source")
	}
	if fmt.Sprint(repo.transitions) != fmt.Sprint([]string{MigStateFailed}) {
		t.Errorf("transitions %v, want failed", repo.transitions)
	}
}

// A move is verified before it is placed: the source must hold the bytes
// the row records, and the copy must come out at the row's size and, where
// both are plain MD5 digests, with the source's digest.
func TestMoveBytesVerifiesTheCopy(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		prepare  func(s *fakeBytes, c *MoveCandidate)
		mismatch bool
	}{
		"a clean copy":                     {func(*fakeBytes, *MoveCandidate) {}, false},
		"the source holds other bytes":     {func(s *fakeBytes, _ *MoveCandidate) { s.etag["shared|c/k"] = etagB }, true},
		"the copy comes out another size":  {func(s *fakeBytes, _ *MoveCandidate) { short := int64(2); s.copySize = &short }, true},
		"the copy has another digest":      {func(s *fakeBytes, _ *MoveCandidate) { s.copyETag = etagB }, true},
		"a multipart ETag is not compared": {func(s *fakeBytes, _ *MoveCandidate) { s.copyETag = etagB + "-2" }, false},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStore()
			m := newMigration(MigStateMoving)
			c := candidate(s, "shared", "k", etagA)
			tc.prepare(s, &c)
			w := newMigWorker(&fakeMigRepo{m: m}, s)
			_, err := w.moveBytes(ctx, w.log(), m, m.Source, m.Target, c)
			if got := errors.Is(err, ErrMoveMismatch); got != tc.mismatch {
				t.Fatalf("err = %v, want mismatch %v", err, tc.mismatch)
			}
		})
	}
}

// A trashed object whose bytes a delete straight from the bucket took moves
// without a copy.
func TestMoveBytesOfATrashedObjectWithoutBytes(t *testing.T) {
	s := newStore()
	m := newMigration(MigStateMoving)
	w := newMigWorker(&fakeMigRepo{m: m}, s)
	c := MoveCandidate{ID: uuid.NewV7(), Collection: "c", Key: "gone", Trashed: true}
	if _, err := w.moveBytes(context.Background(), w.log(), m, m.Source, m.Target, c); err != nil {
		t.Fatalf("moveBytes: %v", err)
	}
	if len(s.copies) != 0 {
		t.Fatalf("copied %v, want nothing", s.copies)
	}
}

// The cleanup waits for cleanup_after, then deletes the source copy of every
// object in the target, batch by batch, and marks the migration cleaned.
func TestCompletedCleansTheSourceAfterItsRetention(t *testing.T) {
	s := newStore()
	now := time.Now()
	m := newMigration(MigStateCompleted)
	m.CleanupAfter = now.Add(time.Hour)
	repo := &fakeMigRepo{m: m, cleanable: []ObjectRef{{"c", "a"}, {"c", "b"}, {"c", "c"}}}
	w := newMigWorker(repo, s)
	w.Now = func() time.Time { return now }
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.deletes) != 0 {
		t.Fatalf("deleted %v before cleanup_after", s.deletes)
	}
	w.Now = func() time.Time { return now.Add(2 * time.Hour) }
	for range 3 {
		if err := w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(s.deletes) != fmt.Sprint([]string{"shared|c/a", "shared|c/b", "shared|c/c"}) {
		t.Errorf("deleted %v, want every source copy", s.deletes)
	}
	if fmt.Sprint(repo.transitions) != fmt.Sprint([]string{MigStateCleaned}) {
		t.Errorf("transitions %v, want cleaned", repo.transitions)
	}
}

// A move that fails for a reason other than its verification ends the tick,
// and the next one tries the same object again.
func TestAFailedMoveIsRetried(t *testing.T) {
	s := newStore()
	repo := &fakeMigRepo{m: newMigration(MigStateMoving), moveErr: errors.New("conn reset")}
	repo.movable = []MoveCandidate{candidate(s, "shared", "a", etagA)}
	w := newMigWorker(repo, s)
	if err := w.Tick(context.Background()); err == nil {
		t.Fatal("the failed move was not reported")
	}
	repo.moveErr = nil
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.moved) != 1 {
		t.Fatalf("moved %d, want the object retried and moved", len(repo.moved))
	}
}

func TestPlainMD5(t *testing.T) {
	for etag, want := range map[string]bool{etagA: true, strings.ToUpper(etagA): true, etagA + "-2": false, "": false, strings.Repeat("g", md5HexLength): false} {
		if got := plainMD5(etag); got != want {
			t.Errorf("plainMD5(%q) = %v, want %v", etag, got, want)
		}
	}
}

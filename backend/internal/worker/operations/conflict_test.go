package operations

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// rereadRepo answers each FindByIDs with the next of reads, the last one
// from then on: an object another write changes between the batch's reads.
type rereadRepo struct {
	objecth.Repository
	reads [][]objecth.Object
	calls int
	// updConflicts is how many UpdateMetadata calls conflict before one
	// succeeds; updArgs records each.
	updConflicts int
	updArgs      []objecth.UpdateMetadataArgs
}

func (r *rereadRepo) FindByIDs(context.Context, uuid.UUID, []uuid.UUID) ([]objecth.Object, error) {
	i := min(r.calls, len(r.reads)-1)
	r.calls++
	return r.reads[i], nil
}

func (r *rereadRepo) UpdateMetadata(_ context.Context, args objecth.UpdateMetadataArgs) (objecth.Object, error) {
	r.updArgs = append(r.updArgs, args)
	if len(r.updArgs) <= r.updConflicts {
		return objecth.Object{}, objecth.ErrVersionMismatch
	}
	return objecth.Object{ObjectID: args.ObjectID}, nil
}

// conflictingTransitions conflicts the first conflicts SoftDeletes of an
// object, then lets one through.
type conflictingTransitions struct {
	fakeTransitions
	conflicts int
	versions  []int64
}

func (f *conflictingTransitions) SoftDelete(_ context.Context, _ uuid.UUID, rv int64) error {
	f.versions = append(f.versions, rv)
	if len(f.versions) <= f.conflicts {
		return statemachine.ErrConflict
	}
	return nil
}

func available(id uuid.UUID, rv int64) objecth.Object {
	return objecth.Object{ObjectID: id, Collection: "k", ResourceVersion: rv, State: statemachine.StateAvailable}
}

func runDelete(t *testing.T, repo objecth.Repository, tr Transitioner, id uuid.UUID) BatchDeleteResponse {
	t.Helper()
	tenant := uuid.New()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr}
	body, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return decodeDeleteResp(t, body)
}

// An object changed between the read and the delete is read again and
// deleted at the version it holds now: a race, not a failure of the batch.
func TestBatchDeleteTriesAConflictAgainOnAFreshRead(t *testing.T) {
	id := uuid.New()
	repo := &rereadRepo{reads: [][]objecth.Object{{available(id, 3)}, {available(id, 4)}}}
	tr := &conflictingTransitions{conflicts: 1}
	resp := runDelete(t, repo, tr, id)
	if resp.Succeeded != 1 || resp.Failed != 0 {
		t.Fatalf("counts = %+v, want the second try to succeed", resp)
	}
	if len(tr.versions) != 2 || tr.versions[1] != 4 {
		t.Errorf("versions tried = %v, want 3 then the fresh read's 4", tr.versions)
	}
}

// One that conflicts on every try is a failure after conflictAttempts of
// them, with the conflict as its reason.
func TestBatchDeleteGivesUpOnAConflictAfterItsAttempts(t *testing.T) {
	id := uuid.New()
	repo := &rereadRepo{reads: [][]objecth.Object{{available(id, 3)}}}
	tr := &conflictingTransitions{conflicts: conflictAttempts}
	resp := runDelete(t, repo, tr, id)
	if resp.Failed != 1 || len(resp.Failures) != 1 || resp.Failures[0].Reason != statemachine.ErrConflict.Error() {
		t.Fatalf("response = %+v, want one failure for the conflict", resp)
	}
	if len(tr.versions) != conflictAttempts {
		t.Errorf("tries = %d, want %d", len(tr.versions), conflictAttempts)
	}
}

// The fresh read decides the second try: an object the other write moved to
// the trash already is done, not deleted twice.
func TestBatchDeleteReadsWhatTheConflictLeft(t *testing.T) {
	id := uuid.New()
	trashed := available(id, 4)
	trashed.State = statemachine.StateDeleted
	repo := &rereadRepo{reads: [][]objecth.Object{{available(id, 3)}, {trashed}}}
	tr := &conflictingTransitions{conflicts: 1}
	resp := runDelete(t, repo, tr, id)
	if resp.Succeeded != 1 || resp.AlreadyApplied != 1 || resp.Failed != 0 {
		t.Fatalf("counts = %+v, want it already applied", resp)
	}
	if len(tr.versions) != 1 {
		t.Errorf("tries = %d, want no second delete", len(tr.versions))
	}
}

// A tag merge that conflicts is merged again into the tags the object holds
// after the other write, at its new version.
func TestBatchUpdateTagsTriesAConflictAgainOnAFreshRead(t *testing.T) {
	tenant, id := uuid.New(), uuid.New()
	before := objecth.Object{ObjectID: id, Collection: "k", ResourceVersion: 5, Tags: map[string]string{"a": "1"}}
	after := objecth.Object{ObjectID: id, Collection: "k", ResourceVersion: 6, Tags: map[string]string{"a": "1", "b": "2"}}
	repo := &rereadRepo{reads: [][]objecth.Object{{before}, {after}}, updConflicts: 1}
	e := &BatchUpdateTagsExecutor{Objects: repo}
	body, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchUpdateTagsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}, Tags: map[string]string{"c": "3"},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp := decodeTagsResp(t, body); resp.Succeeded != 1 || resp.Failed != 0 {
		t.Fatalf("counts = %+v", resp)
	}
	last := repo.updArgs[len(repo.updArgs)-1]
	if last.ResourceVersion != 6 || last.Tags["b"] != "2" || last.Tags["c"] != "3" {
		t.Errorf("second write = version %d tags %v, want 6 with b and c", last.ResourceVersion, last.Tags)
	}
}

func TestIsVersionConflict(t *testing.T) {
	for err, want := range map[error]bool{
		statemachine.ErrConflict:   true,
		objecth.ErrVersionMismatch: true,
		errObjectNotFound:          false,
		nil:                        false,
	} {
		if got := isVersionConflict(err); got != want {
			t.Errorf("isVersionConflict(%v) = %v, want %v", err, got, want)
		}
	}
}

package operations

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

func runTags(t *testing.T, repo objecth.Repository, id uuid.UUID) BatchUpdateTagsResponse {
	t.Helper()
	tenant := uuid.New()
	e := &BatchUpdateTagsExecutor{Objects: repo}
	body, err := e.Execute(context.Background(), mkOp(t, tenant, batchh.BatchUpdateTagsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}, Tags: map[string]string{"a": "1"},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return decodeTagsResp(t, body)
}

// Only an AVAILABLE object takes a tag write. The store matches no other row,
// and the executor used to read that as a version conflict: it re-read the
// object conflictAttempts times and reported a resource_version mismatch for
// a write that could never land.
func TestBatchUpdateTagsRefusesAnObjectNotAvailableWithoutRetrying(t *testing.T) {
	for _, state := range []statemachine.State{statemachine.StateDeleted, statemachine.StatePending} {
		t.Run(string(state), func(t *testing.T) {
			id := uuid.New()
			obj := available(id, 2)
			obj.State = state
			repo := &rereadRepo{reads: [][]objecth.Object{{obj}}, updConflicts: conflictAttempts}
			resp := runTags(t, repo, id)
			if resp.Failed != 1 || len(resp.Failures) != 1 {
				t.Fatalf("response = %+v, want one failure", resp)
			}
			if want := objecth.MetadataUpdateRefusal(state).Error(); resp.Failures[0].Reason != want {
				t.Errorf("reason = %q, want %q", resp.Failures[0].Reason, want)
			}
			if len(repo.updArgs) != 0 {
				t.Errorf("tag writes = %d, want none for a %s object", len(repo.updArgs), state)
			}
			if repo.calls != 1 {
				t.Errorf("reads = %d, want the batch's one read and no retry", repo.calls)
			}
		})
	}
}

// An object trashed between the read and the write is refused on the re-read
// the conflict causes, not written to again.
func TestBatchUpdateTagsRefusesAnObjectTrashedDuringTheRetry(t *testing.T) {
	id := uuid.New()
	trashed := available(id, 3)
	trashed.State = statemachine.StateDeleted
	repo := &rereadRepo{reads: [][]objecth.Object{{available(id, 2)}, {trashed}}, updConflicts: 1}
	resp := runTags(t, repo, id)
	if resp.Failed != 1 || len(resp.Failures) != 1 ||
		resp.Failures[0].Reason != objecth.MetadataUpdateRefusal(statemachine.StateDeleted).Error() {
		t.Fatalf("response = %+v, want the trashed object refused", resp)
	}
	if len(repo.updArgs) != 1 {
		t.Errorf("tag writes = %d, want only the first, conflicting one", len(repo.updArgs))
	}
}

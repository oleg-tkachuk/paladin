package objecth

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// deleteRecorder is noopStorage recording every delete's bucket and key.
type deleteRecorder struct {
	noopStorage
	deleted *[]string
}

func (s deleteRecorder) DeleteObject(_ context.Context, _, bucket string, _ uuid.UUID, collection, key string) error {
	*s.deleted = append(*s.deleted, bucket+"|"+collection+"/"+key)
	return nil
}

// A completion whose promote a storage migration's rebind refused answers
// UNAVAILABLE and leaves the row PENDING: it is anchored to the bucket its
// URL names, so the retry moves the bytes into the new binding.
func TestCompleteObjectRetriesAPromoteTheRebindRefused(t *testing.T) {
	sm := &fakeStateMachine{promoteErr: &statemachine.BindingMovedError{}}
	repo := &completeRepo{}
	h, quota, versions, _, ctx, tenantID := tailHandler(t, repo, headStorage{etag: "e1", size: 12}, sm)
	repo.obj = pendingObject(tenantID)

	_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: repo.obj.ObjectID.String()})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", connect.CodeOf(err))
	}
	if len(sm.markedFailed) != 0 {
		t.Error("discarded an upload whose bytes the retry can still move")
	}
	if len(versions.inserted) != 0 || quota.calls != 0 {
		t.Error("the tail ran for a promote that was refused")
	}
}

// A copy's promote refused the same way leaves bytes under the new object's
// own key in a bucket it is no longer read from, and a row nothing anchors:
// both are removed, as a failed copy's, and the caller retries.
func TestCopyObjectDiscardsACopyTheRebindRefused(t *testing.T) {
	sm := &fakeStateMachine{promoteErr: &statemachine.BindingMovedError{}}
	repo := &completeRepo{}
	var deleted []string
	h, _, _, _, ctx, tenantID := tailHandler(t, repo, deleteRecorder{deleted: &deleted}, sm)
	repo.obj = copySource(tenantID)

	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.New().String(), DestCollection: "dst", DestKey: "copy.txt",
	})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", connect.CodeOf(err))
	}
	if len(deleted) != 1 || deleted[0] != "bucket-7|dst/copy.txt" {
		t.Errorf("deleted %v, want the copy under its own key", deleted)
	}
	if len(sm.markedFailed) != 1 || sm.markFailedBy[0] != statemachine.FailedStorageCopy {
		t.Errorf("discarded %v (%v), want the destination as a failed copy", sm.markedFailed, sm.markFailedBy)
	}
}

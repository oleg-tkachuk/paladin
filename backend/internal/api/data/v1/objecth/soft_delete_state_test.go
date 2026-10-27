package objecth

import (
	"errors"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// A soft delete of an object that is not AVAILABLE is refused for its state,
// not reported as a version conflict: the console read that as "reload and
// retry", and an upload that never finished can never be trashed.
func TestSoftDeleteRefusesAnObjectThatIsNotAvailable(t *testing.T) {
	cases := []struct {
		state statemachine.State
		want  string
	}{
		{statemachine.StatePending, "upload has not completed"},
		{statemachine.StateDeleted, "DELETED"},
	}
	for _, c := range cases {
		t.Run(string(c.state), func(t *testing.T) {
			tenantID := uuid.New()
			objectID := uuid.New()
			repo := &purgeRepo{obj: Object{ObjectID: objectID, TenantID: tenantID, Collection: "docs", Key: "a.txt", State: c.state}}
			h := newPurgeHandler(t, repo, nil)

			err := h.DeleteObject(purgeCtx(tenantID), "docs", objectID.String(), "1", false, false)

			var cerr *connect.Error
			if !errors.As(err, &cerr) || cerr.Code() != connect.CodeFailedPrecondition {
				t.Fatalf("DeleteObject = %v, want FailedPrecondition", err)
			}
			if !strings.Contains(cerr.Message(), c.want) {
				t.Errorf("message %q does not say %q", cerr.Message(), c.want)
			}
		})
	}
}

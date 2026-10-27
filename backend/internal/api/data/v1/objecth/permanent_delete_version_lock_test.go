package objecth

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
)

// A permanent delete removes every version of the object, so a lock on any
// version refuses it. The pre-check used to read only the current version's
// lock: an older version's hold passed it, the SQL guard (or the version
// trigger) then refused the row, and the caller got Internal or Aborted
// instead of the FailedPrecondition that says what to do.
func TestPermanentDeleteRefusesALockOnAnyVersion(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	cases := []struct {
		name   string
		locks  []ObjectLock
		bypass bool
		refuse bool
	}{
		{name: "no locks", refuse: false},
		{name: "legal hold on an older version",
			locks: []ObjectLock{{}, {LegalHold: true}}, refuse: true},
		{name: "compliance retention on an older version",
			locks: []ObjectLock{{Mode: "COMPLIANCE", RetainUntil: &future}}, refuse: true},
		{name: "compliance retention ignores the governance bypass",
			locks: []ObjectLock{{Mode: "COMPLIANCE", RetainUntil: &future}}, bypass: true, refuse: true},
		{name: "governance retention without the bypass",
			locks: []ObjectLock{{Mode: "GOVERNANCE", RetainUntil: &future}}, refuse: true},
		{name: "governance retention with the bypass",
			locks: []ObjectLock{{Mode: "GOVERNANCE", RetainUntil: &future}}, bypass: true, refuse: false},
		{name: "expired retention and a released hold",
			locks: []ObjectLock{{Mode: "COMPLIANCE", RetainUntil: &past}, {}}, refuse: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			objectID := uuid.New()
			repo := &purgeRepo{
				obj:   Object{ObjectID: objectID, TenantID: tenantID, Collection: "docs", Key: "a.txt"},
				locks: tc.locks,
			}
			h := newPurgeHandler(t, repo, &failingDeleteStorage{})

			err := h.PermanentDelete(purgeCtx(tenantID), tenantID, repo.obj, 0, tc.bypass)
			if !tc.refuse {
				if err != nil {
					t.Fatalf("PermanentDelete = %v, want success", err)
				}
				return
			}
			var cerr *connect.Error
			if !errors.As(err, &cerr) || cerr.Code() != connect.CodeFailedPrecondition {
				t.Fatalf("PermanentDelete = %v, want FailedPrecondition", err)
			}
			if repo.hardDeletes != 0 {
				t.Errorf("row delete attempted %d times on a locked object, want 0", repo.hardDeletes)
			}
		})
	}
}

package objecth

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"
)

// A new version takes its bucket's default retention only where the bucket
// has opted into object lock, names a default, and the deployment does
// object lock at all; a default that cannot be applied is reported.
func TestApplyDefaultLock(t *testing.T) {
	withDefault := BucketMeta{ObjectLockEnabled: true, ObjectLockDefaultMode: LockModeGovernance, ObjectLockDefaultRetention: time.Hour}
	for name, tc := range map[string]struct {
		locks   *lockRepoStub
		meta    BucketMeta
		applied int
		wantErr bool
	}{
		"applied":             {&lockRepoStub{}, withDefault, 1, false},
		"no lock repository":  {nil, withDefault, 0, false},
		"bucket not opted in": {&lockRepoStub{}, BucketMeta{ObjectLockDefaultMode: LockModeGovernance}, 0, false},
		"no default":          {&lockRepoStub{}, BucketMeta{ObjectLockEnabled: true}, 0, false},
		"store failing":       {&lockRepoStub{defaultErr: errors.New("db down")}, withDefault, 1, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := &VersionHandler{}
			if tc.locks != nil {
				h.locks = tc.locks
			}
			err := h.applyDefaultLock(context.Background(), uuid.New(), uuid.New(), tc.meta)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if tc.locks != nil && len(tc.locks.defaults) != tc.applied {
				t.Fatalf("applied %d defaults, want %d", len(tc.locks.defaults), tc.applied)
			}
			if tc.applied == 1 && (tc.locks.defaults[0].mode != LockModeGovernance || tc.locks.defaults[0].retention != time.Hour) {
				t.Errorf("applied %+v, want the bucket's default", tc.locks.defaults[0])
			}
		})
	}
}

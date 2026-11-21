package adapters

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// A collection's constraints override the bucket's versioning and lock flags
// either way; a key it does not set, or sets to something not a bool, leaves
// the bucket's flag. The bucket's default lock mode and retention carry over.
func TestOverlayVersioningAndLock(t *testing.T) {
	governance := objecth.LockModeGovernance
	const retentionSeconds = 3600
	for _, tc := range []struct {
		name        string
		bucket      objecth.BucketMeta
		constraints string
		mode        *string
		retention   int64
		want        objecth.BucketMeta
	}{
		{"no overrides", objecth.BucketMeta{VersioningEnabled: true}, "", nil, 0,
			objecth.BucketMeta{VersioningEnabled: true}},
		{"forced off", objecth.BucketMeta{VersioningEnabled: true, ObjectLockEnabled: true},
			`{"versioning_enabled": false, "object_lock_enabled": false}`, nil, 0, objecth.BucketMeta{}},
		{"forced on", objecth.BucketMeta{},
			`{"versioning_enabled": true, "object_lock_enabled": true}`, nil, 0,
			objecth.BucketMeta{VersioningEnabled: true, ObjectLockEnabled: true}},
		{"not a bool", objecth.BucketMeta{VersioningEnabled: true}, `{"versioning_enabled": "no"}`, nil, 0,
			objecth.BucketMeta{VersioningEnabled: true}},
		{"lock defaults", objecth.BucketMeta{}, "", &governance, retentionSeconds,
			objecth.BucketMeta{ObjectLockDefaultMode: governance, ObjectLockDefaultRetention: retentionSeconds * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.bucket
			overlayVersioningAndLock(&got, []byte(tc.constraints), tc.mode, tc.retention)
			if got.VersioningEnabled != tc.want.VersioningEnabled || got.ObjectLockEnabled != tc.want.ObjectLockEnabled ||
				got.ObjectLockDefaultMode != tc.want.ObjectLockDefaultMode ||
				got.ObjectLockDefaultRetention != tc.want.ObjectLockDefaultRetention {
				t.Fatalf("overlay = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Zero is an absence, not a page size: the shims forward an unset page
// message as a zero, so returning it unchanged would mean LIMIT 0 — an empty
// page from a table with rows in it.
func TestPageSizeOrDefault(t *testing.T) {
	cases := map[string]struct {
		in   int32
		want int32
	}{
		"unset":          {0, defaultPageSize},
		"negative":       {-1, defaultPageSize},
		"one":            {1, 1},
		"ordinary":       {25, 25},
		"at the maximum": {maxPageSize, maxPageSize},
		"over":           {maxPageSize + 1, defaultPageSize},
		"absurd":         {1 << 30, defaultPageSize},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pageSizeOrDefault(tc.in); got != tc.want {
				t.Errorf("pageSizeOrDefault(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

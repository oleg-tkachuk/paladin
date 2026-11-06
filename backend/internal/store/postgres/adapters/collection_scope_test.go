package adapters

import "testing"

// A list scoped to one side of the (backend, bucket) pair names a bucket that
// does not exist, so it must still be scoped, never widened to every bucket.
func TestBucketScope(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		backend, bucket string
		scoped          bool
	}{
		"neither":      {scoped: false},
		"both":         {backend: "primary", bucket: "photos", scoped: true},
		"backend only": {backend: "primary", scoped: true},
		"bucket only":  {bucket: "photos", scoped: true},
	} {
		onBackend, onBucket := bucketScope(tc.backend, tc.bucket)
		if !tc.scoped {
			if onBackend != nil || onBucket != nil {
				t.Errorf("%s: scoped to (%v, %v), want every bucket", name, onBackend, onBucket)
			}
			continue
		}
		if onBackend == nil || onBucket == nil || *onBackend != tc.backend || *onBucket != tc.bucket {
			t.Errorf("%s: scope (%v, %v), want (%q, %q)", name, onBackend, onBucket, tc.backend, tc.bucket)
		}
	}
}

package admin

import (
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
)

// A migration's buckets are resource names while they exist, and empty once
// a finished migration's bucket was deleted — never a name with no bucket in
// it.
func TestStorageMigrationToProtoBuckets(t *testing.T) {
	const backend, source, target = "primary", "shared", "paladin-t"
	for name, tc := range map[string]struct {
		m                      tenanth.StorageMigration
		wantSource, wantTarget string
	}{
		"both buckets": {
			tenanth.StorageMigration{SourceBackendName: backend, SourceBucketName: source,
				TargetBackendName: backend, TargetBucketName: target},
			"storageBackends/primary/buckets/shared", "storageBackends/primary/buckets/paladin-t",
		},
		"the target deleted": {
			tenanth.StorageMigration{SourceBackendName: backend, SourceBucketName: source},
			"storageBackends/primary/buckets/shared", "",
		},
		"the source deleted": {
			tenanth.StorageMigration{TargetBackendName: backend, TargetBucketName: target},
			"", "storageBackends/primary/buckets/paladin-t",
		},
	} {
		t.Run(name, func(t *testing.T) {
			tc.m.TenantID = uuid.New()
			got := storageMigrationToProto(&tc.m)
			if got.GetSourceBucket() != tc.wantSource || got.GetTargetBucket() != tc.wantTarget {
				t.Errorf("buckets %q, %q; want %q, %q", got.GetSourceBucket(), got.GetTargetBucket(), tc.wantSource, tc.wantTarget)
			}
		})
	}
}

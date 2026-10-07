//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// skipVersionCheck is the expected_version that tells a guarded write to
// ignore resource_version.
const skipVersionCheck int64 = 0

// A guarded bucket write that touches no row has two causes, and they ask the
// caller for different things: the bucket is gone (NOT_FOUND, stop), or it is
// there at another version (ABORTED, re-read and retry). Both used to come
// back as a version mismatch, so deleting a missing bucket with the version
// check skipped answered ABORTED — found on the live cluster.
func TestGuardedBucketWritesTellMissingFromStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)

	backendID := "be-" + uuid.NewString()[:8]
	bucketName := "bk-" + uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name)
		 SELECT id, $2 FROM storage_backends WHERE name = $1`,
		backendID, bucketName)
	current, err := repo.Get(ctx, backendID, bucketName)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stale := current.ResourceVersion + 1

	writes := map[string]func(name string, version int64) error{
		"delete": func(name string, v int64) error { return repo.Delete(ctx, backendID, name, v) },
		"mark deleting": func(name string, v int64) error {
			return repo.MarkDeleting(ctx, backendID, name, v)
		},
		"update": func(name string, v int64) error {
			return repo.UpdateBasic(ctx, admindomain.Bucket{BackendID: backendID, BucketName: name, DisplayName: "x"},
				v, []string{"display_name"})
		},
		"set policy": func(name string, v int64) error { return repo.SetPolicy(ctx, backendID, name, "", v) },
	}
	for what, write := range writes {
		t.Run(what, func(t *testing.T) {
			for _, version := range []int64{skipVersionCheck, stale} {
				if err := write("no-such-bucket", version); !errors.Is(err, admindomain.ErrNotFound) {
					t.Errorf("missing bucket at version %d = %v, want ErrNotFound", version, err)
				}
			}
			if err := write(bucketName, stale); !errors.Is(err, admindomain.ErrVersionMismatch) {
				t.Errorf("stale version = %v, want ErrVersionMismatch", err)
			}
		})
	}
}

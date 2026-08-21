//go:build integration

// The bucket configuration setters are what an operator uses to turn on
// versioning, object lock, replication and lifecycle. They are also the least
// exercised code in the tree: every one of them was at 0% coverage.
//
// The failure they invite is not a query that breaks — the PREPARE gate and
// sqlc both catch a column that does not exist. It is a query that writes to a
// column that does exist and is the wrong one. Nothing static can see that;
// only a write followed by a read can. multipart's ListParts had exactly this
// shape, filtering on the part row's own primary key while being handed an
// upload id, and it went unnoticed because nothing ever called it.
//
// So each setter here is checked by round-trip: set a value that is
// distinguishable from every neighbouring field, read the bucket back, and
// assert the value landed where it was meant to and nothing beside it moved.
package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

func newBucketFixture(t *testing.T) (context.Context, *pgxpool.Pool, *adapters.BucketRepoV2, admindomain.Bucket) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "cfg-backend"
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

	repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)
	b := admindomain.Bucket{
		BackendID:   backendID,
		BucketName:  "cfg-bucket",
		DisplayName: "config round-trip",
		Region:      "eu-central-1",
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	got, err := repo.Get(ctx, backendID, b.BucketName)
	if err != nil {
		t.Fatalf("get bucket: %v", err)
	}
	return ctx, pool, repo, got
}

// TestBucketConfigSettersRoundTrip walks every setter in sequence, re-reading
// between each so a setter that clobbers a sibling field is caught by the next
// assertion rather than hidden by it.
func TestBucketConfigSettersRoundTrip(t *testing.T) {
	ctx, _, repo, b := newBucketFixture(t)
	reread := func(t *testing.T) admindomain.Bucket {
		t.Helper()
		got, err := repo.Get(ctx, b.BackendID, b.BucketName)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		return got
	}

	t.Run("versioning", func(t *testing.T) {
		// Enabled and KeepDeletesForever are both booleans on the same row:
		// writing one into the other's column is invisible unless they differ.
		want := admindomain.BucketVersioning{Enabled: true, KeepDeletesForever: false}
		if err := repo.SetVersioning(ctx, b.BackendID, b.BucketName, want, b.ResourceVersion); err != nil {
			t.Fatalf("set: %v", err)
		}
		got := reread(t)
		if got.Versioning != want {
			t.Errorf("versioning: got %+v want %+v", got.Versioning, want)
		}
		b = got
	})

	t.Run("object lock", func(t *testing.T) {
		want := admindomain.ObjectLockConfig{
			Enabled:          true,
			DefaultMode:      "COMPLIANCE",
			DefaultRetention: 72 * time.Hour,
		}
		if err := repo.SetObjectLock(ctx, b.BackendID, b.BucketName, want, b.ResourceVersion); err != nil {
			t.Fatalf("set: %v", err)
		}
		got := reread(t)
		if got.ObjectLock != want {
			t.Errorf("object lock: got %+v want %+v", got.ObjectLock, want)
		}
		// Object lock and versioning are coupled in S3 semantics but stored
		// separately here; setting one must not silently reset the other.
		if !got.Versioning.Enabled {
			t.Error("SetObjectLock cleared versioning")
		}
		b = got
	})

	t.Run("replication", func(t *testing.T) {
		want := admindomain.BucketReplication{
			Enabled:           true,
			DestinationBucket: "storageBackends/other/buckets/mirror",
			Filter:            `object.size_bytes > 1024`,
		}
		if err := repo.SetReplication(ctx, b.BackendID, b.BucketName, want, b.ResourceVersion); err != nil {
			t.Fatalf("set: %v", err)
		}
		got := reread(t)
		if got.Replication != want {
			t.Errorf("replication: got %+v want %+v", got.Replication, want)
		}
		b = got
	})

	t.Run("lifecycle", func(t *testing.T) {
		want := []admindomain.LifecycleRule{{
			ID:      "archive-old",
			Enabled: true,
			Match:   `object.path.startsWith("logs/")`,
			Transition: &admindomain.LifecycleTransition{
				After: 30 * 24 * time.Hour, StorageClass: "GLACIER",
			},
		}}
		if err := repo.SetLifecycle(ctx, b.BackendID, b.BucketName, want, b.ResourceVersion); err != nil {
			t.Fatalf("set: %v", err)
		}
		got := reread(t)
		if len(got.LifecycleRules) != 1 {
			t.Fatalf("lifecycle: got %d rules want 1", len(got.LifecycleRules))
		}
		r := got.LifecycleRules[0]
		if r.ID != want[0].ID || !r.Enabled || r.Match != want[0].Match {
			t.Errorf("lifecycle rule: got %+v", r)
		}
		if r.Transition == nil || r.Transition.StorageClass != "GLACIER" {
			t.Errorf("lifecycle transition lost: %+v", r.Transition)
		}
		b = got
	})

	t.Run("constraints", func(t *testing.T) {
		// Four size limits of the same type sit next to each other; distinct
		// values are what make a swapped column visible.
		want := admindomain.BucketConstraints{
			MaxObjectSizeBytes:        5_000_000_000,
			MinPartSizeBytes:          5_242_880,
			MaxPartSizeBytes:          104_857_600,
			MaxParts:                  10_000,
			AllowedContentTypes:       []string{"application/pdf", "image/png"},
			MaxPresignPutTTL:          15 * time.Minute,
			MaxPresignGetTTL:          time.Hour,
			RequiredChecksumAlgorithm: "SHA256",
		}
		if err := repo.SetConstraints(ctx, b.BackendID, b.BucketName, want, b.ResourceVersion); err != nil {
			t.Fatalf("set: %v", err)
		}
		got := reread(t)
		c := got.Constraints
		if c.MaxObjectSizeBytes != want.MaxObjectSizeBytes ||
			c.MinPartSizeBytes != want.MinPartSizeBytes ||
			c.MaxPartSizeBytes != want.MaxPartSizeBytes ||
			c.MaxParts != want.MaxParts {
			t.Errorf("size constraints: got %+v want %+v", c, want)
		}
		if c.MaxPresignPutTTL != want.MaxPresignPutTTL || c.MaxPresignGetTTL != want.MaxPresignGetTTL {
			t.Errorf("presign TTLs: got put=%v get=%v want put=%v get=%v",
				c.MaxPresignPutTTL, c.MaxPresignGetTTL, want.MaxPresignPutTTL, want.MaxPresignGetTTL)
		}
		if c.RequiredChecksumAlgorithm != want.RequiredChecksumAlgorithm {
			t.Errorf("checksum algorithm: got %q want %q", c.RequiredChecksumAlgorithm, want.RequiredChecksumAlgorithm)
		}
		assertStrings(t, "allowed content types", c.AllowedContentTypes, want.AllowedContentTypes)
		b = got
	})

	t.Run("policy", func(t *testing.T) {
		const want = `permit(principal, action, resource);`
		if err := repo.SetPolicy(ctx, b.BackendID, b.BucketName, want, b.ResourceVersion); err != nil {
			t.Fatalf("set: %v", err)
		}
		got := reread(t)
		if got.CedarPolicy != want {
			t.Errorf("cedar policy: got %q want %q", got.CedarPolicy, want)
		}
		b = got
	})

	t.Run("everything set earlier survived", func(t *testing.T) {
		got := reread(t)
		if !got.Versioning.Enabled {
			t.Error("versioning lost")
		}
		if !got.ObjectLock.Enabled || got.ObjectLock.DefaultMode != "COMPLIANCE" {
			t.Errorf("object lock lost: %+v", got.ObjectLock)
		}
		if !got.Replication.Enabled || got.Replication.Filter == "" {
			t.Errorf("replication lost: %+v", got.Replication)
		}
		if len(got.LifecycleRules) != 1 {
			t.Error("lifecycle rules lost")
		}
		if got.Constraints.MaxParts != 10_000 {
			t.Errorf("constraints lost: %+v", got.Constraints)
		}
	})
}

// TestBucketSettersEnforceOptimisticConcurrency pins the version gate. Two
// operators editing the same bucket must not silently overwrite each other:
// the loser's change — which may be the one that turned on object lock — would
// vanish with no error anywhere.
//
// The gate has a deliberate escape: expected_version 0 skips the check, which
// is how scripted bootstrap writes a bucket it did not first read. That is
// worth pinning too, because it is indistinguishable from a caller who simply
// forgot to thread the version through.
func TestBucketSettersEnforceOptimisticConcurrency(t *testing.T) {
	ctx, _, repo, b := newBucketFixture(t)

	// Advance the row once so a genuinely stale, non-zero version exists.
	// bump_resource_version fires on every UPDATE, so this moves 1 → 2.
	if err := repo.SetPolicy(ctx, b.BackendID, b.BucketName, "// v1", b.ResourceVersion); err != nil {
		t.Fatalf("prime: %v", err)
	}
	stale := b.ResourceVersion
	current, err := repo.Get(ctx, b.BackendID, b.BucketName)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if current.ResourceVersion == stale {
		t.Fatalf("resource_version did not advance on write (still %d) — "+
			"the version gate cannot detect a concurrent edit", stale)
	}

	setters := []struct {
		name string
		set  func(version int64) error
	}{
		{"versioning", func(v int64) error {
			return repo.SetVersioning(ctx, b.BackendID, b.BucketName, admindomain.BucketVersioning{Enabled: true}, v)
		}},
		{"object lock", func(v int64) error {
			return repo.SetObjectLock(ctx, b.BackendID, b.BucketName, admindomain.ObjectLockConfig{Enabled: true, DefaultMode: "GOVERNANCE"}, v)
		}},
		{"replication", func(v int64) error {
			return repo.SetReplication(ctx, b.BackendID, b.BucketName, admindomain.BucketReplication{Enabled: true, DestinationBucket: "x"}, v)
		}},
		{"lifecycle", func(v int64) error {
			return repo.SetLifecycle(ctx, b.BackendID, b.BucketName, nil, v)
		}},
		{"constraints", func(v int64) error {
			return repo.SetConstraints(ctx, b.BackendID, b.BucketName, admindomain.BucketConstraints{MaxParts: 1}, v)
		}},
		{"policy", func(v int64) error {
			return repo.SetPolicy(ctx, b.BackendID, b.BucketName, "// v2", v)
		}},
	}

	for _, tc := range setters {
		t.Run(tc.name+" refuses a stale version", func(t *testing.T) {
			if err := tc.set(stale); !errors.Is(err, admindomain.ErrVersionMismatch) {
				t.Errorf("stale version %d accepted (err=%v) — concurrent edits overwrite silently", stale, err)
			}
		})
	}

	t.Run("version 0 deliberately bypasses the gate", func(t *testing.T) {
		if err := repo.SetVersioning(ctx, b.BackendID, b.BucketName,
			admindomain.BucketVersioning{Enabled: true}, 0); err != nil {
			t.Fatalf("unversioned write refused: %v", err)
		}
		got, err := repo.Get(ctx, b.BackendID, b.BucketName)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if !got.Versioning.Enabled {
			t.Error("unversioned write reported success but changed nothing")
		}
	})
}

// TestBucketSetterOnMissingBucketIsNotSuccess pins that addressing a bucket
// that does not exist is an error rather than a no-op reported as success. A
// setter that returns nil here tells an operator their policy was applied to a
// bucket that never received it.
func TestBucketSetterOnMissingBucketIsNotSuccess(t *testing.T) {
	ctx, _, repo, b := newBucketFixture(t)

	if err := repo.SetVersioning(ctx, b.BackendID, "no-such-bucket",
		admindomain.BucketVersioning{Enabled: true}, 1); err == nil {
		t.Error("SetVersioning on a missing bucket reported success")
	}
	if err := repo.SetPolicy(ctx, "no-such-backend", b.BucketName, "permit(principal, action, resource);", 1); err == nil {
		t.Error("SetPolicy on a missing backend reported success")
	}
	if _, err := repo.Get(ctx, b.BackendID, "no-such-bucket"); err == nil {
		t.Error("Get on a missing bucket reported success")
	}
}

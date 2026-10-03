//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// Bucket constraints were written by BucketService and read by nothing on
// the data plane. LookupBucketMeta — the resolution every upload, copy and
// presign path goes through — now returns them, decoded from exactly what
// the admin repository stored.
func TestLookupBucketMetaReturnsBucketConstraints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	q := sqlc.New(pool)

	var backendName, bucketName string
	if err := pool.QueryRow(ctx, `SELECT sb.name, bk.name
		  FROM collections c JOIN buckets bk ON bk.id = c.bucket_id
		  JOIN storage_backends sb ON sb.id = bk.backend_id
		 WHERE c.id = $1`, f.collectionID).Scan(&backendName, &bucketName); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, bucketName)

	admin := adapters.NewBucketRepoV2(q, pool)
	b, err := admin.Get(ctx, backendName, bucketName)
	if err != nil {
		t.Fatal(err)
	}
	want := uploadpolicy.BucketConstraints{
		MaxObjectSizeBytes: 4096, MinPartSizeBytes: 8 << 20, MaxPartSizeBytes: 64 << 20, MaxParts: 100,
		AllowedContentTypes: []string{"image/png"}, MaxPresignPutTTL: time.Minute, MaxPresignGetTTL: 2 * time.Minute,
		RequiredChecksumAlgorithm: "SHA256",
	}
	if err := admin.SetConstraints(ctx, backendName, bucketName, want, b.ResourceVersion); err != nil {
		t.Fatal(err)
	}

	objects := adapters.NewObjectRepo(q, pool)
	meta, err := objects.LookupBucketMeta(ctx, f.tenantID, f.collection, true)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Constraints.MaxObjectSizeBytes != want.MaxObjectSizeBytes ||
		meta.Constraints.MaxPresignGetTTL != want.MaxPresignGetTTL ||
		meta.Constraints.RequiredChecksumAlgorithm != want.RequiredChecksumAlgorithm ||
		len(meta.Constraints.AllowedContentTypes) != 1 || meta.Constraints.AllowedContentTypes[0] != "image/png" {
		t.Fatalf("constraints read back as %+v, want %+v", meta.Constraints, want)
	}

	// The multipart and presign repositories resolve through the same query.
	for name, lookup := range map[string]func() (uploadpolicy.BucketConstraints, error){
		"multipart": func() (uploadpolicy.BucketConstraints, error) {
			m, err := adapters.NewMultipartRepo(q, pool).LookupBucketMeta(ctx, f.tenantID, f.collection, true)
			return m.Constraints, err
		},
		"presign": func() (uploadpolicy.BucketConstraints, error) {
			m, err := adapters.NewPresignRepo(q, pool).LookupBucketMeta(ctx, f.tenantID, f.collection, false)
			return m.Constraints, err
		},
	} {
		got, err := lookup()
		if err != nil || got.MaxParts != want.MaxParts {
			t.Errorf("%s: constraints = %+v, err %v", name, got, err)
		}
	}

	// A document that does not decode is refused, not read as "no limits".
	mustExec(t, ctx, pool, `UPDATE buckets SET constraints = '{"MaxObjectSizeBytes": "lots"}'::jsonb WHERE name = $1`, bucketName)
	if _, err := objects.LookupBucketMeta(ctx, f.tenantID, f.collection, true); err == nil {
		t.Fatal("an undecodable constraints document was read as no constraints")
	}
}

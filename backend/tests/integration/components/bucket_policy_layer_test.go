//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// A bucket's cedar_policy is a layer of every collection bound to the bucket
// (migration 021). Two halves, both against a real schema: Fetch returns the
// bucket text and names the bucket for a collection scope, and a write to the
// column notifies every listener to drop its cache.
func TestBucketPolicyLayer(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	store := cedar.NewPostgresStore(pool)

	events, err := store.Watch(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	next := func(what string) cedar.ChangeEvent {
		t.Helper()
		select {
		case ev := <-events:
			return ev
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: no ChangeEvent within 10s", what)
		}
		panic("unreachable")
	}

	var backendName, bucketName string
	if err := pool.QueryRow(ctx,
		`SELECT sb.name, b.name
		   FROM collections c
		   JOIN buckets b           ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenantID, f.collection,
	).Scan(&backendName, &bucketName); err != nil {
		t.Fatalf("read fixture binding: %v", err)
	}

	const policy = `forbid(principal, action == Action::"DeleteObject", resource);`
	mustExec(t, ctx, pool,
		`UPDATE buckets b SET cedar_policy = $3
		   FROM storage_backends sb
		  WHERE sb.id = b.backend_id AND sb.name = $1 AND b.name = $2`,
		backendName, bucketName, policy)
	if ev := next("bucket policy update"); !ev.AllScopes {
		t.Fatalf("bucket policy update event = %+v, want AllScopes", ev)
	}

	layers, _, _, err := store.Fetch(ctx, f.tenantID, f.collection)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if layers.Bucket != policy {
		t.Errorf("bucket layer = %q, want %q", layers.Bucket, policy)
	}
	if want := "storageBackends/" + backendName + "/buckets/" + bucketName; layers.BucketName != want {
		t.Errorf("bucket name = %q, want %q", layers.BucketName, want)
	}

	// The tenant scope has no collection, so no bucket.
	tenantOnly, _, _, err := store.Fetch(ctx, f.tenantID, "")
	if err != nil {
		t.Fatalf("fetch tenant scope: %v", err)
	}
	if tenantOnly.Bucket != "" || tenantOnly.BucketName != "" {
		t.Errorf("tenant scope carried a bucket layer: %+v", tenantOnly)
	}

	// A write that leaves the policy unchanged stays silent.
	mustExec(t, ctx, pool,
		`UPDATE buckets b SET display_name = b.display_name || '+'
		   FROM storage_backends sb
		  WHERE sb.id = b.backend_id AND sb.name = $1 AND b.name = $2`,
		backendName, bucketName)
	select {
	case ev := <-events:
		t.Fatalf("non-policy bucket write notified: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

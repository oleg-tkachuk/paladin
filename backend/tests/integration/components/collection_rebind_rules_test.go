//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// rebindRule says what a constraint or trigger on collections does to the
// UPDATE of bucket_id that Rebind runs: the sentinel Rebind answers when it
// fires, or why it cannot fire there.
type rebindRule struct {
	sentinel   error
	cannotFire string
}

// collectionsRules is every constraint and user trigger the catalog holds on
// collections, whether on the table or pointing at it. A rule added by a
// migration fails TestEveryCollectionsRuleIsClassifiedForRebind until it is
// classified here — and, when it can fire, mapped in CollectionRepo.Rebind.
var collectionsRules = map[string]rebindRule{
	// The bucket is resolved by name inside the UPDATE: an unknown one is NULL.
	"collections_bucket_id_not_null": {sentinel: objectkey.ErrBucketNotFound},
	// The bucket resolved, then was deleted before the check took its lock.
	schema.CollectionsBucketFK: {sentinel: objectkey.ErrBucketNotFound},
	// collections_enforce_bucket_tenancy raises this.
	"collections_enforce_bucket_tenancy": {sentinel: admindomain.ErrBucketNotGranted},
	// collections_enforce_bucket_visibility raises collections_bucket_visibility
	// and collections_public_bucket_fixed on a rebind; its third rule,
	// collections_public_read_fixed, needs public_read or cache_control to change.
	"collections_enforce_bucket_visibility": {sentinel: publicread.ErrRule},

	"collections_bump_resource_version":     {cannotFire: "sets resource_version and updated_at, raises nothing"},
	"collections_notify_policy_changed":     {cannotFire: "notifies only when cedar_policy changes"},
	"collections_reseed_replication":        {cannotFire: "deletes the new bucket's replication seed, raises nothing"},
	"collections_policy_insert":             {cannotFire: "fires on INSERT"},
	"collections_policy_update":             {cannotFire: "fires on UPDATE OF cedar_policy"},
	"collections_policy_delete":             {cannotFire: "fires on DELETE"},
	schema.CollectionsPK:                    {cannotFire: "id is not changed"},
	schema.CollectionsNameUnique:            {cannotFire: "tenant_id and name are not changed"},
	"collections_tenant_id_id_key":          {cannotFire: "tenant_id and id are not changed"},
	schema.CollectionsTenantIDFK:            {cannotFire: "tenant_id is not changed, so the key is not checked"},
	schema.CollectionsFormat:                {cannotFire: "name is not changed and was valid"},
	"collections_cache_control_public_only": {cannotFire: "public_read and cache_control are not changed"},
	"objects_tenant_id_collection_id_fkey":  {cannotFire: "references (tenant_id, id), which are not changed"},
	"erased_objects_collection_id_fkey":     {cannotFire: "references id, which is not changed"},
	"collections_id_not_null":               {cannotFire: "id is not changed"},
	"collections_tenant_id_not_null":        {cannotFire: "tenant_id is not changed"},
	"collections_name_not_null":             {cannotFire: "name is not changed"},
	"collections_display_name_not_null":     {cannotFire: "display_name is not changed"},
	"collections_constraints_not_null":      {cannotFire: "constraints is not changed"},
	"collections_lifecycle_rules_not_null":  {cannotFire: "lifecycle_rules is not changed"},
	"collections_cedar_policy_not_null":     {cannotFire: "cedar_policy is not changed"},
	"collections_resource_version_not_null": {cannotFire: "the bump trigger sets it from the old value"},
	"collections_created_at_not_null":       {cannotFire: "created_at is not changed"},
	"collections_updated_at_not_null":       {cannotFire: "the bump trigger sets it to now()"},
	"collections_public_read_not_null":      {cannotFire: "public_read is not changed"},
	"collections_cache_control_not_null":    {cannotFire: "cache_control is not changed"},
}

// The catalog is the authority on what an UPDATE of collections can fire, not
// a reading of the migrations: every constraint on or pointing at the table,
// and every trigger on it, must be classified above.
func TestEveryCollectionsRuleIsClassifiedForRebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	rows, err := pool.Query(ctx, `
		SELECT conname FROM pg_constraint
		 WHERE conrelid = 'collections'::regclass OR confrelid = 'collections'::regclass
		UNION ALL
		SELECT tgname FROM pg_trigger
		 WHERE tgrelid = 'collections'::regclass AND NOT tgisinternal`)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[name] = true
		rule, ok := collectionsRules[name]
		switch {
		case !ok:
			t.Errorf("%s is not classified: say what Rebind answers when it fires, or why it cannot", name)
		case (rule.sentinel == nil) == (rule.cannotFire == ""):
			t.Errorf("%s: classify it as either a sentinel or a reason it cannot fire", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	for name := range collectionsRules {
		if !seen[name] {
			t.Errorf("%s is classified but not in the catalog", name)
		}
	}
}

// rebindFixture is a tenant with one collection of each visibility and buckets
// to move them to.
type rebindFixture struct {
	tenantID uuid.UUID
	backend  string
}

// Buckets and collections of rebindFixture.
const (
	privateBucket      = "private-a"
	otherPrivateBucket = "private-b"
	publicBucket       = "public-a"
	otherPublicBucket  = "public-b"
	strangersBucket    = "strangers"
	privateCollection  = "docs"
	publicCollection   = "photos"
	unknownBucket      = "no-such-bucket"
)

func seedRebindFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) rebindFixture {
	t.Helper()
	hex := uuid.New().String()[:8]
	f := rebindFixture{tenantID: uuid.New(), backend: "be-" + hex}
	stranger := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, f.backend)
	for id, slug := range map[uuid.UUID]string{f.tenantID: "t-" + hex, stranger: "s-" + hex} {
		mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $2)`, id, slug)
	}
	for _, b := range []struct {
		name   string
		public bool
		owner  uuid.UUID
	}{
		{privateBucket, false, f.tenantID},
		{otherPrivateBucket, false, f.tenantID},
		{publicBucket, true, f.tenantID},
		{otherPublicBucket, true, f.tenantID},
		{strangersBucket, false, stranger},
	} {
		mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, public_read, owner_tenant_id)
			 SELECT sb.id, $2, $3, $4 FROM storage_backends sb WHERE sb.name = $1`,
			f.backend, b.name, b.public, b.owner)
	}
	for name, bucket := range map[string]string{privateCollection: privateBucket, publicCollection: publicBucket} {
		mustExec(t, ctx, pool, `INSERT INTO collections (tenant_id, name, bucket_id, public_read)
			 SELECT $1, $2, b.id, b.public_read FROM buckets b
			   JOIN storage_backends sb ON sb.id = b.backend_id
			  WHERE sb.name = $3 AND b.name = $4`,
			f.tenantID, name, f.backend, bucket)
	}
	return f
}

// Each rule a rebind can fire reaches the caller as its own sentinel, never
// as the store's raw error.
func TestRebindRefusalsAreTyped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedRebindFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	for name, tc := range map[string]struct {
		collection, bucket string
		want               error
	}{
		"collections_bucket_id_not_null":  {privateCollection, unknownBucket, objectkey.ErrBucketNotFound},
		"collections_bucket_tenancy":      {privateCollection, strangersBucket, admindomain.ErrBucketNotGranted},
		"collections_bucket_visibility":   {privateCollection, publicBucket, publicread.ErrRule},
		"collections_public_bucket_fixed": {publicCollection, otherPublicBucket, publicread.ErrRule},
	} {
		err := repo.Rebind(ctx, f.tenantID, tc.collection, f.backend, tc.bucket, skipVersionCheck)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: rebind %s to %s = %v, want %v", name, tc.collection, tc.bucket, err, tc.want)
		}
	}
	if err := repo.Rebind(ctx, f.tenantID, privateCollection, f.backend, otherPrivateBucket, skipVersionCheck); err != nil {
		t.Fatalf("a rebind no rule refuses: %v", err)
	}
}

// lockWaitPoll is how often the test looks for the rebind waiting on the
// bucket row; lockWaitDeadline is how long it looks before it gives up.
const (
	lockWaitPoll     = 20 * time.Millisecond
	lockWaitDeadline = 30 * time.Second
)

// A bucket deleted between the UPDATE resolving it and the foreign key
// checking it trips collections_bucket_id_fkey: the bucket is not found, as
// when it was never there.
func TestRebindToABucketDeletedUnderIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedRebindFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	deleting, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = deleting.Rollback(ctx) }()
	var deleterPID int
	if err := deleting.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&deleterPID); err != nil {
		t.Fatalf("pid: %v", err)
	}
	if _, err := deleting.Exec(ctx, `DELETE FROM buckets b USING storage_backends sb
		 WHERE sb.id = b.backend_id AND sb.name = $1 AND b.name = $2`, f.backend, otherPrivateBucket); err != nil {
		t.Fatalf("delete the target bucket: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- repo.Rebind(ctx, f.tenantID, privateCollection, f.backend, otherPrivateBucket, skipVersionCheck)
	}()
	// The rebind still sees the bucket, and its foreign key check waits for
	// the delete to end.
	deadline := time.Now().Add(lockWaitDeadline)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)))`, deleterPID).Scan(&waiting); err != nil {
			t.Fatalf("look for the waiting rebind: %v", err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("rebind finished before the delete committed: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the rebind never waited on the deleted bucket")
		}
		time.Sleep(lockWaitPoll)
	}
	if err := deleting.Commit(ctx); err != nil {
		t.Fatalf("commit the delete: %v", err)
	}
	if err := <-done; !errors.Is(err, objectkey.ErrBucketNotFound) {
		t.Fatalf("rebind to a bucket deleted under it = %v, want ErrBucketNotFound", err)
	}
}

//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	objecttag "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecttagh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Creating something that already exists is an ordinary answer to an ordinary
// request. Both of these used to reach the caller as CodeInternal carrying the
// raw constraint text — verified against the compose stack for collections:
//
//	internal: create collection: create collection: ERROR: duplicate key value
//	violates unique constraint "collections_tenant_id_name_key" (SQLSTATE 23505)
//
// which is a 500 for the database working exactly as designed, and a doubled
// prefix on top. Their sibling adapters (buckets, users, tenants, storage
// migrations) have classified unique violations since they were written.

func TestCreateCollectionTwiceIsAlreadyExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	repo := adapters.NewCollectionRepo(q, pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	backendID := "be-" + uuid.NewString()[:8]
	bucketName := "bk-" + uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT id, $2, $3 FROM storage_backends WHERE name = $1`,
		backendID, bucketName, tenant)

	args := objectkey.CreateCollectionArgs{
		TenantID:   tenant,
		Collection: "docs",
		BackendID:  backendID,
		BucketName: bucketName,
	}
	if _, err := repo.Create(ctx, args); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(ctx, args)
	if !errors.Is(err, objectkey.ErrCollectionExists) {
		t.Fatalf("second create = %v, want ErrCollectionExists", err)
	}
}

// A Collection on a bucket the backend does not have: the create query
// resolves bucket_id by name, so the row arrived with NULL there and the
// caller got the NOT NULL violation as a 500. The console met it in the e2e
// suite when another spec deleted a bucket between the list and the create.
func TestCreateCollectionOnUnknownBucketIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	backendID := "be-" + uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)

	_, err := repo.Create(ctx, objectkey.CreateCollectionArgs{
		TenantID:   tenant,
		Collection: "docs",
		BackendID:  backendID,
		BucketName: "no-such-bucket",
	})
	if !errors.Is(err, objectkey.ErrBucketNotFound) {
		t.Fatalf("create on an unknown bucket = %v, want ErrBucketNotFound", err)
	}
}

func TestCreateObjectTagTwiceIsAlreadyExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	repo := adapters.NewObjectTagRepo(q)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	args := objecttag.CreateArgs{TenantID: tenant, Slug: "review"}
	if _, err := repo.Create(ctx, args); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := repo.Create(ctx, args)
	if !errors.Is(err, objecttag.ErrObjectTagExists) {
		t.Fatalf("second create = %v, want ErrObjectTagExists", err)
	}
}

// TestCreateBucketTwiceIsAlreadyExists is the runtime half against a real
// Postgres: the unique violation has to reach the caller as ErrAlreadyExists,
// not the ErrConflict it used to share with "that backend is not registered".
func TestCreateBucketTwiceIsAlreadyExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	repo := adapters.NewBucketRepoV2(q, pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	backendID := "be-" + uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)

	b := admindomain.Bucket{
		BackendID:     backendID,
		BucketName:    "bk-" + uuid.NewString()[:8],
		OwnerTenantID: tenant,
		Region:        "us-east-1",
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := repo.Create(ctx, b); !errors.Is(err, admindomain.ErrAlreadyExists) {
		t.Fatalf("second create = %v, want ErrAlreadyExists", err)
	}
}

// The other refusal on the same path must NOT move: naming a backend that does
// not exist is a precondition the caller has to fix, not a resource that is
// already there. One sentinel used to serve both.
func TestCreateBucketOnUnknownBackendStaysAConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	err := repo.Create(ctx, admindomain.Bucket{
		BackendID:     "no-such-backend-" + uuid.NewString()[:8],
		BucketName:    "bk-" + uuid.NewString()[:8],
		OwnerTenantID: tenant,
		Region:        "us-east-1",
	})
	if !errors.Is(err, admindomain.ErrConflict) {
		t.Fatalf("create on an unknown backend = %v, want ErrConflict", err)
	}
	if errors.Is(err, admindomain.ErrAlreadyExists) {
		t.Error("an unregistered backend must not read as AlreadyExists")
	}
}

//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	objecttag "github.com/oleg-tkachuk/paladin/internal/api/v1/object_tag"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
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

func TestCreateObjectTagTwiceIsAlreadyExists(t *testing.T) {
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

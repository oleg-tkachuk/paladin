//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A collection the tenant does not have is ErrCollectionNotFound, still
// wrapping pgx.ErrNoRows for a caller matching that; it used to be the bare
// driver error, which the handler could not tell from a store failure.
func TestCollectionReadOfAMissingCollection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	_, err := repo.Get(ctx, f.tenantID, "no-such-collection")
	if !errors.Is(err, objectkey.ErrCollectionNotFound) || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("get missing = %v, want ErrCollectionNotFound wrapping pgx.ErrNoRows", err)
	}
	if _, err := repo.Get(ctx, f.tenantID, f.collection); err != nil {
		t.Fatalf("get existing: %v", err)
	}
}

// Rebinding to a bucket that is not registered is ErrBucketNotFound, as
// creating on one is; it used to surface as a NOT NULL violation, which the
// handler answered FAILED_PRECONDITION.
func TestRebindToAnUnknownBucket(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	err := repo.Rebind(ctx, f.tenantID, f.collection, "no-such-backend", "no-such-bucket", skipVersionCheck)
	if !errors.Is(err, objectkey.ErrBucketNotFound) {
		t.Fatalf("rebind to an unknown bucket = %v, want ErrBucketNotFound", err)
	}
}

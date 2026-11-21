//go:build integration

package components

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A collection the tenant does not have is ErrCollectionNotFound, and its text
// names the collection only: the handler sends that text to the caller, so the
// driver's "no rows in result set" must not be in it.
func TestCollectionReadOfAMissingCollection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	_, err := repo.Get(ctx, f.tenantID, "no-such-collection")
	if !errors.Is(err, objectkey.ErrCollectionNotFound) {
		t.Fatalf("get missing = %v, want ErrCollectionNotFound", err)
	}
	if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), pgx.ErrNoRows.Error()) {
		t.Errorf("get missing = %q, carries the driver's error to the caller", err)
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

//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

const (
	visibilityBackend = "visibility-be"
	publicBucketName  = "public-bucket"
	otherPublicBucket = "public-bucket-2"
	privateBucketName = "private-bucket"
)

func seedVisibilityFixture(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	seedBackend(t, pool, visibilityBackend)
	for name, public := range map[string]bool{publicBucketName: true, otherPublicBucket: true, privateBucketName: false} {
		if _, err := pool.Exec(ctx, `
            INSERT INTO buckets (backend_id, name, public_read)
            SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`,
			visibilityBackend, name, public); err != nil {
			t.Fatalf("seed bucket %s: %v", name, err)
		}
	}
	tenant := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		tenant, "vis-"+tenant.String()[:8], "visibility "+tenant.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return tenant
}

// The schema holds a collection's visibility equal to its bucket's, fixes it
// at creation, and keeps a public collection in its bucket (ADR-0027) — each
// refusal surfacing as a public-collection rule, not a raw check violation.
func TestPublicCollectionSchemaRules(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := seedVisibilityFixture(t, h.PoolMigrate)
	repo := adapters.NewCollectionRepo(sqlc.New(h.PoolApp), h.PoolApp)
	// The app role's pool reads the tenant from the principal, as at runtime.
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "test", TenantID: tenant})

	create := func(name, bucket string, public bool, cacheControl string) (collectionh.Collection, error) {
		return repo.Create(ctx, collectionh.CreateCollectionArgs{
			TenantID: tenant, Collection: name, BackendID: visibilityBackend, BucketName: bucket,
			PublicRead: public, CacheControl: cacheControl,
		})
	}

	got, err := create("photos", publicBucketName, true, publicread.DefaultCacheControl)
	if err != nil {
		t.Fatalf("a public collection in a public bucket: %v", err)
	}
	if !got.PublicRead || got.CacheControl != publicread.DefaultCacheControl {
		t.Errorf("created %+v, want public with the default cache control", got)
	}
	if _, err := create("docs", privateBucketName, false, ""); err != nil {
		t.Fatalf("a private collection in a private bucket: %v", err)
	}

	for name, err := range map[string]error{
		"a public collection in a private bucket": func() error {
			_, err := create("leak-a", privateBucketName, true, publicread.DefaultCacheControl)
			return err
		}(),
		"a private collection in a public bucket": func() error {
			_, err := create("leak-b", publicBucketName, false, "")
			return err
		}(),
		"a public collection rebound to a private bucket": repo.Rebind(ctx, tenant, "photos", visibilityBackend, privateBucketName, 0),
		// Both public, yet the objects' URLs name the first.
		"a public collection rebound to another public bucket": repo.Rebind(ctx, tenant, "photos", visibilityBackend, otherPublicBucket, 0),
		"a private collection rebound into a public bucket":    repo.Rebind(ctx, tenant, "docs", visibilityBackend, publicBucketName, 0),
	} {
		if !errors.Is(err, publicread.ErrRule) {
			t.Errorf("%s: err = %v, want a public collection rule", name, err)
		}
	}

	for name, tc := range map[string]struct {
		sql        string
		constraint string
	}{
		"a collection made private": {
			`UPDATE collections SET public_read = false WHERE name = 'photos'`, schema.CollectionsPublicReadFixed,
		},
		"a public collection's cache control changed": {
			`UPDATE collections SET cache_control = 'no-store' WHERE name = 'photos'`, schema.CollectionsPublicReadFixed,
		},
		"a bucket made private": {
			`UPDATE buckets SET public_read = false WHERE name = '` + publicBucketName + `'`, schema.BucketsPublicReadFixed,
		},
		"a bucket's public base URL changed": {
			`UPDATE buckets SET public_base_url = 'https://cdn.example' WHERE name = '` + publicBucketName + `'`, schema.BucketsPublicReadFixed,
		},
	} {
		_, err := h.PoolMigrate.Exec(ctx, tc.sql)
		if !pgerr.Is(err, pgerr.CheckViolation) || !pgerr.ConstraintIs(err, tc.constraint) {
			t.Errorf("%s: err = %v, want check violation %s", name, err, tc.constraint)
		}
	}

	_, err = h.PoolMigrate.Exec(ctx,
		`UPDATE collections SET cache_control = 'no-store' WHERE name = 'docs'`)
	if !pgerr.Is(err, pgerr.CheckViolation) {
		t.Errorf("a private collection given a cache control: err = %v, want a check violation", err)
	}
}

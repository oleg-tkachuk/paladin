//go:build integration

// Producer wiring for the lifecycle events lifecycle_events_test.go and
// tenant_events_test.go leave out: each reaches NATS as a CloudEvents
// envelope of its own type, not merely the outbox.
package integration

import (
	"context"
	"strconv"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	collectionpkg "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	tenantpkg "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	objectpkg "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// firstResourceVersion is the version a freshly written row carries.
const firstResourceVersion = 1

// A collection's creation and its deletion each reach a subscriber.
func TestCollectionHandler_CreateAndDeleteDispatch(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "collection-create-events")
	const backendID = "primary"
	bucketName := "coll-events-" + uuid.New().String()[:8]
	seedOwnedBucket(t, f.h.PoolMigrate, tenant, backendID, bucketName)
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewCollectionRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := collectionpkg.NewHandler(repo, allowAll{})
	handler.SetEventProducer(probe.events)
	ctx := ctxAdmin(t, tenant)

	if _, err := handler.CreateCollection(ctx, collectionpkg.CreateCollectionArgs{
		TenantID: tenant, Collection: "docs", BackendID: backendID, BucketName: bucketName,
	}); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.collection.created")

	if err := handler.DeleteCollection(ctx, tenant, "docs", firstResourceVersion); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.collection.deleted")
}

// adoptingProvisioner answers the one backend question registering a bucket
// asks: the bucket exists there, so registering it adopts it. Marking a
// bucket for deletion on its backend leaves the backend work to the
// reconciler and calls nothing; anything else panics on the nil interface.
type adoptingProvisioner struct{ bucketh.Provisioner }

func (adoptingProvisioner) BucketExists(context.Context, string, string) (bool, error) {
	return true, nil
}

// A bucket's creation, and a deletion that leaves the backend work to the
// reconciler (`deleting`), each reach a subscriber.
func TestBucketHandler_CreateAndDeleteOnBackendDispatch(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bucket-create-events")
	const backendID = "primary"
	bucketName := "bucket-events-" + uuid.New().String()[:8]
	if _, err := f.h.PoolMigrate.Exec(context.Background(), `
        INSERT INTO storage_backends (name, kind, region, endpoint)
        VALUES ($1, 's3-compatible', 'us-east-1', 'http://localhost')
        ON CONFLICT (name) DO NOTHING`, backendID); err != nil {
		t.Fatalf("seed backend: %v", err)
	}
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewBucketRepoV2(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := bucketh.NewHandler(repo, adoptingProvisioner{}, allowAll{})
	handler.SetEventProducer(probe.events)
	ctx := ctxAdmin(t, tenant)

	if _, err := handler.CreateBucket(ctx, bucketh.CreateBucketInput{
		Bucket: admindomain.Bucket{BackendID: backendID, BucketName: bucketName, OwnerTenantID: tenant},
	}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.bucket.created")

	// Deleting on the backend is refused for a bucket Paladin did not
	// create there; this one stands in for one it did.
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`UPDATE buckets SET created_on_backend = true WHERE name = $1`, bucketName); err != nil {
		t.Fatalf("mark created on backend: %v", err)
	}
	if err := handler.DeleteBucket(ctx, bucketh.DeleteBucketInput{
		BackendID: backendID, BucketName: bucketName, DeleteOnBackend: true,
	}); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.bucket.deleting")
}

// Trashing a tenant and restoring it each reach a subscriber. The trash
// freezes the tenant's other events, but not the one saying it was trashed.
func TestTenantHandler_TrashAndRestoreDispatch(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "tenant-trash-events")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewTenantRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := tenantpkg.NewHandler(repo, allowAuth{})
	handler.SetEventProducer(probe.events)
	ctx := ctxAdmin(t, tenant)

	if err := handler.DeleteTenant(ctx, tenant, firstResourceVersion); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.tenant.trashed")

	if _, err := handler.RestoreTenant(ctx, tenant); err != nil {
		t.Fatalf("RestoreTenant: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.tenant.restored")
}

// presentStorage answers the one storage call a restore makes: the object's
// bytes are still on the backend. Anything else panics on the nil interface.
type presentStorage struct{ objectpkg.Storage }

func (presentStorage) Head(context.Context, string, string, uuid.UUID, string, string, string) (string, int64, string, string, error) {
	return "", 0, "", "", nil
}

// Restoring an object from the trash reaches a subscriber.
func TestObjectHandler_RestoreDispatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "object-restore-events")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "docs")
	objID := mustInsertAvailableObject(t, f.h.PoolMigrate, tenant, "docs", "mykey")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewObjectRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := objectpkg.NewHandler(repo, presentStorage{}, allowAll{}, nil,
		statemachine.New(f.h.PoolMigrate), objectpkg.PresignConfig{})
	handler.SetEventProducer(probe.events)
	ctx := ctxAdmin(t, tenant)

	if err := handler.DeleteObject(ctx, "docs", objID.String(), "", false, false); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.object.deleted")

	var version int64
	if err := f.h.PoolMigrate.QueryRow(context.Background(),
		`SELECT resource_version FROM objects WHERE id = $1`, objID).Scan(&version); err != nil {
		t.Fatalf("read the trashed object's version: %v", err)
	}
	if _, err := handler.RestoreObject(ctx, "docs", objID.String(), strconv.FormatInt(version, 10)); err != nil {
		t.Fatalf("RestoreObject: %v", err)
	}
	probe.expect(t, f, tenant.String(), "paladin.object.restored")
}

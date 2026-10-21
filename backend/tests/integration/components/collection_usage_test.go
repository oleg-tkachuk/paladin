//go:build integration

package components

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A collection's usage counts its available objects alone — not one still
// uploading, not one in the trash — with their bytes and the newest commit,
// for collections of several tenants in one read. One with none, or one not
// on record, is absent.
func TestCollectionUsage_CountsTheAvailableObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	a, b := seedFixture(t, ctx, pool), seedFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)
	newest := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	seed := func(f fixture, path string, state sqlc.ObjectState, size int64, committed time.Time) {
		mustExec(t, ctx, pool, `
			INSERT INTO objects (id, tenant_id, collection_id, path, state,
			                     content_type, checksum_algorithm, size_bytes, committed_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4::object_state,
			        'application/octet-stream', 0, $5, $6)`,
			f.tenantID, f.collectionID, path, string(state), size, committed)
	}
	seed(a, "one", sqlc.ObjectStateAVAILABLE, 100, newest.Add(-time.Hour))
	seed(a, "two", sqlc.ObjectStateAVAILABLE, 50, newest)
	seed(a, "uploading", sqlc.ObjectStatePENDING, 1000, newest.Add(time.Hour))
	seed(a, "trashed", sqlc.ObjectStateDELETED, 1000, newest.Add(time.Hour))
	seed(b, "only", sqlc.ObjectStateAVAILABLE, 7, newest)

	keyA := objectkey.CollectionKey{TenantID: a.tenantID, Collection: a.collection}
	keyB := objectkey.CollectionKey{TenantID: b.tenantID, Collection: b.collection}
	missing := objectkey.CollectionKey{TenantID: uuid.New(), Collection: "none"}
	got, err := repo.Usage(ctx, []objectkey.CollectionKey{keyA, keyB, missing})
	if err != nil {
		t.Fatal(err)
	}
	if u := got[keyA]; u.ObjectCount != 2 || u.TotalBytes != 150 || u.LastCommittedAt == nil || !u.LastCommittedAt.Equal(newest) {
		t.Errorf("a = %+v, want 2 objects, 150 bytes, last %s", u, newest)
	}
	if u := got[keyB]; u.ObjectCount != 1 || u.TotalBytes != 7 {
		t.Errorf("b = %+v, want 1 object, 7 bytes", u)
	}
	if _, ok := got[missing]; ok || len(got) != 2 {
		t.Errorf("usage = %+v, want a and b only", got)
	}
	if none, err := repo.Usage(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("no keys: %+v, %v", none, err)
	}
}

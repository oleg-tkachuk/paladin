//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// The e2e ingest spec's shape: a nested collection (e2e/<hex>) and a key with
// a "/" of its own. The filer reports the path the upload was signed for —
// storagepath.Compose's, the collection one segment — and that event
// promotes the object; a path spelling the collection over several segments
// names another collection's key and promotes nothing.
func TestIngest_SeaweedFSWebhookOfANestedCollection(t *testing.T) {
	const (
		collection = "e2e/0a1b2c3d"
		key        = "e2e/obj-0a1b2c3d.txt"
	)
	h := pgharness.Setup(t)
	ctx := context.Background()
	tenantID := mustCreateTenant(t, h.PoolMigrate, "ingest-nested-tenant")
	mustCreateCollection(t, h.PoolMigrate, tenantID, collection)
	objectID := mustInsertPendingObject(t, h.PoolMigrate, tenantID, collection, key)

	q := sqlc.New(h.PoolMigrate)
	worker := &eventingest.Worker{
		Handler: &eventingest.PromoteHandler{Lookup: q, Transitioner: statemachine.New(h.PoolMigrate)},
		Dedup:   &eventingest.PgxDedupStore{Q: q},
	}
	source := &eventingest.SeaweedFSSource{BucketName: ingestTestBucket, URI: "seaweedfs://primary"}
	deliver := func(path string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"key":        "/buckets/" + ingestTestBucket + "/" + path,
			"event_type": "create",
			"message": map[string]any{"new_entry": map[string]any{
				"commit_ts_ns": time.Now().UnixNano(),
				"attributes":   map[string]any{"file_size": ingestTestSize},
			}},
		})
		evs, err := source.Parse(payload, "application/json")
		if err != nil || len(evs) != 1 {
			t.Fatalf("parse %q: %d events, %v", path, len(evs), err)
		}
		if err := worker.Deliver(ctx, evs[0]); err != nil {
			t.Fatalf("deliver %q: %v", path, err)
		}
	}

	deliver(tenantID.String() + "/" + collection + "/" + key)
	if state := mustObjectState(t, h.PoolMigrate, objectID); state != "PENDING" {
		t.Fatalf("state = %s after an event at the collection spelled over segments, want PENDING", state)
	}
	path, err := storagepath.Compose(tenantID, collection, key)
	if err != nil {
		t.Fatal(err)
	}
	deliver(path)
	if state := mustObjectState(t, h.PoolMigrate, objectID); state != "AVAILABLE" {
		t.Errorf("state = %s after an event at the composed path, want AVAILABLE", state)
	}
}

//go:build integration

package components

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The sequencer a storage event carries for an overwrite in this test.
const overwriteSequencer = "0000000000000002"

// recordingCopier copies through next and records the keys it copied out of
// one bucket: the rollback's copies back, when that bucket is the target.
type recordingCopier struct {
	next worker.ObjectCopier
	from string
	mu   sync.Mutex
	keys []string
}

func (c *recordingCopier) CopyObject(ctx context.Context, src, dst worker.CopyLocation) error {
	if err := c.next.CopyObject(ctx, src, dst); err != nil {
		return err
	}
	if src.Bucket == c.from {
		c.mu.Lock()
		c.keys = append(c.keys, src.Collection+"/"+src.Key)
		c.mu.Unlock()
	}
	return nil
}

func (c *recordingCopier) copied() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := slices.Clone(c.keys)
	slices.Sort(out)
	return out
}

// promotedUpload is an upload as the API makes one: a PENDING row, its bytes
// in the bucket the collection is bound to now, and the promote that confirms
// them. It reports a failure with Errorf, so it may run off the test's
// goroutine.
func (f publicMigrationFixture) promotedUpload(t *testing.T, ctx context.Context, collection, key string, size int64) {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
		SELECT $1, c.id, $3, 'PENDING', 'text/plain', $4 FROM collections c
		 WHERE c.tenant_id = $1 AND c.name = $2 RETURNING id`, f.tenant, collection, key, size).Scan(&id); err != nil {
		t.Errorf("register %s/%s: %v", collection, key, err)
		return
	}
	f.store.put(f.loc(f.boundTo(t, ctx, collection), collection, key), size)
	if changed, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, "", size, "", "", statemachine.SourceRPC); err != nil || !changed {
		t.Errorf("promote %s/%s = %v, %v; want promoted", collection, key, changed, err)
	}
}

// A rollback used to copy back every object whose row changed after the
// rebind: a tag or a taint was enough to copy the object's target copy over
// its source copy, though its bytes never moved — and that target copy may be
// the one the verify failed on. It now copies back the objects whose bytes
// were written after the rebind: a new upload and a storage-event overwrite,
// not a tag or a taint.
func TestStorageMigrationRollbackCopiesBackOnlyBytesWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.promotedUpload(t, ctx, "docs", "tagged", 6)
	f.promotedUpload(t, ctx, "docs", "overwritten", 8)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	copier := &recordingCopier{
		next: &corruptingCopier{fakeObjectStore: f.store, target: f.loc(f.dedicated, "docs", "obj-1")},
		from: f.dedicated,
	}
	header := &hookedHeader{fakeObjectStore: f.store, hook: func() {
		// Rebound: the tenant writes to the dedicated bucket now.
		if _, err := f.pool.Exec(ctx, `UPDATE objects o SET tags = '{"k": "v"}', taint = ARRAY['pii']
			FROM collections c WHERE c.id = o.collection_id
			  AND o.tenant_id = $1 AND c.name = 'docs' AND o.path = 'tagged'`, f.tenant); err != nil {
			t.Errorf("tag: %v", err)
		}
		f.promotedUpload(t, ctx, "docs", "post-1", 7)
		// Overwritten straight in the bucket; the storage event reports it.
		f.store.put(f.loc(f.dedicated, "docs", "overwritten"), 11)
		id := f.objectID(t, ctx, "docs", "overwritten")
		if changed, err := statemachine.New(f.pool).PromoteToAvailable(ctx, id, "etag-2", 11, "", overwriteSequencer, statemachine.SourceEvent); err != nil || !changed {
			t.Errorf("overwrite promote = %v, %v; want applied", changed, err)
		}
	}}

	state, reason := f.driveWith(t, ctx, copier, header)
	if state != worker.MigStateFailed || !strings.Contains(reason, "physical verify") {
		t.Fatalf("migration left at %s (%q), want failed with the verify error", state, reason)
	}
	f.assertOnSource(t, ctx, "docs")
	if got, want := copier.copied(), []string{"docs/overwritten", "docs/post-1"}; !slices.Equal(got, want) {
		t.Errorf("rollback copied back %v, want %v: the bytes written since the rebind, not a tag or a taint", got, want)
	}
	for key, want := range map[string]int64{"tagged": 6, "overwritten": 11, "post-1": 7, "obj-1": 42} {
		if size, ok := f.store.sizeOf(f.loc(f.private, "docs", key)); !ok || size != want {
			t.Errorf("source docs/%s: size %d, present %v; want %d bytes", key, size, ok, want)
		}
	}
}

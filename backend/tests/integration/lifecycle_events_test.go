//go:build integration

// Producer-wiring coverage for the lifecycle handler classes beyond tenant
// (which tenant_events_test.go already pins). Same seam, same contract: a
// successful handler write must fan a CloudEvents envelope out through the
// outbox → dispatcher → NATS. Each test mirrors the tenant template — seed
// the resource, attach the dispatcher as EventProducer, run the lifecycle
// method, assert the outbox row landed and the envelope reached NATS with the
// expected `type`.
//
// Closes the "producer wiring — integration tests for bucket / collection /
// quota / object lifecycle handlers" BACKLOG follow-up.
package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	objectkeypkg "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	quotahpkg "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/quotah"
	objectpkg "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// lifecycleEventProbe wires an embedded NATS subscriber + a dispatcher whose
// outbox loop drains to it, returns the live dispatcher (already as the
// handler's would-be EventProducer) and the received-message channel.
type lifecycleEventProbe struct {
	d   *worker.Dispatcher
	got chan *nats.Msg
}

func newLifecycleEventProbe(t *testing.T, f *dispatcherFixture, tenant, url string) *lifecycleEventProbe {
	t.Helper()
	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	t.Cleanup(pc.Close)
	got := make(chan *nats.Msg, 1)
	if _, err := pc.Subscribe("paladin.events", func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	pool := worker.NewNatsConnPool(nil)
	t.Cleanup(pool.Close)
	d := f.dispatcher()
	d.NATS = pool
	return &lifecycleEventProbe{d: d, got: got}
}

// expect drains the outbox once and asserts a single CloudEvents 1.0 envelope
// with the wanted `type` + tenantid reached NATS — the producer-wiring
// contract (handler called Dispatch after its write).
func (p *lifecycleEventProbe) expect(t *testing.T, f *dispatcherFixture, tenant, wantType string) {
	t.Helper()
	r := f.outboxRunner(p.d)
	if n := f.tickOnce(t, r); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}
	select {
	case m := <-p.got:
		var env struct {
			SpecVersion string `json:"specversion"`
			Type        string `json:"type"`
			TenantID    string `json:"tenantid"`
			Subject     string `json:"subject"`
		}
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("envelope unmarshal: %v (raw: %s)", err, string(m.Data))
		}
		if env.SpecVersion != "1.0" {
			t.Errorf("specversion = %q, want 1.0", env.SpecVersion)
		}
		if env.Type != wantType {
			t.Errorf("type = %q, want %q", env.Type, wantType)
		}
		if env.TenantID != tenant {
			t.Errorf("tenantid = %q, want %q", env.TenantID, tenant)
		}
		if env.Subject == "" {
			t.Error("subject empty — resource_name should be stamped for routing")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("nats: no %q within 2s — producer wired but delivery short-circuited", wantType)
	}
}

// TestQuotaHandler_SetQuotaDispatches pins quotah → paladin.quota.set.
func TestQuotaHandler_SetQuotaDispatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "quota-events")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewQuotaRepoV2(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := quotahpkg.NewHandler(repo, allowAll{})
	handler.SetEventProducer(probe.d)

	if _, err := handler.SetQuota(ctxAdmin(t, tenant), admindomain.Quota{
		TenantID:      tenant,
		MaxTotalBytes: 1 << 30,
	}, []string{"max_total_bytes"}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1 (SetEventProducer wiring)", len(rows))
	}
	probe.expect(t, f, tenant.String(), "paladin.quota.set")
}

// TestCollectionHandler_UpdateDispatches pins collection → paladin.collection.updated.
func TestCollectionHandler_UpdateDispatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "objkey-events")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "docs")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewCollectionRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := objectkeypkg.NewHandler(repo, allowAll{})
	handler.SetEventProducer(probe.d)

	newName := "Docs Renamed"
	if _, err := handler.UpdateCollection(ctxAdmin(t, tenant), objectkeypkg.UpdateCollectionArgs{
		Collection:      "docs",
		ExpectedVersion: 1,
		DisplayName:     &newName,
	}); err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1 (SetEventProducer wiring)", len(rows))
	}
	probe.expect(t, f, tenant.String(), "paladin.collection.updated")
}

// TestBucketHandler_UpdateDispatches pins bucketh → paladin.bucket.updated. The
// bucket must be owned by the tenant (owner_tenant_id) so the event fans out
// to the tenant's subscription.
func TestBucketHandler_UpdateDispatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bucket-events")
	const backendID, bucketName = "primary", "paladin-test"
	seedOwnedBucket(t, f.h.PoolMigrate, tenant, backendID, bucketName)
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewBucketRepoV2(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := bucketh.NewHandler(repo, nil, allowAll{})
	handler.SetEventProducer(probe.d)

	if _, err := handler.UpdateBucket(ctxAdmin(t, tenant), bucketh.UpdateBucketInput{
		Bucket: admindomain.Bucket{
			BackendID:     backendID,
			BucketName:    bucketName,
			OwnerTenantID: tenant,
			DisplayName:   "Bucket Renamed",
		},
		ExpectedVersion: 1,
		UpdateMask:      []string{"display_name"},
	}); err != nil {
		t.Fatalf("UpdateBucket: %v", err)
	}

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1 (SetEventProducer wiring)", len(rows))
	}
	probe.expect(t, f, tenant.String(), "paladin.bucket.updated")
}

// TestObjectHandler_UpdateDispatches pins object → paladin.object.updated.
// UpdateObject only touches the repo + dispatch, so storage / filter / sm
// are nil here.
func TestObjectHandler_UpdateDispatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "object-events")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "docs")
	objID := mustInsertAvailableObject(t, f.h.PoolMigrate, tenant, "docs", "mykey")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewObjectRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := objectpkg.NewHandler(repo, nil, allowAll{}, nil, nil, objectpkg.PresignConfig{})
	handler.SetEventProducer(probe.d)

	if _, err := handler.UpdateObject(ctxAdmin(t, tenant), objectpkg.UpdateObjectInput{
		Collection:      "docs",
		ObjectID:        objID.String(),
		ResourceVersion: 1, // fresh object → v1 (this path enforces OCC, no 0-skip)
		UpdatedFields:   []string{"tags"},
		Tags:            map[string]string{"env": "test"},
	}); err != nil {
		t.Fatalf("UpdateObject: %v", err)
	}

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1 (SetEventProducer wiring)", len(rows))
	}
	probe.expect(t, f, tenant.String(), "paladin.object.updated")
}

// seedOwnedBucket inserts a storage backend + a bucket owned by tenant.
func seedOwnedBucket(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID, backendID, bucketName string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO storage_backends (name, kind, region, endpoint)
        VALUES ($1, 's3-compatible', 'us-east-1', 'http://localhost')
        ON CONFLICT (name) DO NOTHING`, backendID); err != nil {
		t.Fatalf("seed backend: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO buckets (backend_id, name, owner_tenant_id)
        SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`,
		backendID, bucketName, tenant); err != nil {
		t.Fatalf("seed owned bucket: %v", err)
	}
}

// deleteOnlyStorage answers the one storage call a permanent delete makes.
// Everything else would panic, which keeps the test honest about the path it
// claims to exercise.
type deleteOnlyStorage struct {
	objectpkg.Storage
	deleted int
}

func (s *deleteOnlyStorage) DeleteObject(context.Context, string, string, uuid.UUID, string, string) error {
	s.deleted++
	return nil
}

// A permanent delete emits TWO events, and they are the ones an operator is
// most likely to be subscribed to. Both were tested only as far as the outbox
// — components/permanent_delete_test.go counts event_deliveries
// rows — which proves they were enqueued, not that anything can receive them.
// The join between "the handler enqueued it" and "a subscription matched and
// the runner delivered it" is where an event type can quietly stop matching
// the filter it was written for.
//
// The two events are also emitted from different places for different
// reasons: `deleted` rides the row-removal transaction (ADR-0003, so a crash
// cannot drop it), while `purged` fires only after the storage bytes are
// actually reclaimed. A test that saw one and not the other would look fine.
func TestObjectHandler_PermanentDeleteDispatchesDeletedAndPurged(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "object-perm-delete")
	mustCreateCollection(t, f.h.PoolMigrate, tenant, "docs")
	objID := mustInsertAvailableObject(t, f.h.PoolMigrate, tenant, "docs", "gone.txt")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	// The shared probe is used for its DISPATCHER (it wires the NATS pool the
	// runner publishes through); its one-message channel is not what this test
	// reads, because a permanent delete emits two events.
	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	multi := subscribeAll(t, url, "paladin.events")
	repo := adapters.NewObjectRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	storage := &deleteOnlyStorage{}
	handler := objectpkg.NewHandler(repo, storage, allowAll{}, nil,
		statemachine.New(f.h.PoolMigrate), objectpkg.PresignConfig{})
	handler.SetEventProducer(probe.d)

	ctx := ctxAdmin(t, tenant)
	obj, err := repo.FindByName(ctx, tenant, "docs", objID.String())
	if err != nil {
		t.Fatalf("read the object back: %v", err)
	}

	if err := handler.PermanentDelete(ctx, tenant, obj, obj.ResourceVersion, false); err != nil {
		t.Fatalf("PermanentDelete: %v", err)
	}
	if storage.deleted != 1 {
		t.Fatalf("storage deletes = %d, want 1", storage.deleted)
	}

	// One tick drains both rows, so the shared probe (which asserts exactly
	// one delivery and buffers one message) cannot be reused here.
	r := f.outboxRunner(probe.d)
	if n := f.tickOnce(t, r); n != 2 {
		t.Fatalf("tick processed = %d, want 2 — a permanent delete owes subscribers both facts", n)
	}

	got := map[string]bool{}
	for range 2 {
		select {
		case m := <-multi:
			var env struct {
				Type     string `json:"type"`
				TenantID string `json:"tenantid"`
			}
			if err := json.Unmarshal(m.Data, &env); err != nil {
				t.Fatalf("envelope unmarshal: %v (raw: %s)", err, string(m.Data))
			}
			if env.TenantID != tenant.String() {
				t.Errorf("tenantid = %q, want %q", env.TenantID, tenant)
			}
			got[env.Type] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for a second event; got %v", got)
		}
	}
	for _, want := range []string{"paladin.object.deleted", "paladin.object.purged"} {
		if !got[want] {
			t.Errorf("%s never reached the subscriber; got %v", want, got)
		}
	}
}

// subscribeAll is newLifecycleEventProbe's subscription with room for more
// than one message. The shared probe buffers a single message because every
// other lifecycle test emits exactly one event; a permanent delete emits two,
// and a one-slot channel would drop the second and fail for the wrong reason.
func subscribeAll(t *testing.T, url, subject string) <-chan *nats.Msg {
	t.Helper()
	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	t.Cleanup(pc.Close)
	got := make(chan *nats.Msg, 8)
	if _, err := pc.Subscribe(subject, func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return got
}

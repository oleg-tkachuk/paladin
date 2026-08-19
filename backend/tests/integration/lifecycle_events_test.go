//go:build integration

// Producer-wiring coverage for the lifecycle handler classes beyond tenant
// (which tenant_events_test.go already pins). Same seam, same contract: a
// successful handler write must fan a CloudEvents envelope out through the
// outbox → dispatcher → NATS. Each test mirrors the tenant template — seed
// the resource, attach the dispatcher as EventProducer, run the lifecycle
// method, assert the outbox row landed and the envelope reached NATS with the
// expected `type`.
//
// Closes the "producer wiring — integration tests for bucket / object_key /
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

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/bucketh"
	quotahpkg "github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/quotah"
	objectpkg "github.com/oleg-tkachuk/paladin-private/internal/api/v1/object"
	objectkeypkg "github.com/oleg-tkachuk/paladin-private/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin-private/internal/worker"
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

// TestObjectKeyHandler_UpdateDispatches pins object_key → paladin.object_key.updated.
func TestObjectKeyHandler_UpdateDispatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "objkey-events")
	mustCreateObjectKey(t, f.h.PoolMigrate, tenant, "docs")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewObjectKeyRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := objectkeypkg.NewHandler(repo, allowAll{})
	handler.SetEventProducer(probe.d)

	newName := "Docs Renamed"
	if _, err := handler.UpdateObjectKey(ctxAdmin(t, tenant), objectkeypkg.UpdateObjectKeyArgs{
		ObjectKey:       "docs",
		ExpectedVersion: 1,
		DisplayName:     &newName,
	}); err != nil {
		t.Fatalf("UpdateObjectKey: %v", err)
	}

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1 (SetEventProducer wiring)", len(rows))
	}
	probe.expect(t, f, tenant.String(), "paladin.object_key.updated")
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
	mustCreateObjectKey(t, f.h.PoolMigrate, tenant, "docs")
	objID := mustInsertAvailableObject(t, f.h.PoolMigrate, tenant, "docs", "mykey")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	repo := adapters.NewObjectRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := objectpkg.NewHandler(repo, nil, allowAll{}, nil, nil, objectpkg.PresignConfig{})
	handler.SetEventProducer(probe.d)

	if _, err := handler.UpdateObject(ctxAdmin(t, tenant), objectpkg.UpdateObjectInput{
		ObjectKey:       "docs",
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
        INSERT INTO storage_backends (id, kind, region, endpoint)
        VALUES ($1, 's3-compatible', 'us-east-1', 'http://localhost')
        ON CONFLICT (id) DO NOTHING`, backendID); err != nil {
		t.Fatalf("seed backend: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO buckets (backend_id, bucket_name, owner_tenant_id)
        VALUES ($1, $2, $3)`, backendID, bucketName, tenant); err != nil {
		t.Fatalf("seed owned bucket: %v", err)
	}
}

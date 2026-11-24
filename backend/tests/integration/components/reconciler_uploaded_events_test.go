//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// uploadedFilter is the webhook subscription's filter: uploads only.
const uploadedFilter = `type == "` + eventtype.ObjectUploaded + `"`

// The keys of the subscriber's copy of an event the tests read.
const (
	deliveredSource = "source"
	deliveredObject = "object_id"
)

// subscribeToUploads gives the tenant a webhook subscription to uploads, as
// an operator's CreateEventSubscription does.
func (f publicMigrationFixture) subscribeToUploads(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	mustExec(t, ctx, f.pool, `INSERT INTO event_subscriptions (id, tenant_id, cel_filter, sink_kind, sink_config)
		VALUES ($1, $2, $3, 'http', '{}'::jsonb)`, id, f.tenant, uploadedFilter)
	return id
}

// delivered fans the recorded events out, as the dispatcher pod does, and
// returns how many upload deliveries the subscription holds for id and the
// source the latest names.
func (f publicMigrationFixture) delivered(t *testing.T, ctx context.Context, sub, id uuid.UUID) (int, string) {
	t.Helper()
	if _, err := (&worker.OutboxRunner{Pool: f.pool, Dispatcher: &worker.Dispatcher{}}).FanOut(ctx); err != nil {
		t.Fatalf("fan out: %v", err)
	}
	var n int
	var source *string
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*), (array_agg(event_payload->'payload'->>$3::text ORDER BY event_at DESC))[1]
		  FROM event_deliveries
		 WHERE subscription_id = $1 AND event_type = $2
		   AND event_payload->'payload'->>$4::text = $5`,
		sub, eventtype.ObjectUploaded, deliveredSource, deliveredObject, id.String()).Scan(&n, &source); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if source == nil {
		return n, ""
	}
	return n, *source
}

// announcingReconciler is the reconciler over the fake store, wired with the
// event recorder as the worker wires it; head, when set, runs before each
// HEAD, as a request racing the tick would.
func (f publicMigrationFixture) announcingReconciler(head func()) *worker.ReconcilerV2 {
	var probe worker.StorageProbe = adapters.NewReconcilerProbe(sqlc.New(f.pool), contentHead{s: f.store})
	if head != nil {
		probe = racingProbe{StorageProbe: probe, before: head}
	}
	return worker.NewReconcilerV2(statemachine.New(f.pool), probe, worker.ReconcilerV2Config{
		PendingGraceTTL: time.Second, SettleGrace: time.Second,
		Events: worker.NewEventRecorder(zap.NewNop()),
	}, zap.NewNop())
}

// racingProbe runs before ahead of each HEAD.
type racingProbe struct {
	worker.StorageProbe
	before func()
}

func (p racingProbe) HeadByObjectID(ctx context.Context, id uuid.UUID) (string, int64, string, string, statemachine.Placement, bool, error) {
	p.before()
	return p.StorageProbe.HeadByObjectID(ctx, id)
}

// completeAnnouncing promotes id with the bytes in bucket and records the
// event on the promote, as CompleteObject does.
func (f publicMigrationFixture) completeAnnouncing(t *testing.T, ctx context.Context, id uuid.UUID, bucket, etag string) {
	t.Helper()
	rec := worker.NewEventRecorder(zap.NewNop())
	changed, err := statemachine.New(f.pool).PromoteToAvailableInTx(ctx, id,
		&statemachine.Placement{BackendID: f.backend, Bucket: bucket}, etag, lateSize, "", "", statemachine.SourceRPC,
		func(ctx context.Context, tx pgx.Tx) error {
			return rec.RecordTx(ctx, tx, f.tenant.String(), worker.Event{
				Type: eventtype.ObjectUploaded, At: time.Now(), TenantID: f.tenant.String(),
				Payload: map[string]any{
					eventtype.PayloadObjectID: id.String(),
					eventtype.PayloadSource:   string(statemachine.SourceRPC),
				},
			})
		})
	if err != nil || !changed {
		t.Fatalf("complete: changed %v, %v", changed, err)
	}
}

func runTick(t *testing.T, ctx context.Context, r *worker.ReconcilerV2) {
	t.Helper()
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// A webhook subscriber learns of every write the reconciler records, once,
// and of nothing it only confirms (ADR-0051): its promote of an upload whose
// completion never came is announced, its settle of a write no storage event
// reported is announced, and a settle that finds the bytes the row records is
// not.
func TestReconcilerAnnouncesTheWritesItRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	sub := f.subscribeToUploads(t, ctx)
	reconciler := f.announcingReconciler(nil)

	// Uploaded, never completed, its storage event lost.
	lost := f.registerUpload(t, ctx, "lost.bin", f.private)
	f.putThroughURL(t, ctx, "lost.bin", f.private)
	// Completed, then written again through the same URL, that event lost.
	rewritten := f.registerUpload(t, ctx, lateKey, f.private)
	f.completeAnnouncing(t, ctx, rewritten, f.private, f.putThroughURL(t, ctx, lateKey, f.private))
	f.putThroughURL(t, ctx, lateKey, f.private)
	// Completed, and nothing written since.
	completed := f.registerUpload(t, ctx, "done.bin", f.private)
	f.completeAnnouncing(t, ctx, completed, f.private, f.putThroughURL(t, ctx, "done.bin", f.private))
	f.expireURLs(t, ctx)

	// The first tick promotes the lost upload and settles both completed.
	runTick(t, ctx, reconciler)
	reconciled := string(statemachine.SourceReconciler)
	if n, source := f.delivered(t, ctx, sub, lost); n != 1 || source != reconciled {
		t.Errorf("the reconciler's promote reached the subscriber %d times (last from %q), want once, its own", n, source)
	}
	if n, source := f.delivered(t, ctx, sub, rewritten); n != 2 || source != reconciled {
		t.Errorf("the completion and the settled rewrite reached it %d times (last from %q), want twice, the last the reconciler's", n, source)
	}
	if n, _ := f.delivered(t, ctx, sub, completed); n != 1 {
		t.Errorf("a settle that changed nothing: %d deliveries in all, want the completion's one", n)
	}

	// The next tick settles the promoted upload, whose bytes are unchanged:
	// nothing more to announce.
	runTick(t, ctx, reconciler)
	for id, want := range map[uuid.UUID]int{lost: 1, rewritten: 2, completed: 1} {
		if n, _ := f.delivered(t, ctx, sub, id); n != want {
			t.Errorf("object %s: %d deliveries after a settle that changed nothing, want %d", id, n, want)
		}
	}
	if r := f.row(t, ctx, lost); r.anchored {
		t.Error("the promoted upload's anchor was not settled by the second tick")
	}
}

// The reconciler's event says what CompleteObject's says: the object, its
// bytes and the canonical name the data plane gives it.
func TestReconcilerAnnouncesTheObjectAsCompleteObjectDoes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	id := f.registerUpload(t, ctx, "lost.bin", f.private)
	etag := f.putThroughURL(t, ctx, "lost.bin", f.private)
	f.expireURLs(t, ctx)
	runTick(t, ctx, f.announcingReconciler(nil))

	var evt worker.Event
	if err := f.pool.QueryRow(ctx, `SELECT event_payload FROM event_outbox WHERE tenant_id = $1 AND event_type = $2`,
		f.tenant, eventtype.ObjectUploaded).Scan(&evt); err != nil {
		t.Fatalf("read the event: %v", err)
	}
	wantName := "storageBackends/" + f.backend + "/buckets/" + f.private + "/tenants/" + f.tenant.String() +
		"/collections/docs/objects-by-key/lost.bin"
	if evt.ResourceName != wantName || evt.TenantID != f.tenant.String() {
		t.Errorf("event names %q in tenant %q, want %q", evt.ResourceName, evt.TenantID, wantName)
	}
	p := evt.Payload
	if p[eventtype.PayloadObjectID] != id.String() || p[eventtype.PayloadCollection] != "docs" ||
		p[eventtype.PayloadKey] != "lost.bin" || p[eventtype.PayloadETag] != etag ||
		p[eventtype.PayloadSizeBytes] != float64(lateSize) || p[eventtype.PayloadContentType] != "text/plain" {
		t.Errorf("payload = %v, want the object, its bytes and its content type", p)
	}
}

// Exactly once across producers: a CompleteObject that promotes the upload
// while the reconciler is HEADing it wins the row, and the reconciler's own
// promote then changes nothing and announces nothing.
func TestReconcilerDoesNotAnnounceAPromoteAnotherProducerMade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	sub := f.subscribeToUploads(t, ctx)
	id := f.registerUpload(t, ctx, "raced.bin", f.private)
	etag := f.putThroughURL(t, ctx, "raced.bin", f.private)
	f.expireURLs(t, ctx)

	completedFirst := false
	runTick(t, ctx, f.announcingReconciler(func() {
		if !completedFirst {
			completedFirst = true
			f.completeAnnouncing(t, ctx, id, f.private, etag)
		}
	}))
	if !completedFirst {
		t.Fatal("the reconciler never HEADed the upload")
	}
	if n, source := f.delivered(t, ctx, sub, id); n != 1 || source != string(statemachine.SourceRPC) {
		t.Errorf("the upload reached the subscriber %d times (last from %q), want once, from the completion", n, source)
	}
}

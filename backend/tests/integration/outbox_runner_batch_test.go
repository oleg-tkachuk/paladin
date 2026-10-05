//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The tick sends rows bound for one SQS queue or one Kafka topic as a single
// batch, and writes each row's outcome back from the batch's result. These
// tests hold that routing and that bookkeeping without a broker: a failing
// batch has to leave its rows pending, and a row for another kind of sink has
// to stay out of the batch even when the batch pools are there.
//
// Not held, deliberately: whether a NATS or Kafka row is batched only when the
// dispatcher has that sink's pool. Without the pool both paths fail the row
// with the same error, and with it both deliver it, so only the transport shape
// — one flush or write against one per row — could tell them apart. SQS is
// told apart by its error, which names SendMessageBatch or SendMessage.

// seedSinkSubscription inserts an enabled subscription of any sink kind.
func (f *dispatcherFixture) seedSinkSubscription(t *testing.T, tenant uuid.UUID, kind string, cfg map[string]any) uuid.UUID {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal sink config: %v", err)
	}
	id := uuid.New()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_subscriptions (id, tenant_id, cel_filter, sink_kind, sink_config)
		 VALUES ($1, $2, '', $3, $4)`,
		id, tenant, kind, raw,
	); err != nil {
		t.Fatalf("seed %s subscription: %v", kind, err)
	}
	return id
}

// closedAddr is a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

// batchingDispatcher has every batchable sink's pool, so each batch path is
// open to a row that wrongly takes it.
func (f *dispatcherFixture) batchingDispatcher() *worker.Dispatcher {
	d := f.dispatcher()
	d.SQS = worker.NewSQSClientPool(nil)
	d.Kafka = worker.NewKafkaWriterPool(nil)
	return d
}

func TestOutboxRunner_HTTPRowStaysOutOfTheBatches(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	rec := newRecorder(http.StatusOK)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-http-unbatched")
	f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	d := f.batchingDispatcher()
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	f.tickOnce(t, f.outboxRunner(d))

	if got := f.allDeliveryRows(t, tenant)[0].Status; got != "delivered" {
		t.Errorf("status = %q, want delivered", got)
	}
	if rec.count() != 1 {
		t.Errorf("sink saw %d requests, want 1", rec.count())
	}
}

// failedBatchRow ticks one row for sub and checks the batch's failure was
// written back to it as a retry.
func (f *dispatcherFixture) failedBatchRow(t *testing.T, tenant uuid.UUID, d *worker.Dispatcher, wantErr string) {
	t.Helper()
	if _, err := d.Dispatch(context.Background(), tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	f.tickOnce(t, f.outboxRunner(d))

	row := f.allDeliveryRows(t, tenant)[0]
	if row.Status != "pending" || row.Attempts != 1 {
		t.Fatalf("status=%q attempts=%d, want pending/1", row.Status, row.Attempts)
	}
	if !strings.Contains(row.LastError, wantErr) {
		t.Errorf("last_error = %q, want it to contain %q", row.LastError, wantErr)
	}
}

func TestOutboxRunner_FailedKafkaBatchLeavesItsRowsPending(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-kafka-batch")
	// Well-formed enough to be batched; the batch then fails building its
	// transport, before anything is dialled.
	const mechanism = "not-a-mechanism"
	f.seedSinkSubscription(t, tenant, "kafka", map[string]any{
		"brokers":        closedAddr(t),
		"topic":          "paladin-events",
		"sasl_mechanism": mechanism,
	})
	f.failedBatchRow(t, tenant, f.batchingDispatcher(), mechanism)
}

// Not parallel: it points the AWS SDK at a closed port through the
// environment, which t.Setenv only allows in a sequential test.
func TestOutboxRunner_FailedSQSBatchLeavesItsRowsPending(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL", "http://"+closedAddr(t))
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	// One attempt, so the refused connection fails the batch at once rather
	// than after the SDK's retries.
	t.Setenv("AWS_MAX_ATTEMPTS", "1")

	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "outbox-sqs-batch")
	f.seedSinkSubscription(t, tenant, "sqs", map[string]any{
		"queue_url": "http://" + closedAddr(t) + "/000000000000/paladin-events",
		"region":    "us-east-1",
	})
	// Refused by the closed port: the client went where the environment
	// pointed it, not to AWS.
	f.failedBatchRow(t, tenant, f.batchingDispatcher(), "connection refused")
}

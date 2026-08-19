//go:build integration

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
)

// TestSQSSinkBatchDelivery_Wire pins the SendMessageBatch fan-in against a
// real elasticmq: 12 same-queue rows go out via the chunked batch path
// (10+2) and all 12 land as valid CloudEvents envelopes. Lives in package
// worker (unlike its siblings in internal/integration) because it exercises
// the unexported batch surface directly.
func TestSQSSinkBatchDelivery_Wire(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "softwaremill/elasticmq-native:1.6.11",
			ExposedPorts: []string{"9324/tcp"},
			WaitingFor:   wait.ForListeningPort("9324/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start elasticmq: %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(ctr) }()
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "9324/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("elasticmq"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("x", "x", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := awssqs.NewFromConfig(awsCfg, func(o *awssqs.Options) { o.BaseEndpoint = aws.String(endpoint) })
	created, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("paladin-events-batch-it")})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}

	t.Setenv("AWS_ACCESS_KEY_ID", "x")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "x")
	t.Setenv("AWS_REGION", "elasticmq")
	t.Setenv("AWS_ENDPOINT_URL", endpoint)

	d := &Dispatcher{SQS: NewSQSClientPool(nil), MaxAttempts: 1}
	cfg := sqsSinkConfig{QueueURL: *created.QueueUrl, Region: "elasticmq"}
	raw, _ := json.Marshal(cfg)
	const n = 12
	items := make([]sqsBatchItem, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.Must(uuid.NewV7())
		items = append(items, sqsBatchItem{
			RowID: id,
			Sub: admindomain.EventSubscription{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       uuid.Must(uuid.NewV7()),
				SinkKind:       "sqs",
				SinkConfig:     raw,
			},
			Evt: Event{Type: "paladin.bucket.updated", TenantID: "tenant-123", ID: id.String()},
		})
	}
	for id, derr := range d.deliverSQSBatch(ctx, cfg, items) {
		if derr != nil {
			t.Fatalf("row %s failed: %v", id, derr)
		}
	}

	got := 0
	deadline := time.Now().Add(20 * time.Second)
	for got < n && time.Now().Before(deadline) {
		resp, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:            created.QueueUrl,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     2,
		})
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		for _, m := range resp.Messages {
			var env struct {
				SpecVersion string `json:"specversion"`
				Type        string `json:"type"`
			}
			if err := json.Unmarshal([]byte(*m.Body), &env); err != nil || env.SpecVersion != "1.0" || env.Type != "paladin.bucket.updated" {
				t.Fatalf("bad envelope: %v (%q)", err, *m.Body)
			}
			got++
		}
	}
	if got != n {
		t.Fatalf("received %d messages, want %d", got, n)
	}
}

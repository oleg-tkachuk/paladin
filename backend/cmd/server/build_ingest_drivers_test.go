package main

import (
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/eventingest"
)

// The SQS driver and pickSource already have tests; NATS, RabbitMQ and the
// webhook driver did not. Same construction path, same class of mistake —
// a missing required field or a source-format typo becomes a pod that starts
// and then silently ingests nothing, so the validation is worth pinning.

func TestBuildNATSDriver(t *testing.T) {
	l := zap.NewNop()

	t.Run("url required", func(t *testing.T) {
		_, err := buildIngestDriver(config.Ingest{
			Driver: "nats",
			NATS:   config.IngestNATS{Subject: "s", SourceFormat: "seaweedfs_nats"},
		}, l)
		if err == nil {
			t.Fatal("expected an error for a missing nats.url")
		}
	})

	t.Run("valid config threads every field through", func(t *testing.T) {
		d, err := buildIngestDriver(config.Ingest{
			Driver: "nats",
			NATS: config.IngestNATS{
				URL: "nats://nats.local:4222", Subject: "seaweedfs.filer",
				QueueGroup: "paladin-ingest", JetStream: true,
				DurableName: "paladin-ingest-sf", SourceFormat: "seaweedfs_nats",
			},
		}, l)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		nd, ok := d.(*eventingest.NATSDriver)
		if !ok {
			t.Fatalf("got %T, want *NATSDriver", d)
		}
		// JetStream and DurableName together are what make delivery
		// at-least-once; a dropped field silently downgrades to core NATS.
		if !nd.JetStream || nd.DurableName != "paladin-ingest-sf" {
			t.Errorf("jetstream=%v durable=%q — at-least-once delivery depends on both",
				nd.JetStream, nd.DurableName)
		}
		if nd.QueueGroup != "paladin-ingest" {
			t.Errorf("QueueGroup = %q — without it every replica consumes every message",
				nd.QueueGroup)
		}
		if _, ok := nd.SourceAdapt.(*eventingest.SeaweedFSNATSSource); !ok {
			t.Errorf("source = %T, want *SeaweedFSNATSSource", nd.SourceAdapt)
		}
	})

	t.Run("unknown source_format is rejected", func(t *testing.T) {
		_, err := buildIngestDriver(config.Ingest{
			Driver: "nats",
			NATS:   config.IngestNATS{URL: "nats://x:4222", SourceFormat: "not-a-format"},
		}, l)
		if err == nil {
			t.Fatal("an unknown source_format must fail at build, not at first message")
		}
	})
}

func TestBuildRabbitMQDriver(t *testing.T) {
	l := zap.NewNop()

	// Both fields are required and the errors are distinct, so an operator
	// reading the message knows which one they forgot.
	for name, cfg := range map[string]config.IngestRabbitMQ{
		"url required":   {Queue: "q", SourceFormat: "s3"},
		"queue required": {URL: "amqp://mq:5672", SourceFormat: "s3"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildIngestDriver(config.Ingest{Driver: "rabbitmq", RabbitMQ: cfg}, l)
			if err == nil {
				t.Fatalf("expected an error: %s", name)
			}
		})
	}

	t.Run("valid config builds a rabbitmq driver", func(t *testing.T) {
		d, err := buildIngestDriver(config.Ingest{
			Driver: "rabbitmq",
			RabbitMQ: config.IngestRabbitMQ{
				URL: "amqp://mq.local:5672", Queue: "paladin-ingest",
				PrefetchCount: 32, SourceFormat: "s3",
			},
		}, l)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		rd, ok := d.(*eventingest.RabbitMQDriver)
		if !ok {
			t.Fatalf("got %T, want *RabbitMQDriver", d)
		}
		if rd.Queue != "paladin-ingest" || rd.PrefetchCount != 32 {
			t.Errorf("queue=%q prefetch=%d", rd.Queue, rd.PrefetchCount)
		}
	})

	t.Run("unknown source_format is rejected", func(t *testing.T) {
		_, err := buildIngestDriver(config.Ingest{
			Driver:   "rabbitmq",
			RabbitMQ: config.IngestRabbitMQ{URL: "amqp://x", Queue: "q", SourceFormat: "nope"},
		}, l)
		if err == nil {
			t.Fatal("expected an error for an unknown source_format")
		}
	})
}

func TestBuildWebhookDriver(t *testing.T) {
	l := zap.NewNop()

	d, err := buildIngestDriver(config.Ingest{
		Driver:  "webhook",
		Webhook: config.IngestWebhook{Addr: ":8100"},
	}, l)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if d == nil {
		t.Fatal("webhook driver must build without a broker — it is the no-broker path")
	}
}

// An unknown driver name must fail at startup. Falling through to a nil
// driver would leave ingest enabled and silently consuming nothing.
func TestBuildIngestDriverRejectsUnknownDriver(t *testing.T) {
	_, err := buildIngestDriver(config.Ingest{Driver: "kafka-ish"}, zap.NewNop())
	if err == nil {
		t.Fatal("an unknown driver name must be rejected at build time")
	}
}

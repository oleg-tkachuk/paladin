package main

import (
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/eventingest"
)

func TestBuildSQSDriver(t *testing.T) {
	l := zap.NewNop()

	t.Run("queue_url required", func(t *testing.T) {
		_, err := buildIngestDriver(config.Ingest{Driver: "sqs", SQS: config.IngestSQS{Region: "us-east-1"}}, l)
		if err == nil {
			t.Fatal("expected error for missing queue_url")
		}
	})

	t.Run("region required", func(t *testing.T) {
		_, err := buildIngestDriver(config.Ingest{Driver: "sqs", SQS: config.IngestSQS{QueueURL: "https://sqs.local/q"}}, l)
		if err == nil {
			t.Fatal("expected error for missing region")
		}
	})

	t.Run("valid config builds an sqs driver defaulting source to s3", func(t *testing.T) {
		d, err := buildIngestDriver(config.Ingest{
			Driver: "sqs",
			SQS: config.IngestSQS{
				QueueURL: "https://sqs.us-east-1.amazonaws.com/1/q",
				Region:   "us-east-1",
				Endpoint: "http://localhost:4566", // LocalStack — no AWS calls at build time
			},
		}, l)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		sd, ok := d.(*eventingest.SQSDriver)
		if !ok {
			t.Fatalf("got %T, want *SQSDriver", d)
		}
		if sd.Name() != "sqs" {
			t.Errorf("Name = %q", sd.Name())
		}
		if _, ok := sd.SourceAdapt.(*eventingest.S3EventSource); !ok {
			t.Errorf("default source = %T, want *S3EventSource", sd.SourceAdapt)
		}
	})

	t.Run("garage source_format is rejected even under the sqs driver", func(t *testing.T) {
		_, err := buildIngestDriver(config.Ingest{
			Driver: "sqs",
			SQS:    config.IngestSQS{QueueURL: "https://sqs.local/q", Region: "us-east-1", SourceFormat: "garage"},
		}, l)
		if err == nil {
			t.Fatal("garage source_format should be rejected")
		}
	})
}

package eventingest

import (
	"errors"
	"testing"
)

func TestMinIOSource_Parse_Created(t *testing.T) {
	src := &MinIOSource{URI: "minio://primary"}
	body := []byte(`{
		"EventName": "s3:ObjectCreated:Put",
		"Records": [{
			"eventName": "s3:ObjectCreated:Put",
			"eventTime": "2026-01-01T00:00:00Z",
			"s3": {
				"bucket": { "name": "paladin-primary" },
				"object": {
					"key":       "0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc%2Fcontracts%2F2026%2Fq1.pdf",
					"size":      1024,
					"eTag":      "abc",
					"sequencer": "001"
				}
			},
			"responseElements": { "x-amz-request-id": "req-1" }
		}]
	}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("type = %q, want paladin.object.uploaded", ev.Type)
	}
	if ev.SubjectFields.TenantID != "0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc" {
		t.Errorf("tenant = %q", ev.SubjectFields.TenantID)
	}
	if ev.SubjectFields.ObjectKey != "contracts" {
		t.Errorf("object_key = %q", ev.SubjectFields.ObjectKey)
	}
	if ev.SubjectFields.Key != "2026/q1.pdf" {
		t.Errorf("key (multi-segment) = %q", ev.SubjectFields.Key)
	}
	if ev.ID != "req-1" {
		t.Errorf("id = %q, want broker-supplied req-1", ev.ID)
	}
}

func TestMinIOSource_Parse_RemovedAndAccessVariants(t *testing.T) {
	src := &MinIOSource{URI: "minio://primary"}

	// All "Created:*" sub-actions collapse to uploaded.
	for _, n := range []string{
		"s3:ObjectCreated:Put",
		"s3:ObjectCreated:Post",
		"s3:ObjectCreated:Copy",
		"s3:ObjectCreated:CompleteMultipartUpload",
	} {
		body := []byte(`{"Records":[{"eventName":"` + n + `","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t%2Fok%2Fk"}}}]}`)
		ev, err := src.Parse(body, "")
		if err != nil {
			t.Fatalf("Parse(%s): %v", n, err)
		}
		if ev.Type != EventTypeUploaded {
			t.Errorf("Created variant %q: type = %q, want uploaded", n, ev.Type)
		}
	}

	// Removed:* collapses to deleted.
	for _, n := range []string{
		"s3:ObjectRemoved:Delete",
		"s3:ObjectRemoved:DeleteMarkerCreated",
	} {
		body := []byte(`{"Records":[{"eventName":"` + n + `","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t%2Fok%2Fk"}}}]}`)
		ev, err := src.Parse(body, "")
		if err != nil {
			t.Fatalf("Parse(%s): %v", n, err)
		}
		if ev.Type != EventTypeDeleted {
			t.Errorf("Removed variant %q: type = %q, want deleted", n, ev.Type)
		}
	}
}

func TestMinIOSource_Parse_OtherEventNamesIgnored(t *testing.T) {
	src := &MinIOSource{URI: "minio://primary"}
	body := []byte(`{"Records":[{"eventName":"s3:ObjectAccessed:Get","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t%2Fok%2Fk"}}}]}`)
	_, err := src.Parse(body, "")
	if !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("ObjectAccessed:Get should be ignored, got %v", err)
	}
}

func TestMinIOSource_Parse_BucketFilter(t *testing.T) {
	src := &MinIOSource{BucketName: "paladin-primary", URI: "minio://primary"}

	// Wrong bucket → ignored, not an error.
	body := []byte(`{"Records":[{"eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"some-other-bucket"},"object":{"key":"t%2Fok%2Fk"}}}]}`)
	if _, err := src.Parse(body, ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("non-matching bucket should be ignored, got %v", err)
	}
}

func TestMinIOSource_Parse_NonOCPLayoutIgnored(t *testing.T) {
	src := &MinIOSource{URI: "minio://primary"}
	body := []byte(`{"Records":[{"eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"random-flat-key"}}}]}`)
	if _, err := src.Parse(body, ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("non-PALADIN key shape should be ignored, got %v", err)
	}
}

func TestMinIOSource_Parse_EmptyRecords(t *testing.T) {
	src := &MinIOSource{URI: "minio://primary"}
	body := []byte(`{"Records":[]}`)
	if _, err := src.Parse(body, ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("empty Records should be unrecognised, got %v", err)
	}
}

func TestMinIOSource_Parse_FallbackIDStableAcrossReplays(t *testing.T) {
	// No x-amz-request-id → adapter hashes content. Same payload
	// twice = same id (so dedup works).
	src := &MinIOSource{URI: "minio://primary"}
	body := []byte(`{"Records":[{"eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t%2Fok%2Fk","sequencer":"X"}}}]}`)
	a, _ := src.Parse(body, "")
	b, _ := src.Parse(body, "")
	if a.ID == "" || a.ID != b.ID {
		t.Errorf("fallback id must be stable: a=%q b=%q", a.ID, b.ID)
	}
}

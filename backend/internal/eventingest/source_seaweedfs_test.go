package eventingest

import (
	"errors"
	"testing"
)

func TestSeaweedFSSource_Parse_Create(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	body := []byte(`{
		"key": "/0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/contracts/2026/q1.pdf",
		"event_type": "create",
		"timestamp_ns": 1700000000000000000,
		"etag": "abc123",
		"size": 1024
	}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("type = %q, want paladin.object.uploaded", ev.Type)
	}
	if ev.Source != "seaweedfs://primary" {
		t.Errorf("source = %q", ev.Source)
	}
	if ev.SubjectFields.TenantID != "0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc" {
		t.Errorf("tenant = %q", ev.SubjectFields.TenantID)
	}
	if ev.SubjectFields.ObjectKey != "contracts" {
		t.Errorf("object_key = %q", ev.SubjectFields.ObjectKey)
	}
	if ev.SubjectFields.Key != "2026/q1.pdf" {
		t.Errorf("key = %q", ev.SubjectFields.Key)
	}
	if ev.SubjectFields.Etag != "abc123" {
		t.Errorf("etag = %q", ev.SubjectFields.Etag)
	}
	if ev.SubjectFields.SizeBytes != 1024 {
		t.Errorf("size = %d", ev.SubjectFields.SizeBytes)
	}
	if ev.ID == "" {
		t.Error("ID empty — dedup would never work")
	}
}

func TestSeaweedFSSource_Parse_Delete(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	body := []byte(`{"key":"/t/ok/k","event_type":"delete","timestamp_ns":1}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeDeleted {
		t.Errorf("type = %q want paladin.object.deleted", ev.Type)
	}
}

func TestSeaweedFSSource_Parse_IgnoredEventType(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	body := []byte(`{"key":"/t/ok/k","event_type":"checksum","timestamp_ns":1}`)
	_, err := src.Parse(body, "application/json")
	if !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
}

func TestSeaweedFSSource_Parse_NonOCPPath(t *testing.T) {
	// Path doesn't have the 3-segment PALADIN layout — ignored not error.
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	body := []byte(`{"key":"/random/file.txt","event_type":"create","timestamp_ns":1}`)
	_, err := src.Parse(body, "application/json")
	if !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
}

func TestSeaweedFSSource_Parse_BucketPrefixStripped(t *testing.T) {
	src := &SeaweedFSSource{BucketName: "paladin-primary", URI: "seaweedfs://primary"}
	body := []byte(`{"key":"/paladin-primary/0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/ok/k","event_type":"create","timestamp_ns":1}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.SubjectFields.ObjectKey != "ok" {
		t.Errorf("object_key after bucket strip = %q", ev.SubjectFields.ObjectKey)
	}
}

func TestSeaweedFSSource_Parse_Update(t *testing.T) {
	// `update` maps to uploaded just like `create` — both re-emit a usable
	// etag/size for the same PromoteToAvailable transition.
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	body := []byte(`{"key":"/t/ok/k","event_type":"update","timestamp_ns":2}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("type = %q, want uploaded", ev.Type)
	}
}

func TestSeaweedFSSource_Parse_BucketsNamespacePrefix(t *testing.T) {
	// The S3 gateway materialises bucket-rooted paths under
	// `/buckets/<bucket>/...` in the filer namespace. The adapter strips the
	// `buckets/` prefix before the bucket-name check so both publishers parse.
	src := &SeaweedFSSource{BucketName: "paladin-primary", URI: "seaweedfs://primary"}
	body := []byte(`{"key":"/buckets/paladin-primary/0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/ok/deep/k","event_type":"create","timestamp_ns":1}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.SubjectFields.TenantID != "0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc" || ev.SubjectFields.ObjectKey != "ok" || ev.SubjectFields.Key != "deep/k" {
		t.Errorf("segments after buckets/ + bucket strip = %+v", ev.SubjectFields)
	}
}

func TestSeaweedFSSource_Parse_BadJSON(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	_, err := src.Parse([]byte("not json"), "application/json")
	if !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestSeaweedFSID_StableAcrossReplays(t *testing.T) {
	a := seaweedFSID("/t/ok/k", "create", 12345)
	b := seaweedFSID("/t/ok/k", "create", 12345)
	if a != b {
		t.Error("same logical event must produce the same id (dedup will fail)")
	}
	c := seaweedFSID("/t/ok/k", "create", 12346)
	if a == c {
		t.Error("different timestamp must produce different id")
	}
}

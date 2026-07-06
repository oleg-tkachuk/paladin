package eventingest

import (
	"errors"
	"testing"
)

// aws builds an AWS-S3-shaped single-record envelope. eventSource "aws:s3".
func awsEvent(eventName, bucket, key, reqID string) []byte {
	resp := ""
	if reqID != "" {
		resp = `,"responseElements":{"x-amz-request-id":"` + reqID + `"}`
	}
	return []byte(`{"Records":[{"eventSource":"aws:s3","eventName":"` + eventName +
		`","eventTime":"2026-01-02T03:04:05.678Z","s3":{"bucket":{"name":"` + bucket +
		`"},"object":{"key":"` + key + `","size":2048,"eTag":"etag-xyz","sequencer":"00SEQ01"}}` +
		resp + `}]}`)
}

func TestS3Source_AWSShape_CreatedPut(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	// AWS leaves structural slashes literal; space is form-encoded as '+'.
	ev, err := src.Parse(awsEvent("s3:ObjectCreated:Put", "paladin", "11111111-1111-4111-8111-111111111111/ok/red+flower.jpg", "req-1"), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("type = %q, want uploaded", ev.Type)
	}
	if ev.ID != "req-1" {
		t.Errorf("id = %q, want broker req-1", ev.ID)
	}
	if ev.SubjectFields.TenantID != "11111111-1111-4111-8111-111111111111" {
		t.Errorf("tenant = %q", ev.SubjectFields.TenantID)
	}
	if ev.SubjectFields.ObjectKey != "ok" {
		t.Errorf("objectKey = %q", ev.SubjectFields.ObjectKey)
	}
	if ev.SubjectFields.Key != "red flower.jpg" { // '+' decoded to space
		t.Errorf("key = %q, want 'red flower.jpg'", ev.SubjectFields.Key)
	}
	if ev.SubjectFields.Etag != "etag-xyz" || ev.SubjectFields.SizeBytes != 2048 || ev.SubjectFields.Sequencer != "00SEQ01" {
		t.Errorf("s3 metadata not propagated: %+v", ev.SubjectFields)
	}
	if ev.Time.IsZero() {
		t.Error("event time not parsed")
	}
}

func TestS3Source_MinIOShape_EncodedSlashes(t *testing.T) {
	src := &S3EventSource{URI: "minio://primary", Label: "minio"}
	// MinIO percent-encodes every slash, including the structural ones.
	body := []byte(`{"Records":[{"eventSource":"minio:s3","eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t%2Fok%2Fnested%2Fkey.bin"}},"responseElements":{"x-amz-request-id":"m-1"}}]}`)
	ev, err := src.Parse(body, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.SubjectFields.TenantID != "t" || ev.SubjectFields.ObjectKey != "ok" {
		t.Errorf("segments = %+v", ev.SubjectFields)
	}
	// Trailing slashes stay part of the key (SplitN keeps the remainder whole).
	if ev.SubjectFields.Key != "nested/key.bin" {
		t.Errorf("key = %q, want 'nested/key.bin'", ev.SubjectFields.Key)
	}
	if src.Name() != "minio" {
		t.Errorf("Name = %q, want minio", src.Name())
	}
}

func TestS3Source_DefaultNameIsS3(t *testing.T) {
	if (&S3EventSource{}).Name() != "s3" {
		t.Error("default Name should be s3")
	}
}

func TestS3Source_CreatedVariantsAllUploaded(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	for _, n := range []string{
		"s3:ObjectCreated:Put",
		"s3:ObjectCreated:Post",
		"s3:ObjectCreated:Copy",
		"s3:ObjectCreated:CompleteMultipartUpload",
	} {
		ev, err := src.Parse(awsEvent(n, "b", "t/ok/k", ""), "")
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if ev.Type != EventTypeUploaded {
			t.Errorf("%s: type = %q, want uploaded", n, ev.Type)
		}
	}
}

func TestS3Source_RemovedVariantsAllDeleted(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	for _, n := range []string{"s3:ObjectRemoved:Delete", "s3:ObjectRemoved:DeleteMarkerCreated"} {
		ev, err := src.Parse(awsEvent(n, "b", "t/ok/k", ""), "")
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if ev.Type != EventTypeDeleted {
			t.Errorf("%s: type = %q, want deleted", n, ev.Type)
		}
	}
}

func TestS3Source_AccessedAndUnknownIgnored(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	for _, n := range []string{"s3:ObjectAccessed:Get", "s3:ObjectRestore:Completed", "s3:Replication:OperationFailedReplication"} {
		_, err := src.Parse(awsEvent(n, "b", "t/ok/k", ""), "")
		if !errors.Is(err, ErrIgnoredEvent) {
			t.Errorf("%s: err = %v, want ErrIgnoredEvent", n, err)
		}
	}
}

func TestS3Source_PercentEncodedBytesAndLiteralPlus(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	// %C3%A9 → 'é'; %2B → literal '+' (a real '+' in the key, not a space).
	ev, err := src.Parse(awsEvent("s3:ObjectCreated:Put", "b", "t/ok/caf%C3%A9%2Bx.txt", ""), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.SubjectFields.Key != "café+x.txt" {
		t.Errorf("key = %q, want 'café+x.txt'", ev.SubjectFields.Key)
	}
}

func TestS3Source_BucketFilter(t *testing.T) {
	src := &S3EventSource{BucketName: "paladin-primary", URI: "s3://primary"}
	// Wrong bucket → ignored.
	if _, err := src.Parse(awsEvent("s3:ObjectCreated:Put", "other", "t/ok/k", ""), ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("wrong bucket: err = %v, want ErrIgnoredEvent", err)
	}
	// Matching bucket → parsed.
	if _, err := src.Parse(awsEvent("s3:ObjectCreated:Put", "paladin-primary", "t/ok/k", ""), ""); err != nil {
		t.Errorf("matching bucket: %v", err)
	}
}

func TestS3Source_NonOCPLayoutIgnored(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	for _, key := range []string{
		"flat-key", // no slashes
		"only/two", // 2 segments
		"/ok/k",    // empty tenant
		"t//k",     // empty object_key
		"t/ok/",    // empty key
	} {
		if _, err := src.Parse(awsEvent("s3:ObjectCreated:Put", "b", key, ""), ""); !errors.Is(err, ErrIgnoredEvent) {
			t.Errorf("key %q: err = %v, want ErrIgnoredEvent", key, err)
		}
	}
}

func TestS3Source_EmptyRecordsUnrecognised(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	if _, err := src.Parse([]byte(`{"Records":[]}`), ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("empty Records: err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestS3Source_MalformedJSONUnrecognised(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	if _, err := src.Parse([]byte(`{not json`), ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("malformed: err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestS3Source_FallbackIDStableAndSequencerSensitive(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	// No x-amz-request-id → content-hash id, stable across identical replays.
	a, _ := src.Parse(awsEvent("s3:ObjectCreated:Put", "b", "t/ok/k", ""), "")
	b, _ := src.Parse(awsEvent("s3:ObjectCreated:Put", "b", "t/ok/k", ""), "")
	if a.ID == "" || a.ID != b.ID {
		t.Errorf("fallback id not stable: %q vs %q", a.ID, b.ID)
	}
	// A different sequencer is a different physical event → different id.
	other := []byte(`{"Records":[{"eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t/ok/k","sequencer":"DIFFERENT"}}}]}`)
	c, _ := src.Parse(other, "")
	if c.ID == a.ID {
		t.Error("different sequencer should produce a different id")
	}
}

func TestS3Source_SubjectCanonicalForm(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	ev, err := src.Parse(awsEvent("s3:ObjectCreated:Put", "b", "tid/ok/k", ""), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "tenants/tid/objectKeys/ok/objects-by-key/k"
	if ev.Subject != want {
		t.Errorf("subject = %q, want %q", ev.Subject, want)
	}
	if ev.Source != "s3://primary" || ev.SpecVersion != "1.0" || ev.DataContentType != "application/json" {
		t.Errorf("envelope metadata wrong: %+v", ev)
	}
}

package eventingest

import (
	"errors"
	"strings"
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
	ev, err := parseOne(src, awsEvent("s3:ObjectCreated:Put", "paladin", "11111111-1111-4111-8111-111111111111/ok/red+flower.jpg", "req-1"), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("type = %q, want uploaded", ev.Type)
	}
	again, _ := parseOne(src, awsEvent("s3:ObjectCreated:Put", "paladin", "11111111-1111-4111-8111-111111111111/ok/red+flower.jpg", "req-1"), "")
	other, _ := parseOne(src, awsEvent("s3:ObjectCreated:Put", "paladin", "11111111-1111-4111-8111-111111111111/ok/red+flower.jpg", "req-2"), "")
	if ev.ID == "" || ev.ID != again.ID || ev.ID == other.ID {
		t.Errorf("ids = %q, %q, %q; want one per broker request id, stable on a retry", ev.ID, again.ID, other.ID)
	}
	if ev.SubjectFields.TenantID != "11111111-1111-4111-8111-111111111111" {
		t.Errorf("tenant = %q", ev.SubjectFields.TenantID)
	}
	if ev.SubjectFields.Collection != "ok" {
		t.Errorf("collection = %q", ev.SubjectFields.Collection)
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
	ev, err := parseOne(src, body, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.SubjectFields.TenantID != "t" || ev.SubjectFields.Collection != "ok" {
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
		ev, err := parseOne(src, awsEvent(n, "b", "t/ok/k", ""), "")
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
		ev, err := parseOne(src, awsEvent(n, "b", "t/ok/k", ""), "")
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
		_, err := parseOne(src, awsEvent(n, "b", "t/ok/k", ""), "")
		if !errors.Is(err, ErrIgnoredEvent) {
			t.Errorf("%s: err = %v, want ErrIgnoredEvent", n, err)
		}
	}
}

func TestS3Source_PercentEncodedBytesAndLiteralPlus(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	// %C3%A9 → 'é'; %2B → literal '+' (a real '+' in the key, not a space).
	ev, err := parseOne(src, awsEvent("s3:ObjectCreated:Put", "b", "t/ok/caf%C3%A9%2Bx.txt", ""), "")
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
	if _, err := parseOne(src, awsEvent("s3:ObjectCreated:Put", "other", "t/ok/k", ""), ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("wrong bucket: err = %v, want ErrIgnoredEvent", err)
	}
	// Matching bucket → parsed.
	if _, err := parseOne(src, awsEvent("s3:ObjectCreated:Put", "paladin-primary", "t/ok/k", ""), ""); err != nil {
		t.Errorf("matching bucket: %v", err)
	}
}

func TestS3Source_NonPaladinLayoutIgnored(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	for _, key := range []string{
		"flat-key", // no slashes
		"only/two", // 2 segments
		"/ok/k",    // empty tenant
		"t//k",     // empty collection
		"t/ok/",    // empty key
	} {
		if _, err := parseOne(src, awsEvent("s3:ObjectCreated:Put", "b", key, ""), ""); !errors.Is(err, ErrIgnoredEvent) {
			t.Errorf("key %q: err = %v, want ErrIgnoredEvent", key, err)
		}
	}
}

func TestS3Source_EmptyRecordsUnrecognised(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	if _, err := parseOne(src, []byte(`{"Records":[]}`), ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("empty Records: err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestS3Source_MalformedJSONUnrecognised(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	if _, err := parseOne(src, []byte(`{not json`), ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("malformed: err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestS3Source_FallbackIDStableAndSequencerSensitive(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	// No x-amz-request-id → content-hash id, stable across identical replays.
	a, _ := parseOne(src, awsEvent("s3:ObjectCreated:Put", "b", "t/ok/k", ""), "")
	b, _ := parseOne(src, awsEvent("s3:ObjectCreated:Put", "b", "t/ok/k", ""), "")
	if a.ID == "" || a.ID != b.ID {
		t.Errorf("fallback id not stable: %q vs %q", a.ID, b.ID)
	}
	// A different sequencer is a different physical event → different id.
	other := []byte(`{"Records":[{"eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"b"},"object":{"key":"t/ok/k","sequencer":"DIFFERENT"}}}]}`)
	c, _ := parseOne(src, other, "")
	if c.ID == a.ID {
		t.Error("different sequencer should produce a different id")
	}
}

func TestS3Source_SubjectCanonicalForm(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	ev, err := parseOne(src, awsEvent("s3:ObjectCreated:Put", "b", "tid/ok/k", ""), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "tenants/tid/collections/ok/objects-by-key/k"
	if ev.Subject != want {
		t.Errorf("subject = %q, want %q", ev.Subject, want)
	}
	if ev.Source != "s3://primary" || ev.SpecVersion != "1.0" || ev.DataContentType != "application/json" {
		t.Errorf("envelope metadata wrong: %+v", ev)
	}
}

// s3Record builds one record of an envelope.
func s3RecordJSON(eventName, bucket, key, reqID string) string {
	return `{"eventSource":"aws:s3","eventName":"` + eventName +
		`","eventTime":"2026-01-02T03:04:05.678Z","s3":{"bucket":{"name":"` + bucket +
		`"},"object":{"key":"` + key + `","sequencer":"00SEQ01"}},"responseElements":{"x-amz-request-id":"` + reqID + `"}}`
}

// A notification may carry several records; DeleteObjects reports every key
// it removed under one request id. Each Paladin record is its own event, with
// its own id and its own record as Data; the rest are skipped.
func TestS3Source_EveryRecordIsAnEvent(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary", BucketName: "b"}
	records := []string{
		s3RecordJSON("s3:ObjectRemoved:Delete", "b", "t/ok/first", "batch-1"),
		s3RecordJSON("s3:ObjectRemoved:Delete", "b", "not-paladin", "batch-1"),
		s3RecordJSON("s3:ObjectAccessed:Get", "b", "t/ok/read", "batch-1"),
		s3RecordJSON("s3:ObjectRemoved:Delete", "other", "t/ok/elsewhere", "batch-1"),
		s3RecordJSON("s3:ObjectRemoved:Delete", "b", "t/ok/second", "batch-1"),
	}
	evs, err := src.Parse([]byte(`{"Records":[`+strings.Join(records, ",")+`]}`), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("events = %d, want the two Paladin records", len(evs))
	}
	for i, want := range []struct{ key, record string }{{"first", records[0]}, {"second", records[4]}} {
		if evs[i].SubjectFields.Key != want.key || evs[i].Type != EventTypeDeleted {
			t.Errorf("event %d = %s %q, want deleted %q", i, evs[i].Type, evs[i].SubjectFields.Key, want.key)
		}
		if string(evs[i].Data) != want.record {
			t.Errorf("event %d data = %s, want its own record", i, evs[i].Data)
		}
	}
	if evs[0].ID == evs[1].ID {
		t.Errorf("records of one request share id %q; they would dedup as one", evs[0].ID)
	}
}

func TestS3Source_NoRecordOfOurs(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary", BucketName: "b"}
	body := `{"Records":[` + s3RecordJSON("s3:ObjectCreated:Put", "other", "t/ok/k", "r") + `,` +
		s3RecordJSON("s3:ObjectCreated:Put", "b", "flat", "r") + `]}`
	if _, err := src.Parse([]byte(body), ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
	malformed := `{"Records":[` + s3RecordJSON("s3:ObjectCreated:Put", "b", "t/ok/k", "r") + `,"not a record"]}`
	if _, err := src.Parse([]byte(malformed), ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("malformed record: err = %v, want ErrUnrecognisedEvent", err)
	}
}

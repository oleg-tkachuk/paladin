package eventingest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// natsEnvelope mirrors gocloud.dev/pubsub/natspubsub#encodeMessage
// well enough to drive the source under test. We can't import the
// real `encodeMessage` (it's package-private to natspubsub), so we
// encode the same way: gob.Encode(metadata) || gob.Encode(body).
func natsEnvelope(t *testing.T, metadata map[string]string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(metadata); err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	if err := enc.Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return buf.Bytes()
}

// fakeFilerEvent emits a proto-shaped EventNotification body with
// the requested old_entry / new_entry tags present. The Entry
// payload is a minimal `name` field (tag 1, length-delimited) — SF
// lets us send a stub without all of Entry's fields populated, and
// our scanner only checks tag presence.
func fakeFilerEvent(t *testing.T, hasOld, hasNew bool) []byte {
	t.Helper()
	var buf []byte
	if hasOld {
		buf = append(buf, encodeEntryField(1, "old.txt")...)
	}
	if hasNew {
		buf = append(buf, encodeEntryField(2, "new.txt")...)
	}
	return buf
}

// encodeEntryField writes one EventNotification field whose value
// is a length-delimited Entry containing only `name = name`.
func encodeEntryField(num protowire.Number, name string) []byte {
	// Inner Entry: `name = <string>` at tag 1, wire type 2.
	inner := protowire.AppendTag(nil, 1, protowire.BytesType)
	inner = protowire.AppendString(inner, name)
	// Outer EventNotification field: tag `num`, wire type 2 (Entry).
	out := protowire.AppendTag(nil, num, protowire.BytesType)
	out = protowire.AppendBytes(out, inner)
	return out
}

func TestSeaweedFSNATSSource_Parse_Create(t *testing.T) {
	src := &SeaweedFSNATSSource{
		BucketName: "paladin-primary",
		URI:        "seaweedfs-nats://primary",
	}
	tenant := "00000000-0000-0000-0000-000000000abc"
	objectKey := "folder1"
	key := "obj.txt"
	path := "/paladin-primary/" + tenant + "/" + objectKey + "/" + key

	body := fakeFilerEvent(t, false, true) // create: only new_entry
	raw := natsEnvelope(t, map[string]string{"key": path}, body)

	ev, err := src.Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("Type = %q, want %q", ev.Type, EventTypeUploaded)
	}
	if ev.SubjectFields.TenantID != tenant {
		t.Errorf("TenantID = %q, want %q", ev.SubjectFields.TenantID, tenant)
	}
	if ev.SubjectFields.ObjectKey != objectKey {
		t.Errorf("ObjectKey = %q, want %q", ev.SubjectFields.ObjectKey, objectKey)
	}
	if ev.SubjectFields.Key != key {
		t.Errorf("Key = %q, want %q", ev.SubjectFields.Key, key)
	}
	if !strings.HasPrefix(ev.Subject, "tenants/"+tenant) {
		t.Errorf("Subject = %q, want prefix tenants/%s", ev.Subject, tenant)
	}
	if ev.ID == "" {
		t.Errorf("ID empty — should hash path+evType+minute")
	}
	if ev.Source != src.URI {
		t.Errorf("Source = %q, want %q", ev.Source, src.URI)
	}
}

func TestSeaweedFSNATSSource_Parse_Update(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	body := fakeFilerEvent(t, true, true) // update: both entries
	raw := natsEnvelope(t,
		map[string]string{"key": "/paladin-primary/t/k/o.txt"},
		body,
	)
	ev, err := src.Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Update collapses to "uploaded" — same downstream handling as
	// create. The dedup ID still differs because the path is the
	// same but the test harness hashes within one minute, so two
	// rapid updates produce the same id (which is the design).
	if ev.Type != EventTypeUploaded {
		t.Errorf("Type = %q, want %q (update should map to uploaded)",
			ev.Type, EventTypeUploaded)
	}
}

func TestSeaweedFSNATSSource_Parse_Delete(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	body := fakeFilerEvent(t, true, false) // delete: only old_entry
	raw := natsEnvelope(t,
		map[string]string{"key": "/paladin-primary/t/k/gone.txt"},
		body,
	)
	ev, err := src.Parse(raw, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeDeleted {
		t.Errorf("Type = %q, want %q", ev.Type, EventTypeDeleted)
	}
}

func TestSeaweedFSNATSSource_Parse_BothEntriesAbsent_Ignored(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	body := fakeFilerEvent(t, false, false) // neither old nor new
	raw := natsEnvelope(t,
		map[string]string{"key": "/paladin-primary/t/k/whatever.txt"},
		body,
	)
	_, err := src.Parse(raw, "")
	if !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
}

func TestSeaweedFSNATSSource_Parse_PathOutsideBucket_Ignored(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	body := fakeFilerEvent(t, false, true)
	raw := natsEnvelope(t,
		// Path doesn't start with the configured bucket — common when
		// SF publishes filer events for a sibling bucket the operator
		// uses for unrelated data (audit, snapshots).
		map[string]string{"key": "/other-bucket/t/k/o.txt"},
		body,
	)
	_, err := src.Parse(raw, "")
	if !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
}

func TestSeaweedFSNATSSource_Parse_MissingKey_Unrecognised(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	body := fakeFilerEvent(t, false, true)
	raw := natsEnvelope(t,
		// Empty metadata: SF's gocdk_pub_sub always stamps `key`, so
		// a missing one signals a foreign publisher reusing the
		// subject — unrecognised, not ignored.
		map[string]string{},
		body,
	)
	_, err := src.Parse(raw, "")
	if !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestSeaweedFSNATSSource_Parse_MalformedGob_Unrecognised(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	_, err := src.Parse([]byte("not a gob stream"), "")
	if !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestScanFilerEventNotification_TolersUnknownFields(t *testing.T) {
	// Build a body that carries old_entry (1), new_entry (2), AND a
	// future field at tag 99 (varint). The scanner must still report
	// has_old + has_new and not error on the unknown tag.
	body := fakeFilerEvent(t, true, true)
	body = append(body,
		protowire.AppendTag(nil, 99, protowire.VarintType)...,
	)
	body = protowire.AppendVarint(body, 12345)

	hasOld, hasNew, err := scanFilerEventNotification(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !hasOld || !hasNew {
		t.Errorf("hasOld=%v hasNew=%v, want both true (unknown field 99 should be skipped)",
			hasOld, hasNew)
	}
}

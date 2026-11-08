package eventingest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest/filerpb"
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

// fakeFilerEvent is an EventNotification body with the requested entries
// present, each carrying only its name: no version, so a parse falls back to
// the receipt minute.
func fakeFilerEvent(t *testing.T, hasOld, hasNew bool) []byte {
	t.Helper()
	var n filerpb.EventNotification
	if hasOld {
		n.OldEntry = &filerpb.Entry{Name: "old.txt"}
	}
	if hasNew {
		n.NewEntry = &filerpb.Entry{Name: "new.txt"}
	}
	return marshalNotification(t, &n)
}

func marshalNotification(t *testing.T, n *filerpb.EventNotification) []byte {
	t.Helper()
	body, err := proto.Marshal(n)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return body
}

// natsPath is the filer path of an object under the test bucket.
const natsPath = "/paladin-primary/00000000-0000-0000-0000-000000000abc/docs/obj.txt"

// createOf is a create event for entry at natsPath.
func createOf(t *testing.T, entry *filerpb.Entry) []byte {
	t.Helper()
	return natsEnvelope(t, map[string]string{"key": natsPath}, marshalNotification(t, &filerpb.EventNotification{NewEntry: entry}))
}

func TestSeaweedFSNATSSource_Parse_Create(t *testing.T) {
	src := &SeaweedFSNATSSource{
		BucketName: "paladin-primary",
		URI:        "seaweedfs-nats://primary",
	}
	tenant := "00000000-0000-0000-0000-000000000abc"
	collection := "folder1"
	key := "obj.txt"
	path := "/paladin-primary/" + tenant + "/" + collection + "/" + key

	body := fakeFilerEvent(t, false, true) // create: only new_entry
	raw := natsEnvelope(t, map[string]string{"key": path}, body)

	ev, err := parseOne(src, raw, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("Type = %q, want %q", ev.Type, EventTypeUploaded)
	}
	if ev.SubjectFields.TenantID != tenant {
		t.Errorf("TenantID = %q, want %q", ev.SubjectFields.TenantID, tenant)
	}
	if ev.SubjectFields.Collection != collection {
		t.Errorf("Collection = %q, want %q", ev.SubjectFields.Collection, collection)
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

// The event's time and id come from the source's clock: one event parsed
// twice in the same minute is one event to dedup, and in the next minute
// another.
func TestSeaweedFSNATSSource_Parse_IDFromTheClock(t *testing.T) {
	minuteStart := time.Date(2026, 10, 31, 21, 27, 0, 0, time.UTC)
	path := "/paladin-primary/00000000-0000-0000-0000-000000000abc/docs/obj.txt"
	raw := natsEnvelope(t, map[string]string{"key": path}, fakeFilerEvent(t, false, true))
	parseAt := func(at time.Time) CloudEvent {
		t.Helper()
		src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x", Now: func() time.Time { return at }}
		ev, err := parseOne(src, raw, "")
		if err != nil {
			t.Fatalf("Parse at %s: %v", at, err)
		}
		if !ev.Time.Equal(at) {
			t.Errorf("Time = %s, want the clock's %s", ev.Time, at)
		}
		return ev
	}

	first := parseAt(minuteStart.Add(time.Second))
	if same := parseAt(minuteStart.Add(time.Minute - time.Second)); same.ID != first.ID {
		t.Errorf("same minute: ID %s, want %s", same.ID, first.ID)
	}
	if next := parseAt(minuteStart.Add(time.Minute)); next.ID == first.ID {
		t.Errorf("next minute: ID %s, want another than %s", next.ID, first.ID)
	}
}

func TestSeaweedFSNATSSource_Parse_Update(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	body := fakeFilerEvent(t, true, true) // update: both entries
	raw := natsEnvelope(t,
		map[string]string{"key": "/paladin-primary/00000000-0000-0000-0000-000000000abc/k/o.txt"},
		body,
	)
	ev, err := parseOne(src, raw, "")
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
		map[string]string{"key": "/paladin-primary/00000000-0000-0000-0000-000000000abc/k/gone.txt"},
		body,
	)
	ev, err := parseOne(src, raw, "")
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
		map[string]string{"key": "/paladin-primary/00000000-0000-0000-0000-000000000abc/k/whatever.txt"},
		body,
	)
	_, err := parseOne(src, raw, "")
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
	_, err := parseOne(src, raw, "")
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
	_, err := parseOne(src, raw, "")
	if !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestSeaweedFSNATSSource_Parse_MalformedGob_Unrecognised(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	_, err := parseOne(src, []byte("not a gob stream"), "")
	if !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

// Fields SeaweedFS sends that the subset leaves out — an entry's chunks, a
// notification's delete_chunks, one a later release adds — are skipped.
func TestSeaweedFSNATSSource_Parse_ToleratesUnknownFields(t *testing.T) {
	const (
		chunksField       protowire.Number = 3
		deleteChunksField protowire.Number = 3
		laterField        protowire.Number = 99
		laterValue                         = 12345
	)
	entry := marshalEntryWith(t, &filerpb.Entry{Name: "new.txt"}, chunksField)
	body := protowire.AppendTag(nil, 2, protowire.BytesType) // new_entry
	body = protowire.AppendBytes(body, entry)
	body = protowire.AppendTag(body, deleteChunksField, protowire.VarintType)
	body = protowire.AppendVarint(body, 1)
	body = protowire.AppendTag(body, laterField, protowire.VarintType)
	body = protowire.AppendVarint(body, laterValue)

	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	ev, err := parseOne(src, natsEnvelope(t, map[string]string{"key": natsPath}, body), "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("Type = %q, want %q", ev.Type, EventTypeUploaded)
	}
}

// marshalEntryWith is entry's bytes with an empty length-delimited field
// appended, as a chunk the subset does not decode.
func marshalEntryWith(t *testing.T, entry *filerpb.Entry, field protowire.Number) []byte {
	t.Helper()
	b, err := proto.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendBytes(b, nil)
}

// A body that is no protobuf is no event of SeaweedFS's.
func TestSeaweedFSNATSSource_Parse_MalformedBody_Unrecognised(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	malformed := protowire.AppendTag(nil, 2, protowire.BytesType) // a length that never comes
	_, err := parseOne(src, natsEnvelope(t, map[string]string{"key": natsPath}, malformed), "")
	if !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

// The directories the filer creates for a key's prefixes are no object.
func TestSeaweedFSNATSSource_Parse_Directory_Ignored(t *testing.T) {
	src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x"}
	_, err := parseOne(src, createOf(t, &filerpb.Entry{Name: "docs", IsDirectory: true}), "")
	if !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
}

// An entry with a version names its event whenever it is received: a
// redelivery an hour later is the same event, a second write of the key —
// a later commit, or before 4.51 a later or different modification — is
// another, and the event's time is the version's.
func TestSeaweedFSNATSSource_Parse_IDFromTheEntry(t *testing.T) {
	const (
		commitTs  int64  = 1793700668832899354
		mtime     int64  = 1793700665
		size      uint64 = 5
		otherSize uint64 = 6
	)
	md5 := []byte("hello-digest")
	clock := time.Date(2026, 10, 31, 21, 27, 0, 0, time.UTC)
	parseAt := func(at time.Time, entry *filerpb.Entry) CloudEvent {
		t.Helper()
		src := &SeaweedFSNATSSource{BucketName: "paladin-primary", URI: "x", Now: func() time.Time { return at }}
		ev, err := parseOne(src, createOf(t, entry), "")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return ev
	}
	committed := func(ts int64) *filerpb.Entry { return &filerpb.Entry{Name: "obj.txt", CommitTsNs: ts} }
	modified := func(sec int64, sz uint64) *filerpb.Entry {
		return &filerpb.Entry{Name: "obj.txt", Attributes: &filerpb.FuseAttributes{Mtime: sec, FileSize: sz, Md5: md5}}
	}

	first := parseAt(clock, committed(commitTs))
	if !first.Time.Equal(time.Unix(0, commitTs)) {
		t.Errorf("Time = %s, want the commit time", first.Time)
	}
	if later := parseAt(clock.Add(time.Hour), committed(commitTs)); later.ID != first.ID {
		t.Error("a redelivery an hour later has a new id: dedup would not catch it")
	}
	if next := parseAt(clock, committed(commitTs+1)); next.ID == first.ID {
		t.Error("a later commit of the key shares the first's id: it would be dropped")
	}

	old := parseAt(clock, modified(mtime, size))
	if !old.Time.Equal(time.Unix(mtime, 0)) {
		t.Errorf("Time = %s, want the modification time", old.Time)
	}
	if later := parseAt(clock.Add(time.Hour), modified(mtime, size)); later.ID != old.ID {
		t.Error("before 4.51, a redelivery an hour later has a new id")
	}
	if sameSecond := parseAt(clock, modified(mtime, otherSize)); sameSecond.ID == old.ID {
		t.Error("before 4.51, another write in the same second shares the id: it would be dropped")
	}
	if nextSecond := parseAt(clock, modified(mtime+1, size)); nextSecond.ID == old.ID {
		t.Error("before 4.51, a later write of the same bytes shares the id")
	}
}

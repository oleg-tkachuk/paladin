package eventingest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest/filerpb"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// fuzzBucket is the bucket every fuzzed source is configured for.
const fuzzBucket = "paladin-primary"

// subjectSep separates the segments of a storage path.
const subjectSep = "/"

// fuzzNow is the clock of the NATS source, so a version-less entry names the
// same event on every run.
var fuzzNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// segmented is how a source splits its storage path: whether the tenant and
// the collection it reports each come from one segment, and whether the
// tenant must already be a UUID. A collection from one segment is a valid
// collection name, decoded from its segment (storagepath.ParseCollectionSegment),
// so it may hold '/' only as a nested collection does. The cloud-events
// subject anchors on its separators, so its collection is taken as written.
type segmented struct {
	oneSegment, uuidTenant bool
}

// checkParsed holds what every source promises the worker for any payload:
// a refusal it can tell apart — unrecognised, so dead-lettered, or ignored,
// so acknowledged — and otherwise events the handler can act on, no more of
// them than the payload has bytes.
func checkParsed(t *testing.T, raw []byte, evs []CloudEvent, err error, shape segmented) {
	t.Helper()
	if err != nil {
		if !errors.Is(err, ErrUnrecognisedEvent) && !errors.Is(err, ErrIgnoredEvent) {
			t.Fatalf("Parse(%q) = %v, want ErrUnrecognisedEvent or ErrIgnoredEvent", raw, err)
		}
		if evs != nil {
			t.Fatalf("Parse(%q) refused with %v but returned %d events", raw, err, len(evs))
		}
		return
	}
	if len(evs) == 0 || len(evs) > len(raw) {
		t.Fatalf("Parse(%q) = %d events from %d bytes", raw, len(evs), len(raw))
	}
	for i, ev := range evs {
		if ev.Type != EventTypeUploaded && ev.Type != EventTypeDeleted {
			t.Fatalf("Parse(%q) event %d has type %q", raw, i, ev.Type)
		}
		s := ev.SubjectFields
		if ev.ID == "" || s.TenantID == "" || s.Collection == "" || s.Key == "" {
			t.Fatalf("Parse(%q) event %d is incomplete: id %q, subject %+v", raw, i, ev.ID, s)
		}
		if shape.oneSegment && (strings.Contains(s.TenantID, subjectSep) ||
			storagepath.ValidateCollection(s.Collection) != nil ||
			strings.Contains(storagepath.CollectionSegment(s.Collection), subjectSep)) {
			t.Fatalf("Parse(%q) event %d splits its path at the wrong place: %+v", raw, i, s)
		}
		if _, perr := uuid.Parse(s.TenantID); shape.uuidTenant && perr != nil {
			t.Fatalf("Parse(%q) event %d names tenant %q", raw, i, s.TenantID)
		}
	}
}

// addFiles seeds f with every file under testdata matching pattern.
func addFiles(f *testing.F, pattern string) {
	f.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", pattern))
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p) // #nosec G304 -- the package's own test fixtures
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
}

func FuzzParseSeaweedFS(f *testing.F) {
	addFiles(f, "seaweedfs-webhook-*.json")
	f.Add([]byte(`{"key":"/buckets/paladin-primary/00000000-0000-0000-0000-000000000abc/docs/a.txt","event_type":"create","message":{"new_entry":{"attributes":{"file_size":3,"mtime":1}}}}`))
	f.Add([]byte(`{"key":"x","event_type":"delete","message":{}}`))
	f.Add([]byte(`not json`))
	src := &SeaweedFSSource{BucketName: fuzzBucket, URI: "seaweedfs://fuzz"}
	f.Fuzz(func(t *testing.T, raw []byte) {
		evs, err := src.Parse(raw, "application/json")
		checkParsed(t, raw, evs, err, segmented{oneSegment: true, uuidTenant: true})
	})
}

func FuzzParseS3(f *testing.F) {
	f.Add([]byte(`{"Records":[{"eventSource":"aws:s3","eventName":"s3:ObjectCreated:Put","eventTime":"2026-01-01T00:00:00Z","s3":{"bucket":{"name":"paladin-primary"},"object":{"key":"t%2Fok%2Fnested%2Fkey.bin","size":3,"eTag":"e","sequencer":"01"}},"responseElements":{"x-amz-request-id":"m-1"}}]}`))
	f.Add([]byte(`{"Records":[{"eventName":"s3:ObjectRemoved:Delete","s3":{"bucket":{"name":"paladin-primary"},"object":{"key":"t/c/red+flower.jpg"}}},{"eventName":"s3:ObjectAccessed:Get"}]}`))
	f.Add([]byte(`{"Records":[]}`))
	f.Add([]byte(`{"Records":[7]}`))
	src := &S3EventSource{BucketName: fuzzBucket, URI: "s3://fuzz"}
	f.Fuzz(func(t *testing.T, raw []byte) {
		evs, err := src.Parse(raw, "application/json")
		checkParsed(t, raw, evs, err, segmented{oneSegment: true})
	})
}

func FuzzParseCloudEvents(f *testing.F) {
	f.Add([]byte(`{"specversion":"1.0","type":"paladin.object.uploaded","source":"agent-runtime://acme","id":"evt-42","time":"2026-01-01T00:00:00Z","subject":"tenants/0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/collections/contracts/objects-by-key/2026/q1.pdf","paladinsize":4096,"data":{"foo":"bar"}}`))
	f.Add([]byte(`{"specversion":"1.0","type":"paladin.object.deleted","source":"s","id":"1","subject":"0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/c/k"}`))
	f.Add([]byte(`{"specversion":"1.0","type":"other","source":"s","id":"1"}`))
	src := &CloudEventsSource{URI: "cloudevents://fuzz"}
	f.Fuzz(func(t *testing.T, raw []byte) {
		evs, err := src.Parse(raw, "application/json")
		checkParsed(t, raw, evs, err, segmented{})
	})
}

// natsSeed is a gocdk_pub_sub NATS message: gob(metadata) then gob(body).
func natsSeed(f *testing.F, key string, n *filerpb.EventNotification) []byte {
	f.Helper()
	body, err := proto.Marshal(n)
	if err != nil {
		f.Fatal(err)
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(map[string]string{"key": key}); err != nil {
		f.Fatal(err)
	}
	if err := enc.Encode(body); err != nil {
		f.Fatal(err)
	}
	return buf.Bytes()
}

func FuzzParseSeaweedFSNATS(f *testing.F) {
	path := "/" + fuzzBucket + "/00000000-0000-0000-0000-000000000abc/docs/obj.txt"
	f.Add(natsSeed(f, path, &filerpb.EventNotification{NewEntry: &filerpb.Entry{Name: "obj.txt", Attributes: &filerpb.FuseAttributes{FileSize: 3, Mtime: 1}}}))
	f.Add(natsSeed(f, path, &filerpb.EventNotification{OldEntry: &filerpb.Entry{Name: "obj.txt"}}))
	f.Add(natsSeed(f, path, &filerpb.EventNotification{}))
	f.Add(natsSeed(f, "", &filerpb.EventNotification{NewEntry: &filerpb.Entry{Name: "x"}}))
	f.Add([]byte{0xff, 0x00})
	src := &SeaweedFSNATSSource{BucketName: fuzzBucket, URI: "nats://fuzz", Now: func() time.Time { return fuzzNow }}
	f.Fuzz(func(t *testing.T, raw []byte) {
		evs, err := src.Parse(raw, "")
		checkParsed(t, raw, evs, err, segmented{oneSegment: true, uuidTenant: true})
	})
}

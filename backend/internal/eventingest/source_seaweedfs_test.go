package eventingest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The bodies under testdata/seaweedfs-webhook-*.json are what SeaweedFS 4.51's
// [notification.webhook] posted for a bucket created through its S3 gateway,
// an object put under it and deleted again.
const (
	webhookTenant    = "3a823fd4-0b3d-4ce2-a280-93b8d75cc07b"
	webhookCommitTs  = 1793700668832899354
	webhookObjectMD5 = "5d41402abc4b2a76b9719d911017c592" // md5("hello")
)

func webhookBody(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "seaweedfs-webhook-"+name+".json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// A put reaches ingest as the object's subject — the bucket segment of the
// filer path dropped, not read as the tenant — with the size and etag the
// promote checks the object's registration against.
func TestSeaweedFSSource_ParseCapturedCreate(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	ev, err := parseOne(src, webhookBody(t, "create"), "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded || ev.Source != "seaweedfs://primary" {
		t.Errorf("type, source = %q, %q", ev.Type, ev.Source)
	}
	f := ev.SubjectFields
	if f.TenantID != webhookTenant || f.Collection != "docs" || f.Key != "dir/a b.txt" {
		t.Errorf("subject = %+v, want %s / docs / dir/a b.txt", f, webhookTenant)
	}
	if f.SizeBytes != int64(len("hello")) || f.Etag != webhookObjectMD5 {
		t.Errorf("size, etag = %d, %q; want %d, %q", f.SizeBytes, f.Etag, len("hello"), webhookObjectMD5)
	}
	if !ev.Time.Equal(time.Unix(0, webhookCommitTs)) {
		t.Errorf("time = %v, want the entry's commit time", ev.Time)
	}
}

func TestSeaweedFSSource_ParseCapturedDelete(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	ev, err := parseOne(src, webhookBody(t, "delete"), "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeDeleted || ev.SubjectFields.Key != "dir/a b.txt" {
		t.Errorf("type, key = %q, %q", ev.Type, ev.SubjectFields.Key)
	}
	create, _ := parseOne(src, webhookBody(t, "create"), "application/json")
	if ev.ID == create.ID {
		t.Error("the delete of an entry has its create's id: dedup would drop it")
	}
}

// A second put of one key is a new entry, committed later: it must not
// dedup against the first, or the object it wrote is never promoted.
func TestSeaweedFSSource_ReuploadIsANewEvent(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	first := webhookBody(t, "create")
	second := []byte(strings.Replace(string(first), `"commit_ts_ns":1793700668832899354`, `"commit_ts_ns":1793700668832899355`, 1))
	a, errA := parseOne(src, first, "application/json")
	b, errB := parseOne(src, second, "application/json")
	if errA != nil || errB != nil {
		t.Fatalf("Parse: %v, %v", errA, errB)
	}
	if a.ID == b.ID {
		t.Error("two puts of one key share an id")
	}
	if again, _ := parseOne(src, first, "application/json"); again.ID != a.ID {
		t.Error("a redelivery of one put has a new id: dedup would not catch it")
	}
}

// What the filer does around an object — the directories a key implies, the
// gateway's own records under .system — is no object of Paladin's.
func TestSeaweedFSSource_IgnoresWhatIsNoObject(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	for _, name := range []string{"directory", "system"} {
		if _, err := parseOne(src, webhookBody(t, name), "application/json"); !errors.Is(err, ErrIgnoredEvent) {
			t.Errorf("%s: err = %v, want ErrIgnoredEvent", name, err)
		}
	}
}

func TestSeaweedFSSource_BucketFilter(t *testing.T) {
	body := webhookBody(t, "create")
	if _, err := parseOne(&SeaweedFSSource{BucketName: "capture-bucket"}, body, "application/json"); err != nil {
		t.Errorf("its own bucket: %v", err)
	}
	if _, err := parseOne(&SeaweedFSSource{BucketName: "another"}, body, "application/json"); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("another bucket: err = %v, want ErrIgnoredEvent", err)
	}
}

func TestSeaweedFSSource_Parse_IgnoredEventType(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	body := []byte(strings.Replace(string(webhookBody(t, "create")), `"event_type":"create"`, `"event_type":"checksum"`, 1))
	if _, err := parseOne(src, body, "application/json"); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("err = %v, want ErrIgnoredEvent", err)
	}
}

func TestSeaweedFSSource_Parse_Unrecognised(t *testing.T) {
	src := &SeaweedFSSource{URI: "seaweedfs://primary"}
	for name, body := range map[string]string{
		"not json":         "not json",
		"no key":           `{"event_type":"create"}`,
		"no entry at all":  `{"key":"/buckets/b/` + webhookTenant + `/c/k","event_type":"create","message":{}}`,
		"an md5 not bytes": `{"key":"/buckets/b/` + webhookTenant + `/c/k","event_type":"create","message":{"new_entry":{"attributes":{"md5":"!"}}}}`,
	} {
		if _, err := parseOne(src, []byte(body), "application/json"); !errors.Is(err, ErrUnrecognisedEvent) {
			t.Errorf("%s: err = %v, want ErrUnrecognisedEvent", name, err)
		}
	}
}

// The filer path is /buckets/<bucket>/<tenant>/<collection>/<key>; the
// leading "/" and "buckets/" are optional, as the two publishers differ.
func TestParseSeaweedFSPath(t *testing.T) {
	const tenant = webhookTenant
	cases := map[string]struct {
		path, bucket string
		want         SubjectFields
		ok           bool
	}{
		"any bucket":            {"/buckets/b/" + tenant + "/c/k/l", "", SubjectFields{TenantID: tenant, Collection: "c", Key: "k/l"}, true},
		"no buckets/ namespace": {"b/" + tenant + "/c/k", "", SubjectFields{TenantID: tenant, Collection: "c", Key: "k"}, true},
		"its own bucket":        {"/buckets/b/" + tenant + "/c/k", "b", SubjectFields{TenantID: tenant, Collection: "c", Key: "k"}, true},
		"another bucket":        {"/buckets/x/" + tenant + "/c/k", "b", SubjectFields{}, false},
		"tenant not a uuid":     {"/buckets/.system/owners/paladin-dev/b", "", SubjectFields{}, false},
		"no key":                {"/buckets/b/" + tenant + "/c", "", SubjectFields{}, false},
		"empty key":             {"/buckets/b/" + tenant + "/c/", "", SubjectFields{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := parseSeaweedFSPath(tc.path, tc.bucket)
			if ok != tc.ok || got != tc.want {
				t.Errorf("= %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

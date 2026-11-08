package eventingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"uuid"
)

// SeaweedFS source adapter.
//
// Wire format: SeaweedFS' [notification.webhook] publisher posts each filer
// event as the JSON of its filer_pb.EventNotification, beside the path and
// the event type (testdata/seaweedfs-webhook-*.json are bodies SeaweedFS
// 4.51 posted):
//
//	key         — the full filer path: /buckets/<bucket>/<tenant>/<collection>/<key>
//	event_type  — "create" | "update" | "delete" | …
//	message     — old_entry and/or new_entry, each with its attributes
//	              (file_size, md5) and the commit_ts_ns of its write
//
// The path is parsed into SubjectFields by parseSeaweedFSPath. Directories
// the filer creates for a key's prefixes are events too, and are ignored.
//
// id derivation: the body carries no broker-assigned id, so we hash the
// path, the event type and the entry's version (entryVersion). A retry of
// one event produces the same id and the dedup table catches it; a second put
// of the same key is a new entry with its own version, so it is not dropped as
// a replay of the first.

// SeaweedFSSource parses SeaweedFS webhook bodies. BucketName, when set,
// limits the source to that bucket; empty accepts every bucket. URI is the
// source label stamped onto every emitted CloudEvent.
type SeaweedFSSource struct {
	BucketName string
	URI        string // e.g. "seaweedfs://primary"
}

func (s *SeaweedFSSource) Name() string { return "seaweedfs" }

// seaweedFSWebhookPayload is the part of a webhook body ingest reads.
// Extra fields are tolerated (json.Decode is non-strict) so a SeaweedFS
// upgrade that adds new fields doesn't break ingest.
type seaweedFSWebhookPayload struct {
	Key       string `json:"key"`
	EventType string `json:"event_type"`
	Message   struct {
		OldEntry *seaweedFSEntry `json:"old_entry"`
		NewEntry *seaweedFSEntry `json:"new_entry"`
	} `json:"message"`
}

// seaweedFSEntry is a filer_pb.Entry as the webhook serialises it.
type seaweedFSEntry struct {
	IsDirectory bool  `json:"is_directory"`
	CommitTsNs  int64 `json:"commit_ts_ns"`
	Attributes  struct {
		FileSize int64 `json:"file_size"`
		// MD5 is the object's digest, base64 in the JSON; absent for an
		// object written in parts.
		MD5     []byte `json:"md5"`
		Mtime   int64  `json:"mtime"`
		MtimeNs int32  `json:"mtime_ns"`
	} `json:"attributes"`
}

// Parse is the one event of a message: the format carries one.
func (s *SeaweedFSSource) Parse(raw []byte, _ string) ([]CloudEvent, error) {
	return single(s.parseEvent(raw))
}

func (s *SeaweedFSSource) parseEvent(raw []byte) (CloudEvent, error) {
	var p seaweedFSWebhookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return CloudEvent{}, fmt.Errorf("%w: seaweedfs json: %w", ErrUnrecognisedEvent, err)
	}
	if p.Key == "" {
		return CloudEvent{}, ErrUnrecognisedEvent
	}

	evType, ok := seaweedFSEventType(p.EventType)
	if !ok {
		return CloudEvent{}, ErrIgnoredEvent
	}
	// The entry the event is about: what was written, or what was removed.
	entry := p.Message.NewEntry
	if evType == EventTypeDeleted {
		entry = p.Message.OldEntry
	}
	if entry == nil {
		return CloudEvent{}, fmt.Errorf("%w: seaweedfs %s event without its entry", ErrUnrecognisedEvent, p.EventType)
	}
	if entry.IsDirectory {
		return CloudEvent{}, ErrIgnoredEvent
	}

	subjFields, ok := parseSeaweedFSPath(p.Key, s.BucketName)
	if !ok {
		// Path didn't match the Paladin layout — could be a legitimate
		// non-Paladin file dropped into the same bucket. Ignore so we
		// don't fill the dedup table with junk.
		return CloudEvent{}, ErrIgnoredEvent
	}
	if evType == EventTypeUploaded {
		subjFields.SizeBytes = entry.Attributes.FileSize
		subjFields.Etag = hex.EncodeToString(entry.Attributes.MD5)
	}

	version, t, ok := entryVersion(entry.CommitTsNs, entry.Attributes.Mtime, entry.Attributes.MtimeNs,
		entry.Attributes.FileSize, entry.Attributes.MD5)
	if !ok {
		t = time.Now()
	}

	return CloudEvent{
		SpecVersion:     "1.0",
		Type:            evType,
		Source:          s.URI,
		ID:              seaweedFSID(p.Key, p.EventType, version),
		Time:            t,
		DataContentType: "application/json",
		Subject: fmt.Sprintf(
			"tenants/%s/collections/%s/objects-by-key/%s",
			subjFields.TenantID, subjFields.Collection, subjFields.Key,
		),
		Data:          raw,
		SubjectFields: subjFields,
	}, nil
}

// seaweedFSEventType maps SeaweedFS event_type values to the Paladin
// taxonomy. Returns ok=false for events we don't act on.
func seaweedFSEventType(srcType string) (EventType, bool) {
	switch strings.ToLower(srcType) {
	case "create", "update":
		// Both produce a usable etag/size/sequencer in the same
		// PromoteToAvailable transition.
		return EventTypeUploaded, true
	case "delete":
		return EventTypeDeleted, true
	default:
		return "", false
	}
}

// seaweedFSBucketsDir is the filer directory the S3 gateway keeps its
// buckets under.
const seaweedFSBucketsDir = "buckets/"

// parseSeaweedFSPath extracts (tenant_id, collection, key) from the
// full filer path. Layout (s3adapter.composeKey under the bucket):
//
//	/buckets/<bucket>/<tenant_uuid>/<collection>/<key...>
//
// The leading "/" and "buckets/" are optional: the webhook and the
// gocdk_pub_sub-over-NATS publishers both observe the full filer namespace,
// but a test or another publisher may send the bucket-rooted path. bucket,
// when set, is the only bucket accepted; empty accepts any. A tenant segment
// that is no UUID — the gateway's own .system records — is no Paladin
// object. Anything else returns ok=false.
func parseSeaweedFSPath(path, bucket string) (SubjectFields, bool) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(path, "/"), seaweedFSBucketsDir)

	// trimmed is now: <bucket>/<tenant_uuid>/<collection>/<key...>
	// Split into 4 parts max — key may contain / and we want it whole.
	parts := strings.SplitN(trimmed, "/", 4)
	if len(parts) < 4 || parts[2] == "" || parts[3] == "" {
		return SubjectFields{}, false
	}
	if bucket != "" && parts[0] != bucket {
		return SubjectFields{}, false
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return SubjectFields{}, false
	}

	return SubjectFields{
		Bucket:     parts[0],
		TenantID:   parts[1],
		Collection: parts[2],
		Key:        parts[3],
	}, true
}

// entryVersion names the version of a filer entry an event is about, and
// when it was written. A redelivery of the event carries the same entry and
// so the same version; a later write of the key carries another.
//
// The filer's commit time names it where the entry records one (SeaweedFS
// 4.51 and later). Before that, the modification time every filer writes
// does, with the size and digest beside it: the filer may keep the time to
// the second only, and two writes in one second differ in their bytes, or
// are the same write. ok is false when the entry records neither time.
// The size is the webhook's int64 or the protobuf's uint64, as each decodes it.
func entryVersion[Size int64 | uint64](commitTsNs, mtime int64, mtimeNs int32, size Size, md5 []byte) (version string, at time.Time, ok bool) {
	if commitTsNs != 0 {
		return strconv.FormatInt(commitTsNs, 10), time.Unix(0, commitTsNs), true
	}
	if mtime == 0 && mtimeNs == 0 {
		return "", time.Time{}, false
	}
	at = time.Unix(mtime, int64(mtimeNs))
	return fmt.Sprintf("mtime:%d|size:%d|md5:%x", at.UnixNano(), size, md5), at, true
}

// seaweedFSID hashes the canonical fields a duplicate event would
// share — same path + same event type + same entry version = same id.
func seaweedFSID(key, evType, version string) string {
	h := sha256.New()
	fmt.Fprintf(h, "seaweedfs|%s|%s|%s", key, evType, version)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

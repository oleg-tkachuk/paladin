package eventingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SeaweedFS source adapter.
//
// Wire format: SeaweedFS' webhook publisher serialises filer events as
// JSON. The exact shape is the gocdk-shaped envelope its webhook code
// emits for filer notifications (see SeaweedFS source/weed/notification/
// webhook). For ingest we care about three fields:
//
//   key            — full path inside the filer
//   event_type     — "create" | "update" | "delete" | …
//   timestamp_ns   — nanosecond unix timestamp
//
// Since SeaweedFS is configured to publish via S3 path-style buckets
// the PALADIN backend creates, the path follows the PALADIN composeKey
// pattern: "<bucket>/<tenant_uuid>/<object_key>/<key>". The adapter
// strips the leading bucket segment (operators set it; not part of
// the PALADIN resource ref) and parses the rest into SubjectFields.
//
// id derivation: webhook payloads carry no broker-assigned id, so we
// hash (key + event_type + timestamp_ns). Same logical event from a
// retry produces the same id → dedup table catches it.

// SeaweedFSSource parses SeaweedFS webhook bodies. BucketName is
// stripped from the path prefix; URI is the source label
// stamped onto every emitted CloudEvent.
type SeaweedFSSource struct {
	BucketName string // "paladin-primary" — the SeaweedFS bucket this source publishes for
	URI        string // e.g. "seaweedfs://primary"
}

func (s *SeaweedFSSource) Name() string { return "seaweedfs" }

// seaweedFSWebhookPayload mirrors the fields the SeaweedFS webhook
// notifier publishes. Extra fields are tolerated (json.Decode is
// non-strict) so a SeaweedFS upgrade that adds new fields doesn't
// break ingest.
type seaweedFSWebhookPayload struct {
	Key         string `json:"key"`
	EventType   string `json:"event_type"`
	TimestampNs int64  `json:"timestamp_ns"`

	// Optional fields the gocdk webhook envelope may include.
	// Etag is best-effort — SeaweedFS reports it on create/update.
	Etag       string `json:"etag,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Sequencer  string `json:"sequencer,omitempty"`
	Collection string `json:"collection,omitempty"`
}

func (s *SeaweedFSSource) Parse(raw []byte, _ string) (CloudEvent, error) {
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

	subjFields, ok := parseSeaweedFSPath(p.Key, s.BucketName)
	if !ok {
		// Path didn't match the PALADIN layout — could be a legitimate
		// non-PALADIN file dropped into the same bucket. Ignore so we
		// don't fill the dedup table with junk.
		return CloudEvent{}, ErrIgnoredEvent
	}
	subjFields.Etag = p.Etag
	subjFields.SizeBytes = p.Size
	subjFields.Sequencer = p.Sequencer

	t := time.Unix(0, p.TimestampNs)
	if p.TimestampNs == 0 {
		t = time.Now()
	}

	return CloudEvent{
		SpecVersion:     "1.0",
		Type:            evType,
		Source:          s.URI,
		ID:              seaweedFSID(p.Key, p.EventType, p.TimestampNs),
		Time:            t,
		DataContentType: "application/json",
		Subject: fmt.Sprintf(
			"tenants/%s/objectKeys/%s/objects-by-key/%s",
			subjFields.TenantID, subjFields.ObjectKey, subjFields.Key,
		),
		Data:          raw,
		SubjectFields: subjFields,
	}, nil
}

// seaweedFSEventType maps SeaweedFS event_type values to the PALADIN
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

// parseSeaweedFSPath extracts (tenant_id, object_key, key) from the
// full filer path. Layout (matches s3adapter.composeKey):
//
//	/<bucket>/<tenant_uuid>/<object_key>/<key...>
//
// The leading "/" is optional (depends on SeaweedFS publisher
// configuration). Anything else returns ok=false.
func parseSeaweedFSPath(path, bucket string) (SubjectFields, bool) {
	trimmed := strings.TrimPrefix(path, "/")
	// SF's S3 gateway materialises bucket-rooted paths under
	// `/buckets/<bucket>/...` in the filer namespace. The webhook
	// notification driver emits the bare `<bucket>/...` shape, but
	// the gocdk_pub_sub-over-NATS path observes the full filer
	// namespace and includes the `buckets/` prefix. Strip it
	// upfront so the bucket-prefix check below works for both
	// publishers.
	trimmed = strings.TrimPrefix(trimmed, "buckets/")
	if bucket != "" {
		bp := strings.TrimPrefix(trimmed, bucket+"/")
		if bp == trimmed {
			// bucket prefix not present — wrong source or wrong
			// configuration. Treat as ignored.
			return SubjectFields{}, false
		}
		trimmed = bp
	}

	// trimmed is now: <tenant_uuid>/<object_key>/<key...>
	// Split into 3 parts max — key may contain / and we want it whole.
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 3 {
		return SubjectFields{}, false
	}

	return SubjectFields{
		TenantID:  parts[0],
		ObjectKey: parts[1],
		Key:       parts[2],
	}, true
}

// seaweedFSID hashes the canonical fields a duplicate event would
// share — same path + same event type + same timestamp = same id.
func seaweedFSID(key, evType string, ts int64) string {
	h := sha256.New()
	fmt.Fprintf(h, "seaweedfs|%s|%s|%d", key, evType, ts)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

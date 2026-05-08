package eventingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MinIO source adapter.
//
// Wire format: MinIO publishes object-lifecycle events as JSON in the
// AWS S3 event-notification shape (see
// https://min.io/docs/minio/linux/administration/monitoring/bucket-notifications.html).
// The `Records` envelope mirrors S3:
//
//   {
//     "EventName": "s3:ObjectCreated:Put",
//     "Records": [{
//       "eventSource": "minio:s3",
//       "eventTime":   "2026-01-01T00:00:00Z",
//       "eventName":   "s3:ObjectCreated:Put",
//       "s3": {
//         "bucket": { "name": "paladin-primary" },
//         "object": {
//           "key":       "tenant_uuid%2Fobject_key%2Fkey",
//           "size":      1024,
//           "eTag":      "abc",
//           "sequencer": "0017A53F..."
//         }
//       },
//       "responseElements": { "x-amz-request-id": "..." },
//       "userIdentity":     { "principalId": "..." }
//     }]
//   }
//
// We extract one CloudEvent per Records entry. ID derivation prefers
// the broker-supplied request-id; falls back to a hash of (key,
// eventName, sequencer) so retries dedup.

// MinIOSource parses MinIO event-notification bodies.
//
// MinIO URL-encodes object keys (slashes become %2F). We URL-decode
// before splitting into the PALADIN layout. BucketName is stripped only
// when the event's bucket matches it — useful for deployments that
// publish multiple bucket events into one queue, but we only act on
// the PALADIN-managed bucket.
type MinIOSource struct {
	BucketName string // matches s3.bucket.name
	URI        string // e.g. "minio://primary"
}

func (s *MinIOSource) Name() string { return "minio" }

// minioEnvelope is the top-level shape MinIO posts. Only the fields
// we need are decoded; extra keys are tolerated.
type minioEnvelope struct {
	EventName string        `json:"EventName,omitempty"`
	Records   []minioRecord `json:"Records"`
}

type minioRecord struct {
	EventName        string            `json:"eventName"`
	EventTime        string            `json:"eventTime"`
	S3               minioS3           `json:"s3"`
	ResponseElements map[string]string `json:"responseElements,omitempty"`
}

type minioS3 struct {
	Bucket minioBucket `json:"bucket"`
	Object minioObject `json:"object"`
}

type minioBucket struct {
	Name string `json:"name"`
}

type minioObject struct {
	Key       string `json:"key"`
	Size      int64  `json:"size"`
	ETag      string `json:"eTag"`
	Sequencer string `json:"sequencer"`
}

func (s *MinIOSource) Parse(raw []byte, _ string) (CloudEvent, error) {
	var env minioEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return CloudEvent{}, fmt.Errorf("%w: minio json: %v", ErrUnrecognisedEvent, err)
	}
	if len(env.Records) == 0 {
		return CloudEvent{}, ErrUnrecognisedEvent
	}

	// One envelope, one record in the typical case. If MinIO ever
	// batches multiple records, we take the first — the others
	// are still in `Data` for a downstream handler that wants
	// fan-out. (Future: emit one CloudEvent per record via a
	// batched-Parse method on Source.)
	r := env.Records[0]

	if s.BucketName != "" && r.S3.Bucket.Name != s.BucketName {
		// Event is for a different bucket — not ours, skip silently.
		return CloudEvent{}, ErrIgnoredEvent
	}

	evType, ok := minioEventType(r.EventName)
	if !ok {
		return CloudEvent{}, ErrIgnoredEvent
	}

	subjFields, ok := parseMinIOKey(r.S3.Object.Key)
	if !ok {
		return CloudEvent{}, ErrIgnoredEvent
	}
	subjFields.Etag = r.S3.Object.ETag
	subjFields.SizeBytes = r.S3.Object.Size
	subjFields.Sequencer = r.S3.Object.Sequencer

	t, err := time.Parse(time.RFC3339Nano, r.EventTime)
	if err != nil {
		t = time.Now()
	}

	id := minioID(r)

	return CloudEvent{
		SpecVersion:     "1.0",
		Type:            evType,
		Source:          s.URI,
		ID:              id,
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

// minioEventType maps MinIO eventName strings to the PALADIN taxonomy.
// Unknown / lifecycle-internal events return ok=false.
//
// MinIO event names follow the AWS S3 convention: namespace prefix
// (`s3:`) + verb (`ObjectCreated`, `ObjectRemoved`, `ObjectAccessed`)
// + sub-action (`Put`, `Post`, `Copy`, `Delete`, `DeleteMarkerCreated`).
// We collapse all "created" variants to one PALADIN event type because
// they all signal "object is now in S3"; same for removed.
func minioEventType(srcType string) (EventType, bool) {
	switch {
	case strings.HasPrefix(srcType, "s3:ObjectCreated:"):
		return EventTypeUploaded, true
	case strings.HasPrefix(srcType, "s3:ObjectRemoved:"):
		return EventTypeDeleted, true
	default:
		return "", false
	}
}

// parseMinIOKey extracts (tenant_id, object_key, key) from MinIO's
// URL-encoded object key. Layout matches s3adapter.composeKey but
// the slashes are URL-encoded as %2F by MinIO before publishing.
//
//	"<tenant_uuid>%2F<object_key>%2F<key...>"
//
// After decoding it's the same 3-segment SplitN as SeaweedFS.
func parseMinIOKey(key string) (SubjectFields, bool) {
	// Replace %2F → / so the SplitN matches. Other %-encoded chars
	// (spaces, unicode) MinIO leaves verbatim only in the value;
	// the slash is the discriminator.
	decoded := strings.ReplaceAll(key, "%2F", "/")
	decoded = strings.ReplaceAll(decoded, "%2f", "/")

	parts := strings.SplitN(decoded, "/", 3)
	if len(parts) < 3 {
		return SubjectFields{}, false
	}
	return SubjectFields{
		TenantID:  parts[0],
		ObjectKey: parts[1],
		Key:       parts[2],
	}, true
}

// minioID prefers the broker-issued x-amz-request-id when MinIO
// supplies one — that's the canonical MinIO-side identifier and any
// retry from the same notifier reuses it. Falls back to a content
// hash so a missing request-id doesn't break dedup.
func minioID(r minioRecord) string {
	if id := r.ResponseElements["x-amz-request-id"]; id != "" {
		return id
	}
	h := sha256.New()
	fmt.Fprintf(h, "minio|%s|%s|%s", r.S3.Object.Key, r.EventName, r.S3.Object.Sequencer)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

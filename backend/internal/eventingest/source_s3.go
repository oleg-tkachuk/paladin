package eventingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// S3 event-notification source adapter.
//
// Wire format: the AWS S3 event-notification JSON envelope — the canonical
// `Records[]` shape S3 delivers to SQS / SNS / Lambda / EventBridge. It is
// emitted verbatim by:
//
//   - AWS S3 (bucket notification → SQS/SNS/EventBridge)
//   - MinIO  (bucket notification → webhook / AMQP / Kafka) — MinIO
//     deliberately mirrors the AWS shape
//   - any other S3-compatible store that speaks S3 bucket notifications
//
//	{
//	  "Records": [{
//	    "eventSource": "aws:s3" | "minio:s3",
//	    "eventTime":   "2026-01-01T00:00:00.000Z",
//	    "eventName":   "s3:ObjectCreated:Put",
//	    "s3": {
//	      "bucket": { "name": "paladin-primary" },
//	      "object": {
//	        "key":       "<tenant_uuid>/<object_key>/<key...>",   // URL-encoded
//	        "size":      1024,
//	        "eTag":      "abc",
//	        "sequencer": "0017A53F..."
//	      }
//	    },
//	    "responseElements": { "x-amz-request-id": "..." }
//	  }]
//	}
//
// One CloudEvent is emitted per envelope (the first Records entry — see the
// batching note in Parse). The object key is bucket-RELATIVE (the bucket is
// carried separately in `s3.bucket.name`), so — unlike SeaweedFS' filer path
// — there is no bucket segment to strip; the key already starts at the Paladin
// 3-segment layout.
//
// NOTE ON GARAGE: Garage does NOT emit these. `Get/PutBucketNotification
// Configuration` are 501 Not Implemented and it has no non-S3 event mechanism
// either (verified against garagehq.deuxfleurs.fr S3-compatibility + features
// docs). Objects written directly to Garage are reconciled by the data-plane
// Reconciler, not this plane. See docs/storage-ingest.md.

// S3EventSource parses AWS-S3-shaped bucket-notification bodies. When
// BucketName is set only events for that bucket are accepted (a multi-bucket
// queue can be split into per-route adapters). URI is the source label stamped
// onto every emitted CloudEvent; Label overrides Name() for metrics/logs
// ("s3" by default, "minio" for the MinIO-flavoured wiring — the wire format
// is identical either way).
type S3EventSource struct {
	BucketName string
	URI        string
	Label      string
}

// MinIOSource is a backward-compatible alias: MinIO speaks the S3 event
// format, so the `minio` source_format + `/webhook/minio` route resolve to the
// same parser. Prefer S3EventSource in new wiring.
type MinIOSource = S3EventSource

func (s *S3EventSource) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return "s3"
}

// s3Envelope is the top-level notification shape. Only decoded fields are
// listed; extra keys (userIdentity, requestParameters, glacierEventData, …)
// are tolerated so a publisher-side addition doesn't break ingest.
type s3Envelope struct {
	Records []s3Record `json:"Records"`
}

type s3Record struct {
	EventSource      string            `json:"eventSource"`
	EventName        string            `json:"eventName"`
	EventTime        string            `json:"eventTime"`
	S3               s3Entity          `json:"s3"`
	ResponseElements map[string]string `json:"responseElements,omitempty"`
}

type s3Entity struct {
	Bucket s3Bucket `json:"bucket"`
	Object s3Object `json:"object"`
}

type s3Bucket struct {
	Name string `json:"name"`
}

type s3Object struct {
	Key       string `json:"key"`
	Size      int64  `json:"size"`
	ETag      string `json:"eTag"`
	Sequencer string `json:"sequencer"`
	VersionID string `json:"versionId,omitempty"`
}

func (s *S3EventSource) Parse(raw []byte, _ string) (CloudEvent, error) {
	var env s3Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return CloudEvent{}, fmt.Errorf("%w: s3 json: %w", ErrUnrecognisedEvent, err)
	}
	if len(env.Records) == 0 {
		return CloudEvent{}, ErrUnrecognisedEvent
	}

	// One envelope, one record in the typical case. If a publisher batches
	// multiple records we take the first; the rest are still in `Data` for a
	// handler that wants fan-out. (Future: a batched-Parse on Source emitting
	// one CloudEvent per record — tracked in BACKLOG.)
	r := env.Records[0]

	if s.BucketName != "" && r.S3.Bucket.Name != s.BucketName {
		// Event is for a different bucket — not ours, skip silently.
		return CloudEvent{}, ErrIgnoredEvent
	}

	evType, ok := s3EventType(r.EventName)
	if !ok {
		return CloudEvent{}, ErrIgnoredEvent
	}

	subj, ok := parseS3Key(r.S3.Object.Key)
	if !ok {
		// Not the Paladin layout — a non-Paladin object dropped in the same bucket.
		// Ignore so we don't fill the dedup table with junk.
		return CloudEvent{}, ErrIgnoredEvent
	}
	subj.Etag = r.S3.Object.ETag
	subj.SizeBytes = r.S3.Object.Size
	subj.Sequencer = r.S3.Object.Sequencer

	t, err := time.Parse(time.RFC3339Nano, r.EventTime)
	if err != nil {
		t = time.Now()
	}

	return CloudEvent{
		SpecVersion:     "1.0",
		Type:            evType,
		Source:          s.URI,
		ID:              s3ID(r),
		Time:            t,
		DataContentType: "application/json",
		Subject: fmt.Sprintf(
			"tenants/%s/objectKeys/%s/objects-by-key/%s",
			subj.TenantID, subj.ObjectKey, subj.Key,
		),
		Data:          raw,
		SubjectFields: subj,
	}, nil
}

// s3EventType maps S3 eventName strings to the Paladin taxonomy. All "created"
// variants (Put / Post / Copy / CompleteMultipartUpload) collapse to uploaded;
// all "removed" variants (Delete / DeleteMarkerCreated) collapse to deleted.
// ObjectAccessed / lifecycle-internal / replication events return ok=false.
func s3EventType(name string) (EventType, bool) {
	switch {
	case strings.HasPrefix(name, "s3:ObjectCreated:"):
		return EventTypeUploaded, true
	case strings.HasPrefix(name, "s3:ObjectRemoved:"):
		return EventTypeDeleted, true
	default:
		return "", false
	}
}

// parseS3Key URL-decodes the notification object key, then splits it into the
// Paladin 3-segment layout "<tenant_uuid>/<object_key>/<key...>".
//
// S3 event notifications form-encode the key: space → '+', '/' → '%2F', other
// bytes → '%XX' (AWS docs: "red flower.jpg" → "red+flower.jpg"). MinIO uses
// the same. url.QueryUnescape decodes all three ('+' and '%20' → space, '%2F'
// → '/', '%2B' → literal '+'), which is exactly the form-encoding S3 uses. A
// malformed sequence falls back to the raw key rather than dropping the event.
func parseS3Key(key string) (SubjectFields, bool) {
	decoded, err := url.QueryUnescape(key)
	if err != nil {
		decoded = key
	}
	parts := strings.SplitN(decoded, "/", 3)
	if len(parts) < 3 {
		return SubjectFields{}, false
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return SubjectFields{}, false
	}
	return SubjectFields{
		TenantID:  parts[0],
		ObjectKey: parts[1],
		Key:       parts[2],
	}, true
}

// s3ID prefers the broker-issued x-amz-request-id (AWS + MinIO both set it, and
// a retry from the same notifier reuses it). Falls back to a content hash of
// (key, eventName, sequencer) so a missing request-id still dedups across
// replays of the same physical event.
func s3ID(r s3Record) string {
	if id := r.ResponseElements["x-amz-request-id"]; id != "" {
		return id
	}
	h := sha256.New()
	fmt.Fprintf(h, "s3|%s|%s|%s", r.S3.Object.Key, r.EventName, r.S3.Object.Sequencer)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

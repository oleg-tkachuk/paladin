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
//	        "key":       "<tenant_uuid>/<collection>/<key...>",   // URL-encoded
//	        "size":      1024,
//	        "eTag":      "abc",
//	        "sequencer": "0017A53F..."
//	      }
//	    },
//	    "responseElements": { "x-amz-request-id": "..." }
//	  }]
//	}
//
// One CloudEvent is emitted per Records entry: a publisher may batch several
// into one notification, and DeleteObjects reports every key it removed under
// one request id. The object key is bucket-RELATIVE (the bucket is
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
	Records []json.RawMessage `json:"Records"`
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

// Parse is one CloudEvent per record of the notification, in order, each
// carrying its own record as Data. A record for another bucket, of an event
// Paladin does not act on, or outside the Paladin layout is skipped;
// ErrIgnoredEvent means every record was.
func (s *S3EventSource) Parse(raw []byte, _ string) ([]CloudEvent, error) {
	var env s3Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: s3 json: %w", ErrUnrecognisedEvent, err)
	}
	if len(env.Records) == 0 {
		return nil, ErrUnrecognisedEvent
	}
	evs := make([]CloudEvent, 0, len(env.Records))
	for i, rec := range env.Records {
		var r s3Record
		if err := json.Unmarshal(rec, &r); err != nil {
			return nil, fmt.Errorf("%w: s3 record %d: %w", ErrUnrecognisedEvent, i, err)
		}
		if ev, ok := s.event(r, rec); ok {
			evs = append(evs, ev)
		}
	}
	if len(evs) == 0 {
		return nil, ErrIgnoredEvent
	}
	return evs, nil
}

// event is the CloudEvent of one record, or false when Paladin does not act on
// it: a record for a different bucket, an event type outside the taxonomy, or
// a non-Paladin object dropped in the same bucket — ignored so it does not
// fill the dedup table with junk.
func (s *S3EventSource) event(r s3Record, raw json.RawMessage) (CloudEvent, bool) {
	if s.BucketName != "" && r.S3.Bucket.Name != s.BucketName {
		return CloudEvent{}, false
	}
	evType, ok := s3EventType(r.EventName)
	if !ok {
		return CloudEvent{}, false
	}
	subj, ok := parseStoragePath(r.S3.Object.Key)
	if !ok {
		return CloudEvent{}, false
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
			"tenants/%s/collections/%s/objects-by-key/%s",
			subj.TenantID, subj.Collection, subj.Key,
		),
		Data:          raw,
		SubjectFields: subj,
	}, true
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

// parseStoragePath URL-decodes the notification object key, then splits it into the
// Paladin 3-segment layout "<tenant_uuid>/<collection>/<key...>".
//
// S3 event notifications form-encode the key: space → '+', '/' → '%2F', other
// bytes → '%XX' (AWS docs: "red flower.jpg" → "red+flower.jpg"). MinIO uses
// the same. url.QueryUnescape decodes all three ('+' and '%20' → space, '%2F'
// → '/', '%2B' → literal '+'), which is exactly the form-encoding S3 uses. A
// malformed sequence falls back to the raw key rather than dropping the event.
func parseStoragePath(key string) (SubjectFields, bool) {
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
		TenantID:   parts[0],
		Collection: parts[1],
		Key:        parts[2],
	}, true
}

// s3IDLength is how many hex digits of the hash name an event.
const s3IDLength = 32

// s3ID names one record: a hash of the broker-issued x-amz-request-id (AWS and
// MinIO both set it, and a retry from the same notifier reuses it) with the
// object key, event name and sequencer. The request id alone is not enough:
// a DeleteObjects call reports every key it removed under one, and those
// records would dedup as one. Without a request id the rest still dedups
// replays of the same physical event.
func s3ID(r s3Record) string {
	h := sha256.New()
	fmt.Fprintf(h, "s3|%s|%s|%s|%s", r.ResponseElements["x-amz-request-id"], r.S3.Object.Key, r.EventName, r.S3.Object.Sequencer)
	return hex.EncodeToString(h.Sum(nil))[:s3IDLength]
}

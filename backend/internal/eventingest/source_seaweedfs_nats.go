package eventingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest/filerpb"
)

// SeaweedFS-over-NATS source adapter.
//
// Wire format mismatch with the existing SeaweedFSSource: that
// adapter parses SF's `[notification.webhook]` JSON. SeaweedFS'
// other publish path — `[notification.gocdk_pub_sub]` over a
// `nats://` topic URL — is what this cluster actually uses (the SF
// operator's `notification.toml` mount lands here; see
// `gitops/.../seaweedfs/notification-config.yaml`).
//
// The gocdk_pub_sub publisher serialises its payload as
// `proto.Marshal(*filer_pb.EventNotification)` and stuffs the
// filer path into a `key` metadata entry. gocloud.dev's natspubsub
// driver then *gob-encodes* both fields (metadata then body) into
// `nats.Msg.Data` because the legacy `nats://` URL scheme doesn't
// use NATS native headers — that's the `nats2://` v2 scheme. So
// what we receive here is `gob(map[string]string) || gob([]byte)`.
//
// The body decodes into filerpb's EventNotification, generated from the
// part of SeaweedFS's filer.proto ingest reads (see filerpb/filer.proto for
// why not upstream's package). Which entries it carries gives the Paladin
// event type:
//
//	   old=nil, new=present → create
//	   old=present, new=present → update
//	   old=present, new=nil → delete
//
// Reference: gocloud.dev/pubsub/natspubsub#encodeMessage (it's a
// gob.NewEncoder writing Metadata, then Body).

// SeaweedFSNATSSource is the adapter the NATS driver hands raw
// `msg.Data` to. URI is the source label; subscribers reading the
// resulting CloudEvent see this in the `source` attribute.
//
// BucketName, when non-empty, is stripped from the start of the
// SF path before tenant/collection/key splitting — same convention
// as the JSON source. Operators set it to the bucket the SF S3
// gateway publishes for; leaving it empty disables the strip and
// expects the path to start at the tenant id.
//
// Now is the clock an event's time and id fall back to when its entry
// records no version (entryVersion); nil is time.Now.
type SeaweedFSNATSSource struct {
	BucketName string
	URI        string
	Now        func() time.Time
}

// now is the source's clock.
func (s *SeaweedFSNATSSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *SeaweedFSNATSSource) Name() string { return "seaweedfs_nats" }

// Parse is the one event of a message: the format carries one.
func (s *SeaweedFSNATSSource) Parse(raw []byte, _ string) ([]CloudEvent, error) {
	return single(s.parseEvent(raw))
}

// parseEvent decodes a single gocdk_pub_sub-shaped NATS payload and maps
// it to a CloudEvent. Parse's contentType argument is unused — the
// `nats://` natspubsub driver doesn't propagate one.
func (s *SeaweedFSNATSSource) parseEvent(raw []byte) (CloudEvent, error) {
	metadata, body, err := decodeNATSEnvelope(raw)
	if err != nil {
		return CloudEvent{}, fmt.Errorf("%w: seaweedfs_nats: %w", ErrUnrecognisedEvent, err)
	}
	path := metadata["key"]
	if path == "" {
		// SF's gocdk_pub_sub always stamps the key. A missing one is
		// either a corrupted message or a wholly different publisher
		// reusing the same subject.
		return CloudEvent{}, ErrUnrecognisedEvent
	}

	var notification filerpb.EventNotification
	if err := proto.Unmarshal(body, &notification); err != nil {
		return CloudEvent{}, fmt.Errorf("%w: seaweedfs_nats: %w", ErrUnrecognisedEvent, err)
	}
	oldEntry, newEntry := notification.GetOldEntry(), notification.GetNewEntry()

	evType, ok := seaweedFSNATSEventType(oldEntry != nil, newEntry != nil)
	if !ok {
		// Both old and new absent — SF guards against this on its
		// side, but the empty notification slips through if the filer
		// signals a metadata-only no-op (rare, but observed). Ignore
		// rather than dead-letter — the dedup table doesn't need the
		// noise.
		return CloudEvent{}, ErrIgnoredEvent
	}
	// The entry the event is about: what was written, or what was removed.
	entry := newEntry
	if evType == EventTypeDeleted {
		entry = oldEntry
	}
	if entry.GetIsDirectory() {
		// The filer creates a directory for each prefix of a key; those
		// are events too, and are no Paladin object.
		return CloudEvent{}, ErrIgnoredEvent
	}

	subjFields, ok := parseSeaweedFSPath(path, s.BucketName)
	if !ok {
		// Path layout doesn't match `<bucket>/<tenant_uuid>/<collection>/<key>`
		// — could be a sibling write that lives in the same bucket
		// (audit logs, snapshots). Ignore silently per the JSON
		// source's convention.
		return CloudEvent{}, ErrIgnoredEvent
	}

	// The id hashes the path, the event type and the entry's version, as
	// the webhook source's does: a redelivery is the same event whenever it
	// arrives, and a second write of the key is another. An entry that
	// records no version falls back to the receipt minute: a redelivery
	// within it dedups, one in the next minute does not.
	attrs := entry.GetAttributes()
	version, t, ok := entryVersion(entry.GetCommitTsNs(), attrs.GetMtime(), attrs.GetMtimeNs(),
		attrs.GetFileSize(), attrs.GetMd5())
	if !ok {
		t = s.now()
		version = "received:" + strconv.FormatInt(t.Truncate(time.Minute).UnixNano(), 10)
	}
	t = t.UTC()
	id := seaweedFSNATSID(path, string(evType), version)

	return CloudEvent{
		SpecVersion:     "1.0",
		Type:            evType,
		Source:          s.URI,
		ID:              id,
		Time:            t,
		DataContentType: "application/octet-stream", // proto-marshalled body
		Subject: fmt.Sprintf(
			"tenants/%s/collections/%s/objects-by-key/%s",
			subjFields.TenantID, subjFields.Collection, subjFields.Key,
		),
		Data:          body,
		SubjectFields: subjFields,
	}, nil
}

// decodeNATSEnvelope mirrors gocloud.dev/pubsub/natspubsub#decodeMessage:
// the publisher writes `gob.Encode(metadata) || gob.Encode(body)` into
// `nats.Msg.Data`. We decode both halves in order.
func decodeNATSEnvelope(raw []byte) (metadata map[string]string, body []byte, err error) {
	dec := gob.NewDecoder(bytes.NewReader(raw)) // #nosec G709 -- decodes the internal SeaweedFS→NATS envelope into fixed types (map[string]string + []byte), not arbitrary user types
	if err := dec.Decode(&metadata); err != nil {
		return nil, nil, fmt.Errorf("decode metadata: %w", err)
	}
	if err := dec.Decode(&body); err != nil {
		return nil, nil, fmt.Errorf("decode body: %w", err)
	}
	return metadata, body, nil
}

func seaweedFSNATSEventType(hasOld, hasNew bool) (EventType, bool) {
	switch {
	case !hasOld && hasNew:
		return EventTypeUploaded, true
	case hasOld && hasNew:
		return EventTypeUploaded, true // update: same handler treatment as create
	case hasOld && !hasNew:
		return EventTypeDeleted, true
	default:
		return "", false
	}
}

// seaweedFSNATSID hashes the path, the event type and the entry's version
// (or, without one, the receipt minute).
func seaweedFSNATSID(path, evType, version string) string {
	h := sha256.New()
	fmt.Fprintf(h, "seaweedfs_nats|%s|%s|%s", strings.TrimPrefix(path, "/"), evType, version)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

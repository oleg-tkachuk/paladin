package eventingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
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
// Why we don't import the SF protobuf in full: `filer_pb.EventNotification`
// pulls in `Entry`, `FileChunk`, `FuseAttributes`, the whole filer
// metadata graph — hundreds of generated types we don't use. We only
// need to know whether the EventNotification carries an `old_entry`
// (tag 1) and / or a `new_entry` (tag 2) to derive the PALADIN event type:
//
//	   old=nil, new=present → create
//	   old=present, new=present → update
//	   old=present, new=nil → delete
//
// Tag-presence scanning via `protowire.ConsumeField` is enough; we
// avoid the proto-vendoring tax. If a future event type needs Entry
// fields we'd promote to a vendored proto then.
//
// Reference: gocloud.dev/pubsub/natspubsub#encodeMessage (it's a
// gob.NewEncoder writing Metadata, then Body).

// SeaweedFSNATSSource is the adapter the NATS driver hands raw
// `msg.Data` to. URI is the source label; subscribers reading the
// resulting CloudEvent see this in the `source` attribute.
//
// BucketName, when non-empty, is stripped from the start of the
// SF path before tenant/object_key/key splitting — same convention
// as the JSON source. Operators set it to the bucket the SF S3
// gateway publishes for; leaving it empty disables the strip and
// expects the path to start at the tenant id.
type SeaweedFSNATSSource struct {
	BucketName string
	URI        string
}

func (s *SeaweedFSNATSSource) Name() string { return "seaweedfs_nats" }

// Parse decodes a single gocdk_pub_sub-shaped NATS payload and maps
// it to a CloudEvent. The contentType argument is unused — the
// `nats://` natspubsub driver doesn't propagate one — but Source's
// signature requires it.
func (s *SeaweedFSNATSSource) Parse(raw []byte, _ string) (CloudEvent, error) {
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

	hasOld, hasNew, err := scanFilerEventNotification(body)
	if err != nil {
		return CloudEvent{}, fmt.Errorf("%w: seaweedfs_nats: %w", ErrUnrecognisedEvent, err)
	}

	evType, ok := seaweedFSNATSEventType(hasOld, hasNew)
	if !ok {
		// Both old and new absent — SF guards against this on its
		// side, but the empty notification slips through if the filer
		// signals a metadata-only no-op (rare, but observed). Ignore
		// rather than dead-letter — the dedup table doesn't need the
		// noise.
		return CloudEvent{}, ErrIgnoredEvent
	}

	subjFields, ok := parseSeaweedFSPath(path, s.BucketName)
	if !ok {
		// Path layout doesn't match `<bucket>/<tenant_uuid>/<object_key>/<key>`
		// — could be a sibling write that lives in the same bucket
		// (audit logs, snapshots). Ignore silently per the JSON
		// source's convention.
		return CloudEvent{}, ErrIgnoredEvent
	}

	// SF's EventNotification body has no timestamp — the parent
	// SubscribeMetadataResponse carries TsNs but gocdk_pub_sub
	// publishes only the inner EventNotification. Use receipt time
	// (now) for the CloudEvent. ID derivation hashes the path +
	// event type + a coarse minute bucket so retries within the same
	// minute dedup; redeliveries that span minutes do show up as
	// distinct events, but the ingest dedup window catches them.
	t := time.Now().UTC()
	id := seaweedFSNATSID(path, string(evType), t.Truncate(time.Minute).Unix())

	return CloudEvent{
		SpecVersion:     "1.0",
		Type:            evType,
		Source:          s.URI,
		ID:              id,
		Time:            t,
		DataContentType: "application/octet-stream", // proto-marshalled body
		Subject: fmt.Sprintf(
			"tenants/%s/objectKeys/%s/objects-by-key/%s",
			subjFields.TenantID, subjFields.ObjectKey, subjFields.Key,
		),
		Data:          body,
		SubjectFields: subjFields,
	}, nil
}

// decodeNATSEnvelope mirrors gocloud.dev/pubsub/natspubsub#decodeMessage:
// the publisher writes `gob.Encode(metadata) || gob.Encode(body)` into
// `nats.Msg.Data`. We decode both halves in order.
func decodeNATSEnvelope(raw []byte) (metadata map[string]string, body []byte, err error) {
	dec := gob.NewDecoder(bytes.NewReader(raw)) //nolint:gosec // G709: decodes the internal SeaweedFS→NATS envelope into fixed types (map[string]string + []byte), not arbitrary user types
	if err := dec.Decode(&metadata); err != nil {
		return nil, nil, fmt.Errorf("decode metadata: %w", err)
	}
	if err := dec.Decode(&body); err != nil {
		return nil, nil, fmt.Errorf("decode body: %w", err)
	}
	return metadata, body, nil
}

// scanFilerEventNotification walks the proto-marshalled body and
// reports whether the `old_entry` (field 1) and/or `new_entry`
// (field 2) wire-tags are present. We don't decode Entry — its
// presence is enough to derive create/update/delete.
//
// Ignores all other fields; an unknown tag (a future SF release
// adding new fields) is consumed and dropped silently.
//
// `protowire.ConsumeTag` returns the field number and wire type as
// separate values; `ConsumeFieldValue` then needs the wire type to
// know how many bytes to consume. We pull the tag's low 3 bits
// directly from the leading byte rather than re-reading via the
// helper — one less varint walk per field on a hot path.
func scanFilerEventNotification(body []byte) (hasOld, hasNew bool, err error) {
	for len(body) > 0 {
		num, _, tagLen := protowire.ConsumeTag(body)
		if tagLen < 0 {
			return false, false, fmt.Errorf("proto: tag: %w", protowire.ParseError(tagLen))
		}
		wireType := protowire.Type(body[0] & 0x07)
		valLen := protowire.ConsumeFieldValue(num, wireType, body[tagLen:])
		if valLen < 0 {
			return false, false, fmt.Errorf("proto: value: %w", protowire.ParseError(valLen))
		}
		switch num {
		case 1:
			hasOld = true
		case 2:
			hasNew = true
		}
		body = body[tagLen+valLen:]
	}
	return hasOld, hasNew, nil
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

// seaweedFSNATSID hashes (path + event type + minute-bucket) so a
// redelivery within the same minute produces the same id and gets
// caught by the dedup window. Cross-minute retries are uncommon;
// the broader ingest dedup table handles them.
func seaweedFSNATSID(path, evType string, minute int64) string {
	h := sha256.New()
	fmt.Fprintf(h, "seaweedfs_nats|%s|%s|%d", strings.TrimPrefix(path, "/"), evType, minute)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

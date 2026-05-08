package eventingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CloudEventsSource is the vendor-neutral adapter: any publisher
// emitting a CloudEvents 1.0 envelope (https://github.com/cloudevents
// /spec) can wire into the ingest plane without a bespoke parser.
// Useful for:
//
//   - Operators with a Knative / Kafka / NATS-JetStream pipeline that
//     already speaks CloudEvents and want PALADIN downstream.
//   - In-house publishers (a custom replication agent, a partner
//     control plane) that prefer the standard envelope to inventing
//     their own.
//
// Mode: structured-content only. JSON body decoded as a CloudEvents
// envelope. Binary-content mode (CE attributes in HTTP headers) is
// out of scope here — the webhook driver hands raw body bytes to
// Source.Parse and doesn't surface headers; if a publisher uses
// binary mode, it can wrap the body in the structured envelope
// before posting.
//
// Type taxonomy: only events whose `type` matches the PALADIN namespace
// (`paladin.object.uploaded`, `paladin.object.deleted`) are acted on. Other
// types — even valid CloudEvents — return ErrIgnoredEvent. This
// keeps the dedup table from filling with unrelated events on a
// shared bus.
//
// Subject parsing: relies on a `subject` field formatted as either:
//
//   tenants/<tenant_uuid>/objectKeys/<ok>/objects-by-key/<key>
//
// (the PALADIN canonical form, what SeaweedFSSource / MinIOSource emit)
// or as a plain `<tenant_uuid>/<object_key>/<key>` triple. The
// parser tries the structured form first, falls back to the bare
// triple. Empty `subject` → ErrIgnoredEvent.
//
// Optional CE extension attributes the adapter understands:
//
//   paladinetag:        copied to SubjectFields.Etag
//   paladinsize:        parsed as int64, copied to SubjectFields.SizeBytes
//   paladinsequencer:   copied to SubjectFields.Sequencer
//
// (CloudEvents extension attributes are vendor-neutral; the
// `paladin` prefix follows the spec's lowercase-only-letters rule.)

// CloudEventsSource parses CloudEvents 1.0 structured-mode JSON.
// URI is the source label stamped on every emitted event when the
// envelope's own `source` is empty (it usually isn't — publishers
// stamp their own URI).
type CloudEventsSource struct {
	URI string // fallback source label, e.g. "cloudevents://incoming"
}

func (s *CloudEventsSource) Name() string { return "cloudevents" }

// cloudEventEnvelope mirrors the CE 1.0 attributes we read. Extra
// attributes (extensions, vendor fields) flow into the underscore
// map for reading via known keys; unknown keys are tolerated.
type cloudEventEnvelope struct {
	SpecVersion     string `json:"specversion"`
	Type            string `json:"type"`
	Source          string `json:"source"`
	ID              string `json:"id"`
	Time            string `json:"time,omitempty"`
	DataContentType string `json:"datacontenttype,omitempty"`
	Subject         string `json:"subject,omitempty"`

	// Extension attributes we honour.
	PALADINEtag      string `json:"paladinetag,omitempty"`
	PALADINSize      int64  `json:"paladinsize,omitempty"`
	PALADINSequencer string `json:"paladinsequencer,omitempty"`

	// Data is preserved for handlers that want native fields.
	Data json.RawMessage `json:"data,omitempty"`
}

func (s *CloudEventsSource) Parse(raw []byte, _ string) (CloudEvent, error) {
	var env cloudEventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return CloudEvent{}, fmt.Errorf("%w: cloudevents json: %v", ErrUnrecognisedEvent, err)
	}

	// CE 1.0 mandates specversion + type + source + id. Reject
	// anything missing one of those — the publisher is buggy and
	// downstream guarantees (dedup, type-keyed handler dispatch)
	// would be unsound.
	if env.SpecVersion == "" || env.Type == "" || env.Source == "" || env.ID == "" {
		return CloudEvent{}, fmt.Errorf(
			"%w: cloudevents missing required attribute (specversion=%q type=%q source=%q id=%q)",
			ErrUnrecognisedEvent, env.SpecVersion, env.Type, env.Source, env.ID,
		)
	}

	// Type filter: act only on the PALADIN taxonomy. Unknown types are
	// ignored without erroring (shared-bus tolerance).
	var evType EventType
	switch env.Type {
	case string(EventTypeUploaded):
		evType = EventTypeUploaded
	case string(EventTypeDeleted):
		evType = EventTypeDeleted
	default:
		return CloudEvent{}, ErrIgnoredEvent
	}

	subjFields, ok := parseCloudEventsSubject(env.Subject)
	if !ok {
		return CloudEvent{}, ErrIgnoredEvent
	}
	subjFields.Etag = env.PALADINEtag
	subjFields.SizeBytes = env.PALADINSize
	subjFields.Sequencer = env.PALADINSequencer

	t, err := time.Parse(time.RFC3339Nano, env.Time)
	if err != nil {
		t = time.Now()
	}

	id := env.ID
	if id == "" {
		// Defence in depth — already required above, but if a future
		// schema change relaxes that, fall back to a content hash so
		// dedup stays sound.
		h := sha256.New()
		fmt.Fprintf(h, "cloudevents|%s|%s|%s", env.Source, env.Subject, env.Type)
		id = hex.EncodeToString(h.Sum(nil))[:32]
	}

	source := env.Source
	if source == "" {
		source = s.URI
	}

	return CloudEvent{
		SpecVersion:     env.SpecVersion,
		Type:            evType,
		Source:          source,
		ID:              id,
		Time:            t,
		DataContentType: env.DataContentType,
		Subject:         env.Subject,
		Data:            env.Data,
		SubjectFields:   subjFields,
	}, nil
}

// parseCloudEventsSubject accepts two forms:
//
//  1. PALADIN canonical:
//     tenants/<uuid>/objectKeys/<ok>/objects-by-key/<key...>
//  2. Bare triple:
//     <uuid>/<object_key>/<key...>
//
// Both yield the same SubjectFields. Anything else returns ok=false
// and the caller treats the event as ignored.
func parseCloudEventsSubject(subject string) (SubjectFields, bool) {
	if subject == "" {
		return SubjectFields{}, false
	}
	// Form 1: tenants/<id>/objectKeys/<ok>/objects-by-key/<key>.
	const pTenants = "tenants/"
	const pObjectKeys = "/objectKeys/"
	const pObjsByKey = "/objects-by-key/"
	if strings.HasPrefix(subject, pTenants) {
		rest := subject[len(pTenants):]
		idx := strings.Index(rest, pObjectKeys)
		if idx < 0 {
			return SubjectFields{}, false
		}
		tenantID := rest[:idx]
		rest = rest[idx+len(pObjectKeys):]
		idx = strings.Index(rest, pObjsByKey)
		if idx < 0 {
			return SubjectFields{}, false
		}
		objectKey := rest[:idx]
		key := rest[idx+len(pObjsByKey):]
		if tenantID == "" || objectKey == "" || key == "" {
			return SubjectFields{}, false
		}
		return SubjectFields{TenantID: tenantID, ObjectKey: objectKey, Key: key}, true
	}

	// Form 2: bare <uuid>/<ok>/<key...>
	parts := strings.SplitN(subject, "/", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return SubjectFields{}, false
	}
	return SubjectFields{TenantID: parts[0], ObjectKey: parts[1], Key: parts[2]}, true
}

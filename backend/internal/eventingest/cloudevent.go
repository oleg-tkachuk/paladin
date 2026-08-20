// Package eventingest is Paladin's storage-event consumer.
//
// External storage backends (SeaweedFS, MinIO) publish object-lifecycle
// events as a file is uploaded / deleted / moved. The ingest plane
// receives those events, normalises them to a CloudEvents 1.0 envelope,
// and dispatches to the handler — typically promoting a Paladin row from
// PENDING → AVAILABLE.
//
// Architecture (ports & adapters):
//
//	Driver (transport)         Source (format)
//	  ─ webhook (HTTP)            ─ seaweedfs
//	  ─ nats                      ─ minio
//	  ─ rabbitmq                  ─ cloudevents (passthrough)
//	         \                      /
//	          ─→ Worker.Dispatch ←─
//	                  │
//	                  ▼
//	          dedup + Handler
//	                  │
//	                  ▼
//	     statemachine.PromoteToAvailable
//
// At-least-once + dedup: every event passes through the
// `ingested_events` table by id before the handler runs.
package eventingest

import (
	"errors"
	"time"
)

// EventType is the normalised internal type taxonomy. Source adapters
// map their native event names into one of these values; downstream
// handlers branch on EventType.
type EventType string

const (
	// EventTypeUploaded fires when a new object body has been
	// committed to the storage backend. Maps to the Paladin transition
	// PENDING → AVAILABLE.
	EventTypeUploaded EventType = "paladin.object.uploaded"

	// EventTypeDeleted fires when an object body has been removed
	// from the storage backend. Used to catch soft-delete cascades
	// triggered out-of-band (operator deleted via S3 directly).
	EventTypeDeleted EventType = "paladin.object.deleted"
)

// CloudEvent is the internal envelope. Mirrors the relevant subset of
// CloudEvents 1.0 (https://github.com/cloudevents/spec) — `specversion`,
// `type`, `source`, `id`, `time`, `datacontenttype`, `subject`, `data`.
//
// `id` is load-bearing for dedup: the worker writes (id, source, type)
// to ingested_events as a unique-key insert. Source adapters must
// pick an id that is stable across replays of the same physical
// underlying event (S3 sequencer, MQ message-id, hash of subject+
// timestamp+seq — adapter's choice, just be deterministic).
type CloudEvent struct {
	SpecVersion     string    // always "1.0"
	Type            EventType // paladin.object.uploaded | paladin.object.deleted
	Source          string    // e.g. "seaweedfs://primary"
	ID              string    // unique per logical event; drives dedup
	Time            time.Time // when the source emitted the event
	DataContentType string    // typically "application/json"
	Subject         string    // canonical resource ref — see SubjectFields

	// Data is the source-specific payload, opaque to the worker but
	// available to the handler if it wants to reach back into native
	// fields (etag, size, sequencer). Source adapters populate it
	// with the raw bytes they parsed from.
	Data []byte

	// Fields decoded from Subject. The handler reads these instead of
	// re-parsing Subject; source adapters populate them once.
	SubjectFields SubjectFields
}

// SubjectFields is the parsed view of the Paladin resource the event
// refers to. The handler resolves an object_id from this tuple and
// promotes the matching row.
//
// At least TenantID + Collection + Key must be present for the handler
// to act; if any is missing the event is logged and dropped (with a
// dedup row written so we don't reprocess the same garbage).
type SubjectFields struct {
	TenantID   string // UUID; "" if the source key didn't include one
	Collection string // logical namespace
	Key        string // intra-namespace key
	Etag       string // S3 etag, when source provides it
	SizeBytes  int64  // when source provides it
	Sequencer  string // S3 sequencer for ordering, when source provides it
}

// ErrUnrecognisedEvent is returned by source adapters when the payload
// doesn't match any known shape. The worker logs the body length and
// the source URI so the operator can inspect the publisher.
var ErrUnrecognisedEvent = errors.New("eventingest: unrecognised event payload")

// ErrIgnoredEvent is returned by source adapters for events we know
// about but don't act on (e.g. SeaweedFS chunk upload, intermediate
// state changes). Worker treats it as success and skips dedup write
// to avoid filling the dedup table with no-op markers.
var ErrIgnoredEvent = errors.New("eventingest: event ignored by adapter")

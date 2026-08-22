// Package codec supplies the JSON codec the Connect handlers decode requests
// with.
//
// Connect's built-in JSON codec discards fields the message does not declare,
// so a client typo is indistinguishable from omission. That is a deliberate
// upstream choice — it lets a new client talk to an old server during a
// rolling deploy — but it costs more here than it buys. Every Paladin client is
// generated from the same protos and shipped in the same release as the server
// (one image + chart version), so the skew window the leniency protects is
// effectively absent, while the failure it creates is silent and expensive:
//
//	Login with {"audience": "paladin-admin"} — the field is actually
//	requested_audience — returned 200 and a token for the DEFAULT audience.
//	Every admin call made with it then failed as "jwt: audience mismatch",
//	four hops from the typo that caused it.
//
// Worse, a discarded field can drop a guard rather than merely change a value:
// resource_version is the OCC token, and an empty one means "skip the check"
// (see api/v1/object.Handler). A misspelled resource_version turns a
// conditional update into a blind overwrite, with a 200 either way.
//
// StrictJSON rejects unknown fields instead. Note what this does and does not
// buy: it catches "I sent the wrong field name", not "I sent no field at all".
// Guards that must not be skippable have to be required in the schema — the
// codec cannot infer intent from silence.
package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Name is the codec name. It must be "json" to displace Connect's built-in
// codec: handlers select a codec by the name in the Content-Type, so
// registering under any other name would leave the lenient one in place for
// application/json and this one unreachable.
const Name = "json"

// StrictJSON is a drop-in replacement for Connect's protoJSONCodec that fails
// on unknown request fields.
//
// It implements Codec plus the two optional extensions Connect probes for —
// marshalAppender (envelope writes reuse a buffer) and stableCodec (required
// for the Connect protocol's idempotent GET encoding). Implementing Codec
// alone would compile and serve, then silently drop GET support and an
// allocation optimisation, so the extras are not optional in practice.
type StrictJSON struct{}

func (StrictJSON) Name() string { return Name }

func (StrictJSON) IsBinary() bool { return false }

func (StrictJSON) Marshal(message any) ([]byte, error) {
	m, ok := message.(proto.Message)
	if !ok {
		return nil, errNotProto(message)
	}
	return protojson.MarshalOptions{}.Marshal(m)
}

func (StrictJSON) MarshalAppend(dst []byte, message any) ([]byte, error) {
	m, ok := message.(proto.Message)
	if !ok {
		return nil, errNotProto(message)
	}
	return protojson.MarshalOptions{}.MarshalAppend(dst, m)
}

// Unmarshal is the whole point of this type: DiscardUnknown is false.
func (StrictJSON) Unmarshal(binary []byte, message any) error {
	m, ok := message.(proto.Message)
	if !ok {
		return errNotProto(message)
	}
	if len(binary) == 0 {
		return errors.New("zero-length payload is not a valid JSON object")
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(binary, m); err != nil {
		return fmt.Errorf("unmarshal into %T: %w", message, err)
	}
	return nil
}

// MarshalStable mirrors the built-in codec: protojson emits inconsistent
// whitespace, so the output is compacted to make it byte-stable.
func (c StrictJSON) MarshalStable(message any) ([]byte, error) {
	b, err := c.Marshal(message)
	if err != nil {
		return nil, err
	}
	compacted := bytes.NewBuffer(b[:0])
	if err := json.Compact(compacted, b); err != nil {
		return nil, err
	}
	return compacted.Bytes(), nil
}

func errNotProto(message any) error {
	if _, ok := message.(protoiface); ok {
		return fmt.Errorf("%T uses github.com/golang/protobuf, but connect-go only supports google.golang.org/protobuf: see https://go.dev/blog/protobuf-apiv2", message)
	}
	return fmt.Errorf("%T doesn't implement proto.Message", message)
}

// protoiface matches the legacy github.com/golang/protobuf message interface,
// so the error above can name the actual problem instead of "not a message".
type protoiface interface {
	Reset()
	String() string
	ProtoMessage()
}

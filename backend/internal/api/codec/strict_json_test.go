package codec

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	connect "connectrpc.com/connect"
	iamv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/iam/v1"
	"google.golang.org/protobuf/proto"
)

// TestStrictJSONRejectsUnknownField is the behaviour the whole package exists
// for. The field name is the real one from the incident: requested_audience
// mistyped as audience, which the default codec discarded — returning a token
// for the default audience with a 200.
func TestStrictJSONRejectsUnknownField(t *testing.T) {
	t.Parallel()

	var msg iamv1.LoginRequest
	err := StrictJSON{}.Unmarshal([]byte(`{"subject":"admin","audience":"paladin-admin"}`), &msg)
	if err == nil {
		t.Fatalf("Unmarshal accepted an unknown field; decoded %+v", &msg)
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("error %q does not name the offending field", err)
	}
}

// TestStrictJSONAcceptsKnownFields pins that strictness did not become
// brittleness: both JSON spellings protojson accepts must still work, since
// generated clients emit camelCase and hand-written callers often use the proto
// field name.
func TestStrictJSONAcceptsKnownFields(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		`{"subject":"admin","requested_audience":"paladin-admin"}`,
		`{"subject":"admin","requestedAudience":"paladin-admin"}`,
	} {
		var msg iamv1.LoginRequest
		if err := (StrictJSON{}).Unmarshal([]byte(in), &msg); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		if msg.GetSubject() != "admin" || msg.GetRequestedAudience() != "paladin-admin" {
			t.Errorf("decoded %+v, want subject=admin audience=paladin-admin", &msg)
		}
	}
}

// TestStrictJSONOmittedFieldIsStillFine is the limit of what this codec buys.
// A caller that sends nothing is making a valid request — the codec cannot
// distinguish "deliberately omitted" from "forgot". Guards that must not be
// skippable have to be required in the schema; this test exists so that
// distinction stays explicit rather than assumed.
func TestStrictJSONOmittedFieldIsStillFine(t *testing.T) {
	t.Parallel()

	var msg iamv1.LoginRequest
	if err := (StrictJSON{}).Unmarshal([]byte(`{"subject":"admin"}`), &msg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if msg.GetRequestedAudience() != "" {
		t.Errorf("requested_audience = %q, want empty", msg.GetRequestedAudience())
	}
}

func TestStrictJSONEmptyPayload(t *testing.T) {
	t.Parallel()

	var msg iamv1.LoginRequest
	if err := (StrictJSON{}).Unmarshal(nil, &msg); err == nil {
		t.Fatal("Unmarshal accepted a zero-length payload")
	}
}

func TestStrictJSONNonProtoMessage(t *testing.T) {
	t.Parallel()

	var notAMessage struct{ A int }
	if err := (StrictJSON{}).Unmarshal([]byte(`{"a":1}`), &notAMessage); err == nil {
		t.Fatal("Unmarshal accepted a non-proto destination")
	}
	if _, err := (StrictJSON{}).Marshal(&notAMessage); err == nil {
		t.Fatal("Marshal accepted a non-proto message")
	}
	if _, err := (StrictJSON{}).MarshalAppend(nil, &notAMessage); err == nil {
		t.Fatal("MarshalAppend accepted a non-proto message")
	}
}

// TestStrictJSONRoundTrip covers the marshal side, including the two optional
// extensions Connect probes for. MarshalStable must be compact — the Connect
// protocol puts it in a GET query parameter.
func TestStrictJSONRoundTrip(t *testing.T) {
	t.Parallel()

	in := &iamv1.LoginRequest{Subject: "admin", RequestedAudience: "paladin-admin"}
	c := StrictJSON{}

	b, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out iamv1.LoginRequest
	if err := c.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal of our own output: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Errorf("round trip changed the message: %v -> %v", in, &out)
	}

	prefix := []byte("PREFIX")
	appended, err := c.MarshalAppend(prefix, in)
	if err != nil {
		t.Fatalf("MarshalAppend: %v", err)
	}
	if !strings.HasPrefix(string(appended), "PREFIX") {
		t.Errorf("MarshalAppend discarded dst: %q", appended)
	}

	stable, err := c.MarshalStable(in)
	if err != nil {
		t.Fatalf("MarshalStable: %v", err)
	}
	if !json.Valid(stable) {
		t.Errorf("MarshalStable produced invalid JSON: %q", stable)
	}
	var compact []byte
	if compact, err = compactJSON(stable); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if string(compact) != string(stable) {
		t.Errorf("MarshalStable output is not compact: %q", stable)
	}
}

func compactJSON(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TestStrictJSONSatisfiesConnectCodec fails at compile time if the type ever
// stops being a valid Connect codec.
func TestStrictJSONSatisfiesConnectCodec(t *testing.T) {
	t.Parallel()

	var c connect.Codec = StrictJSON{}
	if c.Name() != "json" {
		t.Fatalf("Name() = %q — must be \"json\" to displace the built-in codec "+
			"for application/json requests", c.Name())
	}
}

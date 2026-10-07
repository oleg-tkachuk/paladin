package middleware

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// The interceptor read only the header. Two request messages declare an
// idempotency_key field, and a client that set it — reasonably, the schema
// offers it — got no idempotency and no sign of that: a retried
// InitiateMultipartUpload opened a second session.
func TestIdempotencyKeyResolution(t *testing.T) {
	t.Parallel()

	newReq := func(header, body string) (*connect.Header, proto.Message) {
		headers := &connect.Header{}
		if header != "" {
			headers.Set(idempotencyHeader, header)
		}
		return headers, &datav1.InitiateMultipartUploadRequest{IdempotencyKey: body}
	}

	t.Run("header alone", func(t *testing.T) {
		got, err := idempotencyKey(newReq("h-1", ""))
		if err != nil || got != "h-1" {
			t.Fatalf("got %q, %v; want h-1", got, err)
		}
	})

	t.Run("body field alone is honoured", func(t *testing.T) {
		got, err := idempotencyKey(newReq("", "b-1"))
		if err != nil || got != "b-1" {
			t.Fatalf("got %q, %v; want b-1 — the body field must work, not just parse", got, err)
		}
	})

	t.Run("both, agreeing", func(t *testing.T) {
		got, err := idempotencyKey(newReq("same", "same"))
		if err != nil || got != "same" {
			t.Fatalf("got %q, %v; want same", got, err)
		}
	})

	t.Run("both, disagreeing is refused", func(t *testing.T) {
		_, err := idempotencyKey(newReq("h-1", "b-1"))
		if err == nil {
			t.Fatal("conflicting keys accepted; the effective key would depend on an undocumented precedence")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
		}
		if !strings.Contains(err.Error(), "disagree") {
			t.Errorf("error = %q, want it to say the two disagree", err)
		}
	})

	t.Run("neither", func(t *testing.T) {
		got, err := idempotencyKey(newReq("", ""))
		if err != nil || got != "" {
			t.Fatalf("got %q, %v; want empty", got, err)
		}
	})

	t.Run("message without the field falls back to the header", func(t *testing.T) {
		headers := &connect.Header{}
		headers.Set(idempotencyHeader, "h-2")
		got, err := idempotencyKey(headers, &datav1.GetObjectRequest{})
		if err != nil || got != "h-2" {
			t.Fatalf("got %q, %v; want h-2", got, err)
		}
	})
}

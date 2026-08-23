package middleware

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	datav1 "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
)

// The interceptor read only the header. Two request messages declare an
// idempotency_key field, and a client that set it — reasonably, the schema
// offers it — got no idempotency and no sign of that: a retried
// InitiateMultipartUpload opened a second session.
func TestIdempotencyKeyResolution(t *testing.T) {
	t.Parallel()

	newReq := func(header, body string) connect.AnyRequest {
		msg := &datav1.InitiateMultipartUploadRequest{IdempotencyKey: body}
		r := connect.NewRequest(msg)
		if header != "" {
			r.Header().Set(idempotencyHeader, header)
		}
		return r
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
		r := connect.NewRequest(&datav1.GetObjectRequest{})
		r.Header().Set(idempotencyHeader, "h-2")
		got, err := idempotencyKey(r)
		if err != nil || got != "h-2" {
			t.Fatalf("got %q, %v; want h-2", got, err)
		}
	})
}

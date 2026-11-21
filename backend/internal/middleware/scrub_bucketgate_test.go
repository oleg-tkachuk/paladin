package middleware

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// A bucket gate refusal reaches the caller as the handler answered it: its
// code, its message and the ErrorInfo reason the console reads. UNAVAILABLE
// is not a code the scrub rewrites, so a provisioning refusal keeps all three.
func TestScrubKeepsTheBucketGateRefusal(t *testing.T) {
	for _, r := range bucketgate.Refusals() {
		t.Run(r.Error(), func(t *testing.T) {
			var out *connect.Error
			if !errors.As(scrub(objecth.MapResolveErr(r), "req-1"), &out) {
				t.Fatal("not a connect error")
			}
			if out.Code() != r.Code || out.Message() != r.Error() {
				t.Fatalf("scrubbed = (%v, %q), want (%v, %q)", out.Code(), out.Message(), r.Code, r.Error())
			}
			var reason string
			for _, d := range out.Details() {
				v, err := connectproto.UnmarshalErrorDetail(d)
				if err != nil {
					t.Fatalf("detail does not decode: %v", err)
				}
				if info, ok := v.(*errdetails.ErrorInfo); ok {
					reason = info.GetReason()
				}
			}
			if reason != r.Reason.String() {
				t.Fatalf("reason = %q, want %q", reason, r.Reason)
			}
		})
	}
}

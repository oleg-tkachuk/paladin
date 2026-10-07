package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// leak is what a store failure carried to the caller before: the driver's
// error, with the table and the constraint it violated.
const leak = `capability/postgres: insert: ERROR: insert or update on table "capability_records" violates foreign key constraint "capability_records_tenant_id_fkey" (SQLSTATE 23503)`

func TestScrubKeepsTheCodeAndDropsTheMessage(t *testing.T) {
	const id = "req-42"
	for _, tc := range []struct {
		code   connect.Code
		scrubs bool
	}{
		{connect.CodeInternal, true},
		{connect.CodeUnknown, true},
		{connect.CodeDataLoss, true},
		{connect.CodeInvalidArgument, false},
		{connect.CodeNotFound, false},
		{connect.CodeUnavailable, false},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			got := scrub(connect.NewError(tc.code, leak), id)
			if connect.CodeOf(got) != tc.code {
				t.Fatalf("code = %v, want %v", connect.CodeOf(got), tc.code)
			}
			if leaked := strings.Contains(got.Error(), "SQLSTATE"); leaked == tc.scrubs {
				t.Errorf("message %q: scrubbed = %v, want %v", got.Error(), !leaked, tc.scrubs)
			}
			if tc.scrubs && !strings.Contains(got.Error(), id) {
				t.Errorf("message %q does not name request id %q", got.Error(), id)
			}
		})
	}
}

// Details are kept on the error. Metadata — Retry-After and the like — is the
// response's headers, on the call's CallInfo rather than the error, and is
// asserted through a handler below.
func TestScrubKeepsDetails(t *testing.T) {
	detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: "SOME_REASON", Domain: "paladin"})
	if err != nil {
		t.Fatal(err)
	}
	in := connect.NewError(connect.CodeInternal, leak).WithDetail(detail)
	var out *connect.Error
	if !errors.As(scrub(in, "req-1"), &out) {
		t.Fatal("not a connect error")
	}
	if len(out.Details()) != 1 {
		t.Errorf("details %d, want 1", len(out.Details()))
	}
}

func TestScrubLeavesSuccessAlone(t *testing.T) {
	if scrub(nil, "req-1") != nil {
		t.Error("a nil error became one")
	}
}

// A plain error from a handler — not a *connect.Error — is Unknown to
// connect, and is scrubbed like Internal.
func TestScrubAPlainError(t *testing.T) {
	got := scrub(errors.New(leak), "req-1")
	if connect.CodeOf(got) != connect.CodeUnknown || strings.Contains(got.Error(), "SQLSTATE") {
		t.Errorf("got %v", got)
	}
}

// Through a real handler: the interceptor outermost, LogOutcome's place
// inside it seeing the original, the caller the scrubbed one, and both the
// same request id — minted when the caller sent none. The response headers
// the handler set reach the caller alongside the id.
func TestScrubInternalThroughAHandler(t *testing.T) {
	const retryAfter = "3"
	var innerErr error
	var innerID string
	inner := connect.ServerInterceptor(func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, stream)
			innerErr, innerID = err, unary.Info(ctx).RequestHeader().Get(HeaderRequestID)
			return err
		}
	})
	probe := &unarytest.Probe{OnCall: func(ctx context.Context) error {
		unary.Info(ctx).ResponseHeader().Set(paladin.HeaderRetryAfter, retryAfter)
		return connect.NewError(connect.CodeInternal, leak)
	}}
	interceptors := []connect.ServerInterceptor{ScrubInternal(), inner}

	for name, sent := range map[string]string{"an id the caller sent": "req-from-caller", "no id": ""} {
		t.Run(name, func(t *testing.T) {
			var pairs []string
			if sent != "" {
				pairs = append(pairs, HeaderRequestID, sent)
			}
			header, err := unarytest.CallProbe(context.Background(), probe, interceptors, pairs...)
			if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "SQLSTATE") {
				t.Fatalf("caller got %v", err)
			}
			if !strings.Contains(innerErr.Error(), "SQLSTATE") {
				t.Errorf("the inner interceptor saw %v, want the original", innerErr)
			}
			if innerID == "" || (sent != "" && innerID != sent) || !strings.Contains(err.Error(), innerID) {
				t.Errorf("inner id %q, caller message %q, sent %q", innerID, err.Error(), sent)
			}
			if got := header.Get(HeaderRequestID); got != innerID {
				t.Errorf("%s header = %q, want %q", HeaderRequestID, got, innerID)
			}
			if got := header.Get(paladin.HeaderRetryAfter); got != retryAfter {
				t.Errorf("%s header = %q, want %q — the handler's metadata was lost", paladin.HeaderRetryAfter, got, retryAfter)
			}
		})
	}
}

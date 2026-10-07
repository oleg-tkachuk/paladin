package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/emptypb"
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
			got := scrub(connect.NewError(tc.code, errors.New(leak)), id)
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

func TestScrubKeepsDetailsAndMetadata(t *testing.T) {
	in := connect.NewError(connect.CodeInternal, errors.New(leak))
	detail, err := connect.NewErrorDetail(&errdetails.ErrorInfo{Reason: "SOME_REASON", Domain: "paladin"})
	if err != nil {
		t.Fatal(err)
	}
	in.AddDetail(detail)
	in.Meta().Set("Retry-After", "3")
	var out *connect.Error
	if !errors.As(scrub(in, "req-1"), &out) {
		t.Fatal("not a connect error")
	}
	if len(out.Details()) != 1 || out.Meta().Get("Retry-After") != "3" || out.Meta().Get(HeaderRequestID) != "req-1" {
		t.Errorf("details %d, meta %v", len(out.Details()), out.Meta())
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
// same request id — minted when the caller sent none.
func TestScrubInternalThroughAHandler(t *testing.T) {
	var innerErr error
	var innerID string
	inner := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			innerErr, innerID = err, req.Header().Get(HeaderRequestID)
			return res, err
		}
	})
	const procedure = "/test.v1.Svc/Fail"
	mux := http.NewServeMux()
	mux.Handle(procedure, connect.NewUnaryHandler(procedure,
		func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			return nil, connect.NewError(connect.CodeInternal, errors.New(leak))
		},
		connect.WithInterceptors(ScrubInternal(), inner),
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+procedure)

	for name, sent := range map[string]string{"an id the caller sent": "req-from-caller", "no id": ""} {
		t.Run(name, func(t *testing.T) {
			req := connect.NewRequest(&emptypb.Empty{})
			if sent != "" {
				req.Header().Set(HeaderRequestID, sent)
			}
			_, err := client.CallUnary(context.Background(), req)
			if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "SQLSTATE") {
				t.Fatalf("caller got %v", err)
			}
			if !strings.Contains(innerErr.Error(), "SQLSTATE") {
				t.Errorf("the inner interceptor saw %v, want the original", innerErr)
			}
			if innerID == "" || (sent != "" && innerID != sent) || !strings.Contains(err.Error(), innerID) {
				t.Errorf("inner id %q, caller message %q, sent %q", innerID, err.Error(), sent)
			}
		})
	}
}

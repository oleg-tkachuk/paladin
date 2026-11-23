package middleware

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/protonul"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// withNUL is request text holding NUL.
const withNUL = "a" + string(protonul.NUL) + "b"

// countingUploads records whether the handler ran: a refusal from the
// interceptor leaves it at zero.
type countingUploads struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	calls int
}

func (s *countingUploads) UploadObject(context.Context, *datav1.UploadObjectRequest) (*datav1.UploadObjectResponse, error) {
	s.calls++
	return &datav1.UploadObjectResponse{}, nil
}

// newNULServer is ObjectService behind the failure log and RefuseNUL, as a
// plane mounts them, and the log lines it writes.
func newNULServer(t *testing.T) (paladindatav1connect.ObjectServiceClient, *countingUploads, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	svc := &countingUploads{}
	client := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, svc)
	}, LogOutcome(zap.New(core)), RefuseNUL()))
	return client, svc, logs
}

// A request string holding NUL is refused as InvalidArgument naming its
// field, before the handler runs, and logged once as a refusal at Warn
// without the value.
func TestRefuseNULRefusesBeforeTheHandler(t *testing.T) {
	cases := []struct {
		name string
		req  *datav1.UploadObjectRequest
		path string
	}{
		{"the key", &datav1.UploadObjectRequest{Parent: "tenants/t/collections/c", Key: withNUL}, "key"},
		{"a metadata value", &datav1.UploadObjectRequest{Metadata: map[string]string{"owner": withNUL}}, "metadata.value"},
		{"a tag key", &datav1.UploadObjectRequest{Tags: map[string]string{withNUL: "v"}}, "tags.key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, svc, logs := newNULServer(t)
			_, err := client.UploadObject(context.Background(), tc.req)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if svc.calls != 0 {
				t.Fatalf("the handler ran %d times behind a refusal", svc.calls)
			}
			ce, _ := errors.AsType[*connect.Error](err)
			if want := fmt.Sprintf(protonul.RefusalFormat, tc.path); ce.Message() != want {
				t.Errorf("message = %q, want %q", ce.Message(), want)
			}
			fuzzassert.Refusal(t, err, []connect.Code{connect.CodeInvalidArgument})

			lines := logs.All()
			if len(lines) != 1 {
				t.Fatalf("logged %d lines, want 1", len(lines))
			}
			if got := lines[0]; got.Message != msgRPCRefused || got.Level != zapcore.WarnLevel {
				t.Errorf("logged %q at %v, want %q at %v", got.Message, got.Level, msgRPCRefused, zapcore.WarnLevel)
			}
			if err := fuzzassert.CheckText(fmt.Sprint(lines[0].ContextMap())); err != nil {
				t.Errorf("the log line: %v", err)
			}
		})
	}
}

// A clean request reaches the handler untouched; bytes and strings without
// NUL are not this interceptor's business.
func TestRefuseNULPassesCleanRequests(t *testing.T) {
	client, svc, logs := newNULServer(t)
	if _, err := client.UploadObject(context.Background(), &datav1.UploadObjectRequest{
		Parent: "tenants/t/collections/c", Key: "a/b", Metadata: map[string]string{"owner": "x"},
	}); err != nil {
		t.Fatalf("UploadObject: %v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("the handler ran %d times, want 1", svc.calls)
	}
	if n := logs.Len(); n != 0 {
		t.Errorf("a clean call logged %d failure lines", n)
	}
}

// RefuseNUL checks unary requests only. A streaming RPC on any plane would
// pass it unchecked, so adding one has to extend it first.
func TestNoRPCStreams(t *testing.T) {
	eachMethod(func(procedure string, md protoreflect.MethodDescriptor) {
		if md.IsStreamingClient() || md.IsStreamingServer() {
			t.Errorf("%s streams, and RefuseNUL does not see a streamed request", procedure)
		}
	})
}

package unary_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

const replayedVersion = "replayed"

// The wrapper sees the decoded request and the handler's response.
func TestInterceptorSeesRequestAndResponse(t *testing.T) {
	var sawReq, sawRes bool
	ic := unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			_, sawReq = req.(*iamv1.GetVersionRequest)
			res, err := next(ctx, spec, req)
			_, sawRes = res.(*iamv1.VersionInfo)
			return res, err
		}
	}, nil)
	if _, err := unarytest.CallProbe(context.Background(), &unarytest.Probe{}, []connect.ServerInterceptor{ic}); err != nil {
		t.Fatal(err)
	}
	if !sawReq || !sawRes {
		t.Errorf("saw request %v, response %v; want both", sawReq, sawRes)
	}
}

// A wrapper may answer without the handler: the idempotency replay.
func TestInterceptorAnswersWithoutTheHandler(t *testing.T) {
	handled := false
	probe := &unarytest.Probe{OnCall: func(context.Context) error { handled = true; return nil }}
	ic := unary.Interceptor(func(unary.Func) unary.Func {
		return func(_ context.Context, spec connect.Spec, _ proto.Message) (proto.Message, error) {
			res, err := unary.NewResponse(spec)
			if err != nil {
				return nil, err
			}
			res.(*iamv1.VersionInfo).Version = replayedVersion
			return res, nil
		}
	}, nil)
	client := paladiniamv1connect.NewHealthServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterHealthServiceHandler(s, probe)
	}, ic))
	res, err := client.GetVersion(context.Background(), &iamv1.GetVersionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if handled || res.GetVersion() != replayedVersion {
		t.Errorf("handled %v, version %q; want the wrapper's answer alone", handled, res.GetVersion())
	}
}

// A wrapper's error is the call's, and the handler does not run.
func TestInterceptorRefuses(t *testing.T) {
	handled := false
	probe := &unarytest.Probe{OnCall: func(context.Context) error { handled = true; return nil }}
	ic := unary.Interceptor(func(unary.Func) unary.Func {
		return func(context.Context, connect.Spec, proto.Message) (proto.Message, error) {
			return nil, connect.NewError(connect.CodePermissionDenied, "no")
		}
	}, nil)
	_, err := unarytest.CallProbe(context.Background(), probe, []connect.ServerInterceptor{ic})
	if connect.CodeOf(err) != connect.CodePermissionDenied || handled {
		t.Errorf("err %v, handled %v; want PermissionDenied and no handler", err, handled)
	}
}

var errStreamed = errors.New("streamed")

// A stream goes to the stream hook, never to the unary wrapper.
func TestInterceptorPassesStreamsOn(t *testing.T) {
	ic := unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			t.Error("the unary wrapper ran for a stream")
			return next(ctx, spec, req)
		}
	}, func(connect.ServerFunc) connect.ServerFunc {
		return func(context.Context, connect.Spec, connect.ServerStream) error { return errStreamed }
	})
	call := ic(func(context.Context, connect.Spec, connect.ServerStream) error { return nil })
	if err := call(context.Background(), connect.Spec{StreamType: connect.StreamTypeServer}, nil); !errors.Is(err, errStreamed) {
		t.Fatalf("err = %v, want the stream hook's", err)
	}
}

// A procedure without a protobuf schema cannot be decoded here.
func TestNewRequestWithoutASchema(t *testing.T) {
	if _, err := unary.NewRequest(connect.Spec{Procedure: "/x.Svc/M"}); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("err = %v, want Internal", err)
	}
}

// Outside a call Info is empty rather than nil.
func TestInfoOutsideACall(t *testing.T) {
	if got := unary.Info(context.Background()).RequestHeader().Get("X"); got != "" {
		t.Errorf("header = %q", got)
	}
}

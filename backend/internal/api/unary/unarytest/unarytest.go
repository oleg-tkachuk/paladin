// Package unarytest runs Connect handlers and interceptors in process, as a
// test of them needs: a server-side CallInfo exists only for a call that came
// through a connect.Server, so a handler that reads a header, or an
// interceptor, is tested through one.
package unarytest

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
)

// Client is a *connect.Client calling a server that register filled and
// interceptors wrap, in process: hand it to a generated New…ServiceClient.
func Client(register func(*connect.Server), interceptors ...connect.ServerInterceptor) *connect.Client {
	server := connect.NewServer(interceptors...)
	register(server)
	return connect.NewClient(connectinprocess.New(server))
}

// WithHeader returns ctx for a call that sends the given header pairs: key,
// value, key, value.
func WithHeader(ctx context.Context, pairs ...string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	for i := 0; i+1 < len(pairs); i += 2 {
		info.RequestHeader().Add(pairs[i], pairs[i+1])
	}
	return ctx
}

// Probe is a unary RPC from the contract — the IAM plane's GetVersion — that
// a test drives interceptors with. A test needs a real procedure: Interceptor
// reads the request type from the method's schema, which a made-up procedure
// has none of.
type Probe struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	// OnCall, when set, runs with the context the handler is reached under —
	// the principal, the capability, the acting tenant the interceptors set
	// — and its error is the call's.
	OnCall func(ctx context.Context) error
}

// GetVersion answers an empty VersionInfo, or OnCall's error.
func (p *Probe) GetVersion(ctx context.Context, _ *iamv1.GetVersionRequest) (*iamv1.VersionInfo, error) {
	if p.OnCall != nil {
		if err := p.OnCall(ctx); err != nil {
			return nil, err
		}
	}
	return &iamv1.VersionInfo{}, nil
}

// ProbeProcedure is the procedure Probe serves.
const ProbeProcedure = paladiniamv1connect.HealthServiceGetVersionProcedure

// CallProbe calls p in process through interceptors, sending the header
// pairs, and returns the call's error and the response headers the server
// set — Retry-After and the like.
func CallProbe(ctx context.Context, p *Probe, interceptors []connect.ServerInterceptor, pairs ...string) (*connect.Header, error) {
	client := paladiniamv1connect.NewHealthServiceClient(Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterHealthServiceHandler(s, p)
	}, interceptors...))
	ctx = WithHeader(ctx, pairs...)
	info, _ := connect.CallInfoForClientContext(ctx)
	_, err := client.GetVersion(ctx, &iamv1.GetVersionRequest{})
	return info.ResponseHeader(), err
}

// LeavesRequest is a layer that fails tb when a layer inside it changes the
// call's request. Place it first, outermost, in front of the interceptor
// under test.
//
// Every unary.Interceptor in a chain shares the one request message decoded
// from the wire, so a layer that writes to it changes what every other layer
// reads — the audit log among them, which records the request after the
// handler returns. LeavesRequest keeps a copy before the call and compares it
// with the shared message once the call returns, whatever its error.
func LeavesRequest(tb testing.TB) connect.ServerInterceptor {
	tb.Helper()
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			sent := proto.Clone(req)
			res, err := next(ctx, spec, req)
			if !proto.Equal(sent, req) {
				tb.Errorf("%s: a layer changed the shared request (-sent +after):\n%s",
					spec.Procedure, cmp.Diff(sent, req, protocmp.Transform()))
			}
			return res, err
		}
	}, nil)
}

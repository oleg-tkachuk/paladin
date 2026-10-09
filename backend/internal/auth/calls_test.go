package auth

import (
	"context"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

// Shared mechanics for driving this package's interceptors the way a server
// does. An interceptor reads its headers from the server-side CallInfo, which
// exists only for a call that came through a connect.Server, so a test calls
// through one rather than invoking the interceptor func by hand.

// Plane labels the API-token and capability interceptors are mounted with.
// API tokens and capabilities name planes by the same short labels.
const (
	planeData  = limes.AudiencePlaneData
	planeAdmin = limes.AudiencePlaneAdmin
)

// bearerPrefix is the Authorization scheme an API token or a JWT rides in.
const bearerPrefix = "Bearer "

// probeCall is the outcome of one call through the Probe.
type probeCall struct {
	// handlerCtx is the context the handler ran under; nil when the
	// interceptors refused the call before it reached the handler.
	handlerCtx context.Context
	// responseHeader is what the server set on the response.
	responseHeader *connect.Header
	err            error
}

func (c probeCall) reached() bool { return c.handlerCtx != nil }

// callProbe calls the contract Probe in process through interceptors under
// ctx, sending the header pairs.
func callProbe(ctx context.Context, interceptors []connect.ServerInterceptor, pairs ...string) probeCall {
	var out probeCall
	probe := &unarytest.Probe{OnCall: func(ctx context.Context) error {
		out.handlerCtx = ctx
		return nil
	}}
	out.responseHeader, out.err = unarytest.CallProbe(ctx, probe, interceptors, pairs...)
	return out
}

// streamProcedure is the server-streaming procedure callStream registers. The
// contract has no streaming RPC, and a stream's interceptors never read a
// message, so a made-up procedure serves.
const streamProcedure = "/auth.stream.v1.Svc/Watch"

// callStream runs one server-streaming call through interceptors via
// connect.Server.Call, which attaches the server-side CallInfo carrying
// header, and reports the context the handler ran under (nil when refused).
// The handler never touches the stream, so none is passed.
func callStream(ctx context.Context, interceptors []connect.ServerInterceptor, header *connect.Header) (context.Context, error) {
	var seen context.Context
	server := connect.NewServer(interceptors...)
	server.Register(connect.Method{
		Spec: connect.Spec{Procedure: streamProcedure, StreamType: connect.StreamTypeServer},
		Handler: func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
			seen = ctx
			return nil
		},
	})
	info := &connect.CallInfo{}
	for k, vs := range header.All() {
		info.RequestHeader().SetValues(k, vs)
	}
	err := server.Call(ctx, streamProcedure, info, nil)
	return seen, err
}

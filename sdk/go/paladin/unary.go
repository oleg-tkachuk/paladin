package paladin

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
)

// unaryFunc runs one whole unary call: req is the request message, and the
// response is decoded into res. Request and response metadata are on the
// context's connect.CallInfo.
type unaryFunc func(ctx context.Context, spec connect.Spec, req, res any) error

// interceptor builds a connect.ClientInterceptor from what it does to a whole
// unary call and what it does to the opening of a stream; a nil stream lets
// streams pass untouched.
//
// A v2 client interceptor wraps only the opening of a stream, while retrying
// a call, refreshing its token or wrapping its error needs the whole call. So
// a unary call is handed a stream that opens nothing until the response is
// asked for, and then runs the call through unary — which may run it more
// than once, each time on a fresh stream from next.
func interceptor(
	unary func(next unaryFunc) unaryFunc,
	stream func(next connect.ClientFunc) connect.ClientFunc,
) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		call := unary(func(ctx context.Context, spec connect.Spec, req, res any) error {
			return callUnary(ctx, next, spec, req, res)
		})
		streaming := next
		if stream != nil {
			streaming = stream(next)
		}
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if spec.StreamType != connect.StreamTypeUnary {
				return streaming(ctx, spec)
			}
			return &deferredUnary{ctx: ctx, spec: spec, call: call}, nil
		}
	}
}

// callUnary runs a unary call on a stream opened by next, the sequence
// connect.Client.CallUnary runs.
func callUnary(ctx context.Context, next connect.ClientFunc, spec connect.Spec, req, res any) (err error) {
	stream, err := next(ctx, spec)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stream.Close()) }()
	if err := stream.Send(req); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	return stream.Receive(res)
}

// deferredUnary is the stream interceptor hands a unary call: it holds
// the request until the response is asked for, then runs the call.
type deferredUnary struct {
	ctx      context.Context
	spec     connect.Spec
	call     unaryFunc
	req      any
	received bool
}

func (s *deferredUnary) SendHeaders() error { return nil }

func (s *deferredUnary) Send(msg any) error {
	s.req = msg
	return nil
}

func (s *deferredUnary) CloseSend() error { return nil }

func (s *deferredUnary) Receive(dst any) error {
	if s.received {
		return io.EOF
	}
	s.received = true
	return s.call(s.ctx, s.spec, s.req, dst)
}

func (s *deferredUnary) Close() error { return nil }

// callInfo is the call's connect.CallInfo. connect.Client attaches one to the
// context of every call before the interceptors run.
func callInfo(ctx context.Context) *connect.CallInfo {
	if info, ok := connect.CallInfoForClientContext(ctx); ok {
		return info
	}
	return &connect.CallInfo{}
}

// responseMeta reads key from the response header, then the trailer: the two
// places a server's metadata arrives, depending on the protocol.
func responseMeta(info *connect.CallInfo, key string) string {
	if v := info.ResponseHeader().Get(key); v != "" {
		return v
	}
	return info.ResponseTrailer().Get(key)
}

// clearResponse drops what an earlier attempt of the call received, so a
// retry reads its own answer.
func clearResponse(info *connect.CallInfo) {
	for _, h := range []*connect.Header{info.ResponseHeader(), info.ResponseTrailer()} {
		for key := range h.All() {
			h.Delete(key)
		}
	}
}

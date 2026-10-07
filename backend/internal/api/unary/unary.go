// Package unary lets a Connect server interceptor act on a whole unary call:
// the decoded request in, the response out.
//
// connect-go v2 hands a server interceptor a stream, not a request and a
// response. That suits a check made from headers or the context, but not one
// that reads the request message, replays a memoised response, or looks at
// what the handler answered — and most of this server's interceptors do one
// of those. Interceptor receives the request itself, runs the chain on it,
// and sends what the chain returns; the handler runs on a stream that yields
// the request already received and keeps the response it sends.
package unary

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Func serves one whole unary call. Request and response metadata are on the
// context's connect.CallInfo.
type Func func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error)

// Interceptor adapts wrap, which acts on whole unary calls, to a
// connect.ServerInterceptor. A streaming call goes to stream, or passes
// through untouched when stream is nil.
func Interceptor(
	wrap func(next Func) Func,
	stream func(next connect.ServerFunc) connect.ServerFunc,
) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		call := wrap(func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			held := &heldStream{req: req}
			if err := next(ctx, spec, held); err != nil {
				return nil, err
			}
			if held.res == nil {
				return nil, connect.Errorf(connect.CodeInternal, "%s sent no response", spec.Procedure)
			}
			return held.res, nil
		})
		streaming := next
		if stream != nil {
			streaming = stream(next)
		}
		return func(ctx context.Context, spec connect.Spec, s connect.ServerStream) error {
			if spec.StreamType != connect.StreamTypeUnary {
				return streaming(ctx, spec, s)
			}
			req, err := NewRequest(spec)
			if err != nil {
				return err
			}
			if err := s.Receive(req); err != nil {
				return err
			}
			res, err := call(ctx, spec, req)
			if err != nil {
				return err
			}
			return s.Send(res)
		}
	}
}

// NewRequest is an empty request message of the method spec describes.
func NewRequest(spec connect.Spec) (proto.Message, error) {
	return newMessage(spec, protoreflect.MethodDescriptor.Input)
}

// NewResponse is an empty response message of the method spec describes, for
// an interceptor that answers a call itself.
func NewResponse(spec connect.Spec) (proto.Message, error) {
	return newMessage(spec, protoreflect.MethodDescriptor.Output)
}

func newMessage(spec connect.Spec, which func(protoreflect.MethodDescriptor) protoreflect.MessageDescriptor) (proto.Message, error) {
	method, ok := spec.Schema.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, connect.Errorf(connect.CodeInternal, "%s has no protobuf schema", spec.Procedure)
	}
	typ, err := protoregistry.GlobalTypes.FindMessageByName(which(method).FullName())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "%s: %v", spec.Procedure, err).WithCause(err)
	}
	return typ.New().Interface(), nil
}

// heldStream is the stream a unary handler runs on behind Interceptor.
type heldStream struct {
	req      proto.Message
	received bool
	res      proto.Message
}

func (s *heldStream) Receive(msg any) error {
	if s.received {
		return io.EOF
	}
	s.received = true
	dst, ok := msg.(proto.Message)
	if !ok {
		return errors.New("unary: request is not a protobuf message")
	}
	proto.Merge(dst, s.req)
	return nil
}

func (s *heldStream) SendHeaders() error { return nil }

func (s *heldStream) Send(msg any) error {
	res, ok := msg.(proto.Message)
	if !ok {
		return errors.New("unary: response is not a protobuf message")
	}
	s.res = res
	return nil
}

// Info is the call's server-side connect.CallInfo; an empty one outside a
// call, so a check run from a unit test reads no headers rather than panics.
func Info(ctx context.Context) *connect.CallInfo {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		return info
	}
	return &connect.CallInfo{}
}

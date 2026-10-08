package paladintest

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// unaryHandler serves one whole unary call: the decoded request in, the
// response out. Request and response metadata are on the context's
// connect.CallInfo.
type unaryHandler func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error)

// unaryInterceptor adapts wrap, which acts on whole unary calls, to a
// connect.ServerInterceptor; streams pass through untouched.
//
// A v2 server interceptor sees a stream, not a request and a response, so it
// cannot replay a memoised answer or read the message before the handler
// does. A unary call is therefore received here, run through wrap, and
// answered with what wrap returns; the handler is handed a stream that
// yields the request already received and keeps the response it sends.
func unaryInterceptor(wrap func(next unaryHandler) unaryHandler) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if spec.StreamType != connect.StreamTypeUnary {
				return next(ctx, spec, stream)
			}
			req, err := newInput(spec)
			if err != nil {
				return err
			}
			if err := stream.Receive(req); err != nil {
				return err
			}
			handler := wrap(func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
				held := &heldUnary{req: req}
				if err := next(ctx, spec, held); err != nil {
					return nil, err
				}
				return held.res, nil
			})
			res, err := handler(ctx, spec, req)
			if err != nil {
				return err
			}
			return stream.Send(res)
		}
	}
}

// newInput is an empty request message of the method spec describes.
func newInput(spec connect.Spec) (proto.Message, error) {
	method, ok := spec.Schema.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, connect.Errorf(connect.CodeInternal, "paladintest: %s has no protobuf schema", spec.Procedure)
	}
	typ, err := protoregistry.GlobalTypes.FindMessageByName(method.Input().FullName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Sprintf("paladintest: %s: %v", spec.Procedure, err)).WithCause(err)
	}
	return typ.New().Interface(), nil
}

// heldUnary is the stream a unary handler runs on behind unaryInterceptor:
// the request was received already, and the response is kept for the
// interceptor to send.
type heldUnary struct {
	req      proto.Message
	received bool
	res      proto.Message
}

func (s *heldUnary) Receive(msg any) error {
	if s.received {
		return io.EOF
	}
	s.received = true
	dst, ok := msg.(proto.Message)
	if !ok {
		return errors.New("paladintest: request is not a protobuf message")
	}
	proto.Merge(dst, s.req)
	return nil
}

func (s *heldUnary) SendHeaders() error { return nil }

func (s *heldUnary) Send(msg any) error {
	res, ok := msg.(proto.Message)
	if !ok {
		return errors.New("paladintest: response is not a protobuf message")
	}
	s.res = res
	return nil
}

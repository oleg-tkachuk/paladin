// Package middleware: protobuf validation interceptor.
//
// Wires `buf.build/go/protovalidate` into the Connect interceptor chain so
// that every inbound request is checked against its `(buf.validate.field)`
// rules *before* it reaches the handler. Without this interceptor a
// malformed request (`{}`, empty IDs, oversize strings, negative ints)
// reaches the handler and either segfaults later or — worse — produces a
// 500 from a downstream call (e.g. S3 rejecting an empty bucket name with
// MethodNotAllowed). With it in place every shape violation is converted
// to a `CodeInvalidArgument` 400 response, surfacing the offending field.
package middleware

import (
	"context"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// ProtoValidate returns a Connect interceptor that runs protovalidate
// against every unary request that carries a proto.Message body.
//
// Streaming RPCs are passed through untouched; their per-frame messages
// are typically validated inside the handler instead.
func ProtoValidate() (connect.UnaryInterceptorFunc, error) {
	v, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("protovalidate: init: %w", err)
	}

	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			msg, ok := req.Any().(proto.Message)
			if !ok {
				return next(ctx, req)
			}
			if err := v.Validate(msg); err != nil {
				var verr *protovalidate.ValidationError
				if errors.As(err, &verr) {
					return nil, connect.NewError(
						connect.CodeInvalidArgument,
						fmt.Errorf("validation failed: %w", verr),
					)
				}
				return nil, connect.NewError(
					connect.CodeInvalidArgument,
					fmt.Errorf("validate: %w", err),
				)
			}
			return next(ctx, req)
		}
	}, nil
}

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
	"fmt"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/validate"
)

// ProtoValidate returns the interceptor that runs protovalidate against every
// request, unary and streamed, before a handler sees it: connect's own
// validate module. A refusal is InvalidArgument with protovalidate's message
// and the buf.validate.Violations detail, so a client reads which field broke
// which rule without parsing text.
//
// The validator is built here rather than taken from protovalidate's global
// one so that a contract whose rules do not compile fails the server at
// start, not on the first request.
func ProtoValidate() (connect.ServerInterceptor, error) {
	v, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("protovalidate: init: %w", err)
	}
	return validate.NewServerInterceptor(validate.WithValidator(v)), nil
}

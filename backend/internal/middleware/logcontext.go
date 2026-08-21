package middleware

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// LogContext puts a request-scoped logger on the context.
//
// internal/logger already knows how to correlate: FromContext enriches a
// logger with trace_id, span_id, request_id and tenant_id. What was missing
// was anyone calling it — and anyone supplying the values it reads. The
// mechanism was wired at neither end, so every log line was written through a
// struct-held logger that knew the component and nothing about the request.
//
// This is the supply half, at the only boundary where an RPC enters: it stashes
// the base logger plus the two ids that are not already on the context.
// trace_id and span_id need no help — otelconnect has opened the span by the
// time this runs, and FromContext reads it straight from the context.
//
// The read half is logger.FromContext(ctx) at the call sites.
//
// Ordering matters: install this AFTER the otel interceptor, so a span exists,
// and after auth, so the principal's tenant is known. Installed earlier it
// still works and simply carries fewer fields, which is the right failure —
// a log line with a trace id and no tenant is still findable.
func LogContext(base *zap.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(withLogContext(ctx, base, req.Header().Get(HeaderRequestID), req.Spec().Procedure), req)
		}
	}
}

// withLogContext is the shared body, split out so the streaming and unary
// paths cannot drift.
func withLogContext(ctx context.Context, base *zap.Logger, requestID, procedure string) context.Context {
	if requestID != "" {
		ctx = context.WithValue(ctx, utils.RequestIDKey, requestID)
	}
	// The tenant comes from the verified principal rather than from a header:
	// a header-supplied tenant id in a log line is an assertion by the caller,
	// which is exactly what a log used for an audit trail must not contain.
	if p, err := auth.PrincipalFromContext(ctx); err == nil && p != nil && p.TenantID.String() != "" {
		ctx = utils.WithTenantID(ctx, p.TenantID.String())
	}
	if base != nil {
		ctx = logger.WithContext(ctx, base.With(zap.String("rpc", procedure)))
	}
	return ctx
}

// LogContextStreaming mirrors LogContext for streaming handlers. Kept separate
// because connect's streaming signature differs; the body is shared.
func LogContextStreaming(base *zap.Logger) connect.Interceptor {
	return &logContextInterceptor{base: base}
}

type logContextInterceptor struct{ base *zap.Logger }

func (i *logContextInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(withLogContext(ctx, i.base, req.Header().Get(HeaderRequestID), req.Spec().Procedure), req)
	}
}

func (i *logContextInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *logContextInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(withLogContext(ctx, i.base, conn.RequestHeader().Get(HeaderRequestID), conn.Spec().Procedure), conn)
	}
}

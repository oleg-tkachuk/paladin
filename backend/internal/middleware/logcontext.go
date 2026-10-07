package middleware

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/reqctx"
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
//
// One interceptor serves unary and streaming calls alike: a v2 interceptor
// wraps the whole call whatever its shape.
func LogContext(base *zap.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			requestID := unary.Info(ctx).RequestHeader().Get(HeaderRequestID)
			return next(withLogContext(ctx, base, requestID, spec.Procedure), spec, stream)
		}
	}
}

// withLogContext stashes the request-scoped logger and ids.
func withLogContext(ctx context.Context, base *zap.Logger, requestID, procedure string) context.Context {
	if requestID != "" {
		ctx = reqctx.WithRequestID(ctx, requestID)
	}
	// The tenant comes from the verified principal rather than from a header:
	// a header-supplied tenant id in a log line is an assertion by the caller,
	// which is exactly what a log used for an audit trail must not contain.
	if p, err := auth.PrincipalFromContext(ctx); err == nil && p != nil && p.TenantID.String() != "" {
		ctx = reqctx.WithTenantID(ctx, p.TenantID.String())
	}
	if base != nil {
		ctx = logger.WithContext(ctx, base.With(zap.String("rpc", procedure)))
	}
	return ctx
}

// ─── outcomes ───────────────────────────────────────────────────────────────

// LogOutcome writes one line per FAILED RPC.
//
// Until this existed, a request could be refused and leave no trace at all.
// Verified against the deployed admin plane: a ListTenants with a filter that
// does not compile came back InvalidArgument to the client, and
// `kubectl logs` for the following thirty seconds was empty. The same held for
// every rejection the auth interceptor makes — a wave of failed
// authentications was invisible.
//
// otelconnect covers this in traces and metrics, and that is a different
// question. Traces answer "show me this request" when you already have its id
// and a collector kept it; a log line answers "did anything fail in the last
// hour, and what". The repository had the first and not the second.
//
// SUCCESSES ARE NOT LOGGED. One line per served request is a firehose that
// makes the failures harder to find, and otelconnect already counts them.
//
// INSTALL IT OUTERMOST, right after the otel interceptor and BEFORE auth.
// Connect applies the first-listed interceptor outermost, so anything
// installed after auth cannot see auth's own rejections — which is exactly
// where this gap was widest. The cost of the outer position is that the
// verified tenant is not on the context yet, so these lines carry the request
// id and not the tenant. For a request rejected before authentication that is
// not a loss but the truth: there is no verified tenant to name.
func LogOutcome(base *zap.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			start := time.Now()
			err := next(ctx, spec, stream)
			logFailure(base, spec.Procedure, unary.Info(ctx).RequestHeader().Get(HeaderRequestID), time.Since(start), err)
			return err
		}
	}
}

// logFailure is the whole decision, split out from the interceptor plumbing so
// it can be tested at every code rather than at the two the plumbing makes
// easy to produce.
func logFailure(base *zap.Logger, procedure, requestID string, took time.Duration, err error) {
	if base == nil || err == nil {
		return
	}
	code := connect.CodeOf(err)
	fields := []zap.Field{
		zap.String("rpc", procedure),
		zap.String("code", code.String()),
		zap.Int64("took_ms", took.Milliseconds()),
	}
	if requestID != "" {
		fields = append(fields, zap.String("request_id", requestID))
	}
	// The message the server already sent to this caller. Logging it discloses
	// nothing the caller does not have; it is the difference between "an RPC
	// failed" and knowing why without reproducing it.
	fields = append(fields, zap.String("err", err.Error()))

	switch code {
	// The server is broken. Someone should be woken up by a rate of these.
	case connect.CodeInternal, connect.CodeUnknown,
		connect.CodeDataLoss, connect.CodeUnavailable:
		base.Error("rpc failed", fields...)
	// The request was refused by a policy or a precondition. Not a defect, but
	// a burst of them means something changed — a quota, a Cedar policy, a
	// concurrent writer taking every OCC race.
	case connect.CodePermissionDenied, connect.CodeResourceExhausted,
		connect.CodeAborted, connect.CodeFailedPrecondition:
		base.Warn("rpc refused", fields...)
	// An ordinary client mistake: a bad filter, a name that is not there, a
	// token that expired. Info because these are normal traffic and an
	// operator still has to be able to find them; Warn would cry wolf on every
	// browser that probes an endpoint without a token.
	default:
		base.Info("rpc rejected", fields...)
	}
}

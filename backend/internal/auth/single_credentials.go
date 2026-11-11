package auth

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// duplicateCredentials is what a caller is told of a request presenting a
// credential twice; the cause names which.
const duplicateCredentials = "the request presents more than one Authorization header or DPoP proof"

// SingleCredentials refuses a call that carries more than one Authorization
// field or more than one DPoP proof, before any interceptor reads either.
// RFC 9449 §7.2 and RFC 6750 §2 refuse a token presented twice, and §4.3 a
// second proof. Every reader below takes the first value, so one of them
// could otherwise verify a credential another ignored. Mount it ahead of
// every interceptor that authenticates.
func SingleCredentials() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if err := singleCredentials(ctx, spec.Procedure, unary.Info(ctx).RequestHeader()); err != nil {
				return err
			}
			return next(ctx, spec, stream)
		}
	}
}

// singleCredentials is SingleCredentials's check of one call's header, read
// by limes's own parsers so the rule is RFC 9449's as limes holds it.
func singleCredentials(ctx context.Context, procedure string, header *connect.Header) error {
	var err error
	if values := header.Values(paladin.HeaderAuthorization); len(values) > 1 {
		_, _, err = limes.ParseAuthorizationValues(values)
	}
	if err == nil {
		_, err = dpop.SingleProof(header.Values(dpop.Header))
	}
	if err == nil {
		return nil
	}
	logger.FromContext(ctx).Warn("auth: refused a call presenting a credential twice",
		zap.String("procedure", procedure), zap.Error(err))
	return connect.NewError(connect.CodeInvalidArgument, duplicateCredentials).WithCause(err)
}

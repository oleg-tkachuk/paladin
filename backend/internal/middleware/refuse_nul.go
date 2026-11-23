package middleware

import (
	"context"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/protonul"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// RefuseNUL refuses a request any of whose strings holds a NUL character
// (U+0000), as InvalidArgument naming the field: "key: holds a NUL
// character". PostgreSQL stores no NUL in text and jsonb refuses \u0000, so
// such a string, accepted, fails at the store and comes back as Internal — a
// server error for the client's own input. Refused here, once, no handler and
// no interceptor after this one reads it.
//
// The refusal never holds the value, and is a rpcerr.Refusal, which
// LogOutcome logs once, at Warn. Bytes fields are not text and pass.
//
// Install it as soon as the principal is known, ahead of every interceptor
// that reads the request: the tenant freeze and the quota check look up names
// the request carries, and the iam plane's audit stores the request in jsonb.
// Unary only, as the contract declares no streaming RPC; a stream passes
// through unchecked.
func RefuseNUL() connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			if err := checkNUL(req); err != nil {
				return nil, err
			}
			return next(ctx, spec, req)
		}
	}, nil)
}

// checkNUL is RefuseNUL's verdict on one request message.
func checkNUL(req proto.Message) error {
	if found := protonul.Find(req.ProtoReflect()); found != nil {
		return rpcerr.New(connect.CodeInvalidArgument, found)
	}
	return nil
}

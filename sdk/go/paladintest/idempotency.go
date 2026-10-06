package paladintest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// errKeyReused is the server's answer to a key reused for another request.
var errKeyReused = connect.NewError(connect.CodeInvalidArgument, errors.New(
	"idempotency: this Idempotency-Key was already used for a different request to this method; "+
		"use a new key for a new request"))

// memoise answers a call the way the server's idempotency interceptor does: a
// call the contract declares free of side effects runs as it is; one with a
// key replays the response first given to the same request with that key, and
// is refused when the key last went with a different request. A test that
// shares one key across requests fails here as it would against Paladin.
func (s *Server) memoise(ctx context.Context, req connect.AnyRequest, msg proto.Message,
	next connect.UnaryFunc,
) (connect.AnyResponse, error) {
	if req.Spec().IdempotencyLevel == connect.IdempotencyNoSideEffects {
		return next(ctx, req)
	}
	key := req.Header().Get(paladin.HeaderIdempotencyKey)
	if key == "" {
		return next(ctx, req)
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return next(ctx, req)
	}
	sum := sha256.Sum256(encoded)
	slot := req.Spec().Procedure + "\x00" + key

	s.mu.Lock()
	prior, seen := s.replays[slot]
	s.mu.Unlock()
	if seen {
		if !bytes.Equal(prior.fingerprint, sum[:]) {
			return nil, errKeyReused
		}
		return prior.response, nil
	}
	resp, err := next(ctx, req)
	if err != nil {
		return resp, err
	}
	s.mu.Lock()
	s.replays[slot] = replay{fingerprint: sum[:], response: resp}
	s.mu.Unlock()
	return resp, nil
}

package paladin

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
)

var errOpened = errors.New("opened")

// A stream is not run through the unary path: the stream hook sees it, and
// next opens it.
func TestInterceptorPassesStreamsToTheStreamHook(t *testing.T) {
	var hooked, opened bool
	call := interceptor(func(next unaryFunc) unaryFunc { return next }, func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			hooked = true
			return next(ctx, spec)
		}
	})(func(context.Context, connect.Spec) (connect.ClientStream, error) {
		opened = true
		return nil, errOpened
	})
	if _, err := call(context.Background(), connect.Spec{StreamType: connect.StreamTypeServer}); !errors.Is(err, errOpened) {
		t.Fatalf("err = %v, want the opener's", err)
	}
	if !hooked || !opened {
		t.Errorf("hooked %v, opened %v; want both", hooked, opened)
	}
}

// A unary call opens nothing until its response is asked for, and each run
// of it by the wrapper opens a stream of its own.
func TestInterceptorOpensAStreamPerAttempt(t *testing.T) {
	opens := 0
	call := interceptor(func(next unaryFunc) unaryFunc {
		return func(ctx context.Context, spec connect.Spec, req, res any) error {
			_ = next(ctx, spec, req, res)
			return next(ctx, spec, req, res)
		}
	}, nil)(func(context.Context, connect.Spec) (connect.ClientStream, error) {
		opens++
		return nil, errOpened
	})
	stream, err := call(context.Background(), connect.Spec{StreamType: connect.StreamTypeUnary})
	if err != nil {
		t.Fatal(err)
	}
	if opens != 0 {
		t.Fatalf("%d streams opened before the response was asked for", opens)
	}
	if err := stream.Receive(nil); !errors.Is(err, errOpened) {
		t.Fatalf("err = %v, want the opener's", err)
	}
	if opens != 2 {
		t.Errorf("%d streams opened for two attempts", opens)
	}
}

// Each attempt reads its own answer: a Retry-After the first attempt was
// sent is gone before the second runs.
func TestRetryClearsTheLastAttemptsResponse(t *testing.T) {
	r := &retryPolicy{attempts: 2, baseDelay: 1, maxDelay: 1}
	ctx, info := connect.NewClientContext(context.Background())
	attempt := 0
	call := r.unary(func(_ context.Context, _ connect.Spec, _, _ any) error {
		attempt++
		if attempt == 2 && info.ResponseHeader().Get(HeaderRetryAfter) != "" {
			t.Error("the second attempt saw the first's Retry-After")
		}
		info.ResponseHeader().Set(HeaderRetryAfter, "0")
		return connect.NewError(connect.CodeUnavailable, "busy")
	})
	info.RequestHeader().Set(HeaderIdempotencyKey, "k")
	_ = call(ctx, connect.Spec{}, nil, nil)
	if attempt != 2 {
		t.Fatalf("%d attempts, want 2", attempt)
	}
}

// A stream whose token cannot be had is refused before it opens.
func TestTokenAuthRefusesAStreamWithoutAToken(t *testing.T) {
	a := &tokenAuth{source: tokenSourceFunc(func(context.Context, string) (string, error) {
		return "", ErrNoToken
	})}
	opened := false
	open := a.stream(func(context.Context, connect.Spec) (connect.ClientStream, error) {
		opened = true
		return nil, nil
	})
	ctx, _ := connect.NewClientContext(context.Background())
	if _, err := open(ctx, connect.Spec{StreamType: connect.StreamTypeServer}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
	if opened {
		t.Error("the stream opened without a token")
	}
}

type tokenSourceFunc func(context.Context, string) (string, error)

func (f tokenSourceFunc) Token(ctx context.Context, audience string) (string, error) {
	return f(ctx, audience)
}

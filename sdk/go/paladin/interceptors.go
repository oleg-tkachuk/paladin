package paladin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
)

type idempotencyKeyCtx struct{}

// WithIdempotencyKey attaches key to every call made with the returned
// context. Reuse the same key when repeating the same logical operation.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, key)
}

// IdempotencyKey returns the key WithIdempotencyKey attached, if any.
func IdempotencyKey(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(idempotencyKeyCtx{}).(string)
	return key, ok && key != ""
}

// headerInterceptor sets the credentials on every request and the idempotency
// key on requests whose context carries one.
type headerInterceptor struct {
	headers http.Header
}

func (h *headerInterceptor) apply(ctx context.Context, dst http.Header) {
	for name, values := range h.headers {
		dst[name] = append([]string(nil), values...)
	}
	if key, ok := IdempotencyKey(ctx); ok {
		dst.Set(HeaderIdempotencyKey, key)
	}
}

func (h *headerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		h.apply(ctx, req.Header())
		return next(ctx, req)
	}
}

func (h *headerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		h.apply(ctx, conn.RequestHeader())
		return conn
	}
}

func (h *headerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// retryPolicy repeats unary calls that are safe to repeat. Streams are never
// retried: a stream that failed halfway has already delivered part of itself.
type retryPolicy struct {
	attempts  int
	baseDelay time.Duration
	maxDelay  time.Duration
}

func (r *retryPolicy) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		delay := r.baseDelay
		for attempt := 1; ; attempt++ {
			resp, err := next(ctx, req)
			if err == nil || attempt >= r.attempts || !retryable(ctx, req.Spec(), err) {
				return resp, err
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			delay = min(delay*2, r.maxDelay)
		}
	}
}

func (r *retryPolicy) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (r *retryPolicy) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// retryable reports whether err is transient and the call safe to repeat.
func retryable(ctx context.Context, spec connect.Spec, err error) bool {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return false
	}
	switch cerr.Code() {
	case connect.CodeUnavailable, connect.CodeResourceExhausted:
	default:
		return false
	}
	if spec.IdempotencyLevel != connect.IdempotencyUnknown {
		return true
	}
	_, keyed := IdempotencyKey(ctx)
	return keyed
}

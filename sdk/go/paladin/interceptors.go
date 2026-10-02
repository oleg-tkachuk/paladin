package paladin

import (
	"context"
	"crypto/rand"
	"errors"
	mathrand "math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
)

type idempotencyKeyCtx struct{}

// idempotencyChoice is what the context says of a call's key: a key, or none
// at all. The last WithIdempotencyKey or WithoutIdempotencyKey wins.
type idempotencyChoice struct {
	key  string
	none bool
}

// WithIdempotencyKey attaches key to every call made with the returned
// context. Reuse the same key when repeating the same logical operation.
//
// Without one, a call the contract does not declare free of side effects or
// idempotent gets a fresh key of its own — or the request's idempotency_key
// field, when it has one set — and keeps it across retries. The server
// requires a key on Create* and Issue* calls and uses it on every call to
// answer a repeat with the first response.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, idempotencyChoice{key: key})
}

// WithoutIdempotencyKey sends every call made with the returned context
// without an idempotency key: not the default fresh one, not the request's
// own field. Such a call is never retried, unless the contract declares it
// free of side effects or idempotent — for an operation that must run again
// when repeated rather than be answered with the first response. The server
// refuses a Create* or Issue* call without a key.
func WithoutIdempotencyKey(ctx context.Context) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, idempotencyChoice{none: true})
}

// IdempotencyKey returns the key WithIdempotencyKey attached, if any.
func IdempotencyKey(ctx context.Context) (string, bool) {
	choice, _ := ctx.Value(idempotencyKeyCtx{}).(idempotencyChoice)
	return choice.key, !choice.none && choice.key != ""
}

// withoutKey reports whether WithoutIdempotencyKey is in force.
func withoutKey(ctx context.Context) bool {
	choice, _ := ctx.Value(idempotencyKeyCtx{}).(idempotencyChoice)
	return choice.none
}

// headerInterceptor sets the credentials on every request, and the
// idempotency key on every request that should carry one.
type headerInterceptor struct {
	headers http.Header
}

func (h *headerInterceptor) apply(dst http.Header) {
	for name, values := range h.headers {
		dst[name] = append([]string(nil), values...)
	}
}

// bodyIdempotencyKey is a request message with an idempotency_key field. The
// server refuses a call whose header and field disagree.
type bodyIdempotencyKey interface{ GetIdempotencyKey() string }

// idempotencyKeyFor picks the key a unary call sends: the context's, else the
// request's own field, else a fresh one when the call has side effects the
// contract makes no promise about. ok is false when the call needs none.
func idempotencyKeyFor(ctx context.Context, req connect.AnyRequest) (string, bool) {
	if withoutKey(ctx) {
		return "", false
	}
	if key, ok := IdempotencyKey(ctx); ok {
		return key, true
	}
	if req.Spec().IdempotencyLevel != connect.IdempotencyUnknown {
		return "", false
	}
	if body, ok := req.Any().(bodyIdempotencyKey); ok && body.GetIdempotencyKey() != "" {
		return body.GetIdempotencyKey(), true
	}
	return rand.Text(), true
}

func (h *headerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		h.apply(req.Header())
		// Before the retry interceptor, so every attempt carries the same key.
		if key, ok := idempotencyKeyFor(ctx, req); ok {
			req.Header().Set(HeaderIdempotencyKey, key)
		}
		return next(ctx, req)
	}
}

func (h *headerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		h.apply(conn.RequestHeader())
		if key, ok := IdempotencyKey(ctx); ok {
			conn.RequestHeader().Set(HeaderIdempotencyKey, key)
		}
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
	// transient decides which failures are worth another attempt; nil is
	// DefaultRetryable.
	transient func(error) bool
}

func (r *retryPolicy) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ceiling := r.baseDelay
		for attempt := 1; ; attempt++ {
			resp, err := next(ctx, req)
			if err == nil || attempt >= r.attempts || !r.retryable(req, err) {
				return resp, err
			}
			wait := r.wait(ceiling, err)
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
				// The retry could not start in time; the caller gets the
				// server's answer rather than a deadline error.
				return resp, err
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			ceiling = min(ceiling*2, r.maxDelay)
		}
	}
}

// wait is how long to pause before the next attempt: a random share of the
// ceiling, so clients that failed together do not retry together, and never
// less than the server's Retry-After.
func (r *retryPolicy) wait(ceiling time.Duration, err error) time.Duration {
	wait := time.Duration(mathrand.Int64N(int64(ceiling) + 1)) //nolint:gosec // jitter, not a secret
	if after, ok := retryAfter(err); ok && after > wait {
		wait = after
	}
	return wait
}

// retryAfter reads the server's Retry-After from a Connect error: a number of
// seconds, or an HTTP date (RFC 9110), which waits until then.
func retryAfter(err error) (time.Duration, bool) {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return 0, false
	}
	return parseRetryAfter(cerr.Meta().Get(HeaderRetryAfter), time.Now())
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}

func (r *retryPolicy) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (r *retryPolicy) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// DefaultRetryable is the failures retried unless WithRetryable says
// otherwise: Unavailable and ResourceExhausted, the two a server sends for a
// condition that passes.
func DefaultRetryable(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnavailable, connect.CodeResourceExhausted:
		return true
	default:
		return false
	}
}

// retryable reports whether err is transient and the call safe to repeat:
// declared free of side effects or idempotent, or carrying an idempotency key.
// The second half is not the classifier's to decide.
func (r *retryPolicy) retryable(req connect.AnyRequest, err error) bool {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return false
	}
	transient := r.transient
	if transient == nil {
		transient = DefaultRetryable
	}
	if !transient(err) {
		return false
	}
	if req.Spec().IdempotencyLevel != connect.IdempotencyUnknown {
		return true
	}
	return req.Header().Get(HeaderIdempotencyKey) != ""
}

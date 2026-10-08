package paladin

import (
	"context"
	"crypto/rand"
	"errors"
	mathrand "math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect/v2"
)

type idempotencyKeyCtx struct{}

// idempotencyChoice is what the context says of a call's key: a key, or none
// at all. The last WithIdempotencyKey or WithoutIdempotencyKey wins.
type idempotencyChoice struct {
	key  string
	none bool
}

// WithIdempotencyKey attaches key to the calls made with the returned context
// that the contract does not declare free of side effects or idempotent —
// those never carry a key. Reuse the same key when repeating the same logical
// operation; the server refuses one key reused for a different request to the
// same method. Upload, Download and their Many forms keep the key for the
// calls that create or complete an object and give every other call its own.
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

// ownKeys returns ctx with any WithIdempotencyKey or WithoutIdempotencyKey
// lifted, so each call made with it gets the default: a fresh key of its own.
// The helpers use it for calls they make more than once per operation —
// DownloadObject on a retry after a URL expired, PresignPart per part — which
// one shared key would make the server refuse or, before it compared
// requests, answer with the first call's response.
func ownKeys(ctx context.Context) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, idempotencyChoice{})
}

// itemKey returns ctx with the caller's key, if any, narrowed to one item of
// a bulk operation: key + "/" + item. Without it every item of UploadMany
// would share one key across different requests.
func itemKey(ctx context.Context, item string) context.Context {
	key, ok := IdempotencyKey(ctx)
	if !ok {
		return ctx
	}
	return WithIdempotencyKey(ctx, key+"/"+item)
}

// headerInterceptor sets the credentials on every request, and the
// idempotency key on every request that should carry one. It runs before
// the DPoP interceptor, which signs over the capability it sets.
type headerInterceptor struct {
	headers http.Header
	// capability is WithCapabilitySource's, read per call.
	capability func(context.Context) string
}

func (h *headerInterceptor) apply(ctx context.Context, dst *connect.Header) {
	for name, values := range h.headers {
		dst.SetValues(name, append([]string(nil), values...))
	}
	if h.capability != nil {
		if token := h.capability(ctx); token != "" {
			dst.Set(HeaderCapability, token)
		}
	}
}

// bodyIdempotencyKey is a request message with an idempotency_key field. The
// server refuses a call whose header and field disagree.
type bodyIdempotencyKey interface{ GetIdempotencyKey() string }

// idempotencyKeyFor picks the key a unary call sends: the context's, else the
// request's own field, else a fresh one when the call has side effects the
// contract makes no promise about. ok is false when the call needs none.
func idempotencyKeyFor(ctx context.Context, spec connect.Spec, req any) (string, bool) {
	if withoutKey(ctx) {
		return "", false
	}
	// A call the contract declares free of side effects or idempotent never
	// carries one, not even the context's: the server does not memoise a
	// read, and an idempotent call — RegenerateUploadUrl — must run again
	// when repeated rather than hand back the URL it is replacing.
	if spec.IdempotencyLevel != connect.IdempotencyUnknown {
		return "", false
	}
	if key, ok := IdempotencyKey(ctx); ok {
		return key, true
	}
	if body, ok := req.(bodyIdempotencyKey); ok && body.GetIdempotencyKey() != "" {
		return body.GetIdempotencyKey(), true
	}
	return rand.Text(), true
}

func (h *headerInterceptor) interceptor() connect.ClientInterceptor {
	return interceptor(h.unary, h.stream)
}

func (h *headerInterceptor) unary(next unaryFunc) unaryFunc {
	return func(ctx context.Context, spec connect.Spec, req, res any) error {
		header := callInfo(ctx).RequestHeader()
		h.apply(ctx, header)
		// Before the retry interceptor, so every attempt carries the same key.
		if key, ok := idempotencyKeyFor(ctx, spec, req); ok {
			header.Set(HeaderIdempotencyKey, key)
		}
		return next(ctx, spec, req, res)
	}
}

func (h *headerInterceptor) stream(next connect.ClientFunc) connect.ClientFunc {
	return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
		header := callInfo(ctx).RequestHeader()
		h.apply(ctx, header)
		if key, ok := IdempotencyKey(ctx); ok {
			header.Set(HeaderIdempotencyKey, key)
		}
		return next(ctx, spec)
	}
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
	observe   observer
}

func (r *retryPolicy) interceptor() connect.ClientInterceptor {
	return interceptor(r.unary, nil)
}

func (r *retryPolicy) unary(next unaryFunc) unaryFunc {
	return func(ctx context.Context, spec connect.Spec, req, res any) error {
		info := callInfo(ctx)
		ceiling := r.baseDelay
		for attempt := 1; ; attempt++ {
			clearResponse(info)
			err := next(ctx, spec, req, res)
			if err == nil || attempt >= r.attempts || !r.retryable(spec, info, err) {
				return err
			}
			wait := r.wait(ceiling, info)
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
				// The retry could not start in time; the caller gets the
				// server's answer rather than a deadline error.
				return err
			}
			r.observe.retry(ctx, RetryEvent{Procedure: spec.Procedure, Attempt: attempt, Wait: wait, Err: err})
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			ceiling = min(ceiling*2, r.maxDelay)
		}
	}
}

// wait is how long to pause before the next attempt: a random share of the
// ceiling, so clients that failed together do not retry together, and never
// less than the server's Retry-After.
func (r *retryPolicy) wait(ceiling time.Duration, info *connect.CallInfo) time.Duration {
	wait := time.Duration(mathrand.Int64N(int64(ceiling) + 1)) //nolint:gosec // jitter, not a secret
	if after, ok := retryAfter(info); ok && after > wait {
		wait = after
	}
	return wait
}

// retryAfter reads the server's Retry-After from the call's response: a number
// of seconds, or an HTTP date (RFC 9110), which waits until then.
func retryAfter(info *connect.CallInfo) (time.Duration, bool) {
	return parseRetryAfter(responseMeta(info, HeaderRetryAfter), time.Now())
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
func (r *retryPolicy) retryable(spec connect.Spec, info *connect.CallInfo, err error) bool {
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
	if spec.IdempotencyLevel != connect.IdempotencyUnknown {
		return true
	}
	return info.RequestHeader().Get(HeaderIdempotencyKey) != ""
}

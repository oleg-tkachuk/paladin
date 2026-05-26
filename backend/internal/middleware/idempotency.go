// Idempotency middleware for Connect RPCs.
//
// Two responsibilities, both at the Connect interceptor layer:
//
//  1. **Enforcement.** When RequireOnCreate=true, every RPC whose
//     method name's trailing segment starts with `Create` MUST carry
//     an `Idempotency-Key` header. Missing → CodeInvalidArgument
//     before the handler runs.
//
//  2. **Memoize / replay.** When the header IS present, the first
//     request executes the handler normally, the serialized response
//     is cached per (tenant, method, key) with a TTL, and any
//     subsequent request with the same key returns the cached
//     response without re-executing the handler. This collapses
//     retries (network flake, client double-click) onto a single
//     resource.
//
// Replay is implemented via a runtime type registry. On the first
// successful response for a given method we capture two things:
//   - reflect.Type of the *connect.Response[T] wrapper, and
//   - the proto-message factory needed to materialize a fresh *T
//     from the cached bytes.
//
// On replay we reflectively allocate a new *Response[T], unmarshal
// the cached bytes into a fresh *T, set the wrapper's exported `Msg`
// field, and return it. The reflectively-constructed value
// satisfies connect.AnyResponse because the `internalOnly()` marker
// is a method on `*Response[_]` — method sets are type-defined, not
// instance-defined, so any *Response[T] (regardless of how it was
// allocated) implements the interface.
//
// Cold-start path: the FIRST request for any (method) tuple is
// always a cache miss because the type registry is empty for that
// method. That request runs normally and populates the registry as
// a side effect of caching the response. Subsequent requests with
// the same key hit the replay path.
//
// Errors are NOT cached — a transient failure must be retryable.
// Streaming RPCs are out of scope (Create* is always unary).

package middleware

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

const idempotencyHeader = "Idempotency-Key"

// IdempotencyStore persists and returns cached responses.
type IdempotencyStore interface {
	Get(ctx context.Context, tenantID uuid.UUID, method, key string) (response []byte, sha []byte, found bool, err error)
	Put(ctx context.Context, tenantID uuid.UUID, method, key string, response, sha []byte, expiresAt time.Time) error
}

// IdempotencyConfig tunes the interceptor.
type IdempotencyConfig struct {
	// TTL is how long stored responses survive in the cache. Tune to
	// the longest plausible client retry window; default 24h covers
	// every browser refresh + most worker requeues.
	TTL time.Duration
	// SkipMethods are full Connect procedures (e.g.
	// `/paladin.admin.v1.TenantService/CreateTenant`) that bypass BOTH
	// enforcement AND memoize. Use for internal seeding RPCs whose
	// upsert semantics already give idempotency.
	SkipMethods map[string]bool
	// RequireOnCreate rejects Create* RPCs that omit the header.
	// Default false to keep the constructor drop-in safe; flip when
	// clients are guaranteed to inject (admin UI BFF does this via
	// a transport interceptor; programmatic clients must opt in).
	RequireOnCreate bool
}

// NewIdempotencyInterceptor returns a Connect interceptor that
// enforces + memoizes the Idempotency-Key contract.
func NewIdempotencyInterceptor(store IdempotencyStore, cfg IdempotencyConfig) connect.Interceptor {
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	return &idempotencyInterceptor{store: store, cfg: cfg}
}

type idempotencyInterceptor struct {
	store IdempotencyStore
	cfg   IdempotencyConfig
	// respTypes maps Connect procedure → reflect.Type of the
	// *connect.Response[T] wrapper seen on the first successful call.
	// Populated on cache miss, read on cache hit. sync.Map is the
	// right shape: write-once-per-key, read-many.
	respTypes sync.Map
}

func (i *idempotencyInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		method := req.Spec().Procedure
		if i.cfg.SkipMethods[method] {
			return next(ctx, req)
		}
		key := req.Header().Get(idempotencyHeader)
		if key == "" {
			if i.cfg.RequireOnCreate && isCreateMethod(method) {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					errors.New("idempotency: missing Idempotency-Key header"))
			}
			return next(ctx, req)
		}
		tenantID, err := auth.TenantFromContext(ctx)
		if err != nil {
			// Pre-auth or auth-failed: skip memoize, defer to next()
			// which will surface the auth error to the caller.
			return next(ctx, req)
		}

		// Replay path: cached response + known type → reconstruct
		// without invoking the handler. If we have the bytes but
		// NOT the type yet (e.g. another pod warmed the cache but
		// this pod just started), we have to fall through to next()
		// so this pod can learn the type. That's not a side-effect
		// duplicate because next() will hit the SAME row via Put's
		// ON CONFLICT DO NOTHING and the second-writer's response
		// is just dropped — but the OBSERVABLE side effect (the
		// resource row) is the one cached.
		//
		// Edge case: side-effecting handlers (CreateTenant) DO write
		// to the resource table on the warm-up call. Acceptable: the
		// resource handler is expected to use its own ON CONFLICT
		// guard on the natural key. Without that guard we'd be at
		// risk regardless of this cache.
		cached, _, found, err := i.store.Get(ctx, tenantID, method, key)
		if err == nil && found {
			if respType, ok := i.respTypes.Load(method); ok {
				replay, rerr := reconstructResponse(respType.(reflect.Type), cached)
				if rerr == nil {
					return replay, nil
				}
				// Reconstruction failure is suspicious (proto schema
				// drift, corrupt cache). Fall through to next()
				// rather than fail the request.
			}
		}

		resp, err := next(ctx, req)
		if err != nil {
			// Don't memoize failures — the caller can retry and succeed.
			return resp, err
		}

		// Cache the response. Type registry write is idempotent
		// (same method always yields the same wrapper type); the
		// store Put is ON CONFLICT DO NOTHING so concurrent
		// first-time writers race harmlessly.
		i.respTypes.Store(method, reflect.TypeOf(resp))
		bytes, merr := marshalResponse(resp)
		if merr != nil {
			// Marshal failure means we can't memoize this method.
			// The original response still flows back to the caller;
			// just log-and-skip (no logger plumbed here, so skip).
			return resp, nil
		}
		sum := sha256.Sum256(bytes)
		// Put errors are non-fatal — losing the cache write means
		// the next retry will re-run the handler. The caller still
		// gets their fresh response.
		_ = i.store.Put(ctx, tenantID, method, key, bytes, sum[:], time.Now().Add(i.cfg.TTL))
		return resp, nil
	}
}

func (i *idempotencyInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *idempotencyInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	// Streaming RPCs don't participate in idempotency.
	return next
}

// IdempotencyKeyFromHeader reads the canonical header and trims
// whitespace. Centralised so future header-name changes (e.g. a
// migration to `X-Idempotency-Token`) live in one place.
func IdempotencyKeyFromHeader(req connect.AnyRequest) string {
	return strings.TrimSpace(req.Header().Get(idempotencyHeader))
}

// marshalResponse extracts the proto message from an AnyResponse and
// marshals it to bytes. Connect responses always wrap a proto.Message
// (the generated Resp type), so the type assertion is safe in
// production; the explicit error keeps test fixtures honest.
func marshalResponse(resp connect.AnyResponse) ([]byte, error) {
	msg, ok := resp.Any().(proto.Message)
	if !ok {
		return nil, errors.New("idempotency: response message is not a proto.Message")
	}
	return proto.Marshal(msg)
}

// reconstructResponse builds a fresh *connect.Response[T] from the
// cached bytes and the wrapper's reflect.Type. The returned value
// satisfies connect.AnyResponse because the method set
// (`internalOnly`, `Any`, `Header`, `Trailer`) is defined on
// *Response[_] at the type level, not the instance level, so a
// reflectively-allocated value of the same dynamic type carries the
// same method set.
//
// Layout assumed: *connect.Response[T] has exported field `Msg *T`.
// If connect-go ever renames or unexports that field we'll get a
// hard error here at startup-time of the first replay — easier to
// catch than a silent miss.
func reconstructResponse(respType reflect.Type, body []byte) (connect.AnyResponse, error) {
	if respType.Kind() != reflect.Ptr {
		return nil, errors.New("idempotency: response type is not a pointer")
	}
	// Allocate a zero *Response[T].
	respVal := reflect.New(respType.Elem())
	msgField := respVal.Elem().FieldByName("Msg")
	if !msgField.IsValid() || msgField.Kind() != reflect.Ptr {
		return nil, errors.New("idempotency: Response.Msg field missing or not a pointer")
	}
	// Allocate a zero T, unmarshal cached bytes into it, set Msg.
	newMsg := reflect.New(msgField.Type().Elem())
	pm, ok := newMsg.Interface().(proto.Message)
	if !ok {
		return nil, errors.New("idempotency: Response.Msg's element type is not a proto.Message")
	}
	if err := proto.Unmarshal(body, pm); err != nil {
		return nil, err
	}
	msgField.Set(newMsg)
	// Type-assert back to AnyResponse. This succeeds iff respType
	// is *connect.Response[T] for some T (true at every site we
	// populate the registry — we capture reflect.TypeOf(resp) where
	// resp comes straight out of a connect handler chain).
	anyResp, ok := respVal.Interface().(connect.AnyResponse)
	if !ok {
		return nil, errors.New("idempotency: reconstructed value does not satisfy AnyResponse")
	}
	return anyResp, nil
}

// isCreateMethod reports whether the Connect procedure name
// corresponds to a resource-creating RPC. Scoped to the trailing
// method segment so a hypothetical service whose name contains
// "Create" (e.g. `CreateOrderHistoryService/ListOrders`) is NOT
// caught. Connect procedure strings are always
// `/<package>.<Service>/<Method>` per the spec.
func isCreateMethod(procedure string) bool {
	if procedure == "" {
		return false
	}
	idx := strings.LastIndex(procedure, "/")
	if idx < 0 || idx == len(procedure)-1 {
		return false
	}
	return strings.HasPrefix(procedure[idx+1:], "Create")
}

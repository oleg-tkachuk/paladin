// Idempotency middleware for Connect RPCs.
//
// Two responsibilities, both at the Connect interceptor layer:
//
//  1. **Enforcement.** When RequireOnCreate=true, every RPC whose
//     method name's trailing segment starts with `Create` OR
//     `Issue` MUST carry an `Idempotency-Key` header. Missing →
//     CodeInvalidArgument before the handler runs. The Create-vs-
//     Issue split is policy: Create matches AIP-canonical resource
//     creations (CreateTenant, CreateBucket, …); Issue matches the
//     token/credential variant (CapabilityService.Issue and
//     similar) — semantically identical double-submit hazard,
//     different domain noun.
//
//  2. **Memoize / replay.** When the header IS present, the first
//     request executes the handler normally, the serialized response
//     is cached per (tenant, method, key) with a TTL, and any
//     subsequent request with the same key returns the cached
//     response without re-executing the handler. This collapses
//     retries (network flake, client double-click) onto a single
//     resource.
//
// Replay reconstructs the cached *connect.Response[T] two ways:
//
//   - Preferred: a type-parameterized factory registered per method via
//     RegisterResponseFactory[T]. It builds the response with
//     connect.NewResponse[T] — pure generics, no Go reflection and no
//     dependency on connect-go's internal struct layout.
//
//   - Fallback (any method without a registered factory): a runtime
//     reflect.Type registry auto-populated from the first response. This
//     keeps every method working without a registration sweep; methods
//     migrate to the generic path incrementally by adding one
//     RegisterResponseFactory[T] call at wiring time.
//
// Both produce a value that satisfies connect.AnyResponse because the
// interface method set is defined on *Response[_] at the type level.
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

// responseFactory builds a fresh AnyResponse from cached proto bytes.
type responseFactory func(body []byte) (connect.AnyResponse, error)

// protoPtr constrains PT to "*T that is a proto.Message" so the generic
// factory can allocate new(T) and treat it as a proto message.
type protoPtr[T any] interface {
	*T
	proto.Message
}

// responseFactories maps a Connect procedure → its generic reconstructor.
// Process-wide (registration is a one-time wiring concern, not per
// interceptor instance).
var responseFactories sync.Map // method string → responseFactory

// RegisterResponseFactory registers the generic, reflection-free replay
// path for one memoizable method. Call once at wiring time, e.g.:
//
//	RegisterResponseFactory[adminv1.CreateTenantResponse](
//	    adminv1connect.TenantServiceCreateTenantProcedure)
//
// On replay the interceptor allocates a fresh *T, unmarshals the cached
// bytes into it, and wraps it with connect.NewResponse[T] — no Go
// reflection, no reliance on the Response struct's field names.
func RegisterResponseFactory[T any, PT protoPtr[T]](method string) {
	responseFactories.Store(method, responseFactory(func(body []byte) (connect.AnyResponse, error) {
		msg := PT(new(T))
		if err := proto.Unmarshal(body, msg); err != nil {
			return nil, err
		}
		return connect.NewResponse[T](msg), nil
	}))
}

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
	// RequireOnCreate rejects mutation-shaped RPCs that omit the
	// header. Matches method names with prefix `Create` (the AIP
	// canonical) AND `Issue` (the token/credential variant —
	// CapabilityService.Issue is the motivating case). Field name
	// is historical; see isMutationMethod() for the exact policy.
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
			if i.cfg.RequireOnCreate && isMutationMethod(method) {
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
			// Preferred: generic factory (no reflection).
			if f, ok := responseFactories.Load(method); ok {
				if replay, rerr := f.(responseFactory)(cached); rerr == nil {
					return replay, nil
				}
				// Reconstruction failure (proto drift / corrupt cache) →
				// fall through to next() rather than fail the request.
			} else if respType, ok := i.respTypes.Load(method); ok {
				// Fallback: reflection registry auto-populated on a prior
				// cache miss for an unregistered method.
				if replay, rerr := reconstructResponse(respType.(reflect.Type), cached); rerr == nil {
					return replay, nil
				}
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
	if respType.Kind() != reflect.Pointer {
		return nil, errors.New("idempotency: response type is not a pointer")
	}
	// Allocate a zero *Response[T].
	respVal := reflect.New(respType.Elem())
	msgField := respVal.Elem().FieldByName("Msg")
	if !msgField.IsValid() || msgField.Kind() != reflect.Pointer {
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

// isMutationMethod reports whether the Connect procedure name
// corresponds to a resource-creating RPC that benefits from the
// Idempotency-Key contract. Scoped to the trailing method
// segment so a hypothetical service whose name contains
// "Create" or "Issue" (e.g. `CreateOrderHistoryService/ListOrders`
// or `IssueTrackerService/ListIssues`) is NOT caught — only
// METHODS whose name starts with one of the recognised prefixes.
//
// Connect procedure strings are always `/<package>.<Service>/
// <Method>` per the spec.
//
// Recognised prefixes:
//   - "Create" — the canonical AIP-style verb. CreateTenant,
//     CreateBucket, CreateObjectKey, CreateUser, …
//   - "Issue"  — the token/credential variant. CapabilityService.
//     Issue, an IAM ApiKeyService.IssueKey (if added), etc.
//     Logically these are creates with a different domain noun,
//     and double-submit hazards are identical.
//
// Adding a new prefix here is a deliberate policy expansion —
// document the reasoning inline so the next reader doesn't add
// "Submit" or "Post" without thinking through the semantic.
func isMutationMethod(procedure string) bool {
	if procedure == "" {
		return false
	}
	idx := strings.LastIndex(procedure, "/")
	if idx < 0 || idx == len(procedure)-1 {
		return false
	}
	method := procedure[idx+1:]
	return strings.HasPrefix(method, "Create") ||
		strings.HasPrefix(method, "Issue")
}

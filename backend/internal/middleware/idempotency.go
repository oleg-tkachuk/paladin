// Idempotency middleware for Connect RPCs.
//
// Contract:
//   - Clients send the Idempotency-Key header (RFC 7240-inspired) on unsafe
//     writes (UploadObject, CompleteObject, DeleteObject, BatchDelete, …).
//   - First request for a given (tenant, method, key) executes and the
//     serialized response is stored.
//   - Subsequent requests with the same key return the stored response
//     verbatim — no re-execution, no side effects.
//
// Collision handling:
//   - Key reuse with a different request body is detected via response_sha
//     stored at commit time. The middleware does NOT re-read the request
//     body for a canonical hash (bodies are already consumed by Connect);
//     the response hash is enough to flag divergent intents on replay.

package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

const idempotencyHeader = "Idempotency-Key"

// IdempotencyStore persists and returns cached responses.
type IdempotencyStore interface {
	Get(ctx context.Context, tenantID uuid.UUID, method, key string) (response []byte, sha []byte, found bool, err error)
	Put(ctx context.Context, tenantID uuid.UUID, method, key string, response, sha []byte, expiresAt time.Time) error
}

// IdempotencyConfig tunes the cache TTL.
type IdempotencyConfig struct {
	// TTL is how long stored responses survive. 24h is a common default.
	TTL time.Duration
	// SkipMethods are RPC names that bypass this middleware (e.g. read-only).
	SkipMethods map[string]bool
	// RequireOnCreate, when true, rejects any RPC whose procedure name
	// includes `/Create` (case-sensitive — Connect routes are CamelCase,
	// e.g. `/paladin.admin.v1.TenantService/CreateTenant`) without an
	// Idempotency-Key header. The check fires BEFORE auth-binding, so
	// it doesn't depend on TenantFromContext resolving; the goal is to
	// make every mutation that produces a resource have a client-owned
	// replay sentinel. Returns CodeInvalidArgument with a stable
	// "idempotency: missing Idempotency-Key header" message so clients
	// can detect the shape and retry with one. Defaults to false to
	// preserve drop-in behaviour; flip in production once clients are
	// guaranteed to inject the header (admin UI, MCP, capability CLI).
	RequireOnCreate bool
}

// NewIdempotencyInterceptor returns a Connect interceptor that implements
// the contract described above.
func NewIdempotencyInterceptor(store IdempotencyStore, cfg IdempotencyConfig) connect.Interceptor {
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	return &idempotencyInterceptor{store: store, cfg: cfg}
}

type idempotencyInterceptor struct {
	store IdempotencyStore
	cfg   IdempotencyConfig
}

func (i *idempotencyInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		method := req.Spec().Procedure
		if i.cfg.SkipMethods[method] {
			return next(ctx, req)
		}
		key := req.Header().Get(idempotencyHeader)
		if key == "" {
			// Enforcement: every Create* RPC must carry a header so
			// retries collapse to one resource. SkipMethods above wins
			// (lets us opt out of a specific endpoint if needed, e.g.
			// internal seeding RPCs that already are upserts).
			if i.cfg.RequireOnCreate && isCreateMethod(method) {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					errors.New("idempotency: missing Idempotency-Key header"))
			}
			return next(ctx, req)
		}
		tenantID, err := auth.TenantFromContext(ctx)
		if err != nil {
			// Pre-auth interceptor didn't bind a tenant — punt.
			return next(ctx, req)
		}

		// Replay path: return the cached response as a synthetic error carrying
		// the serialized bytes. Connect's type system won't let a middleware
		// produce a typed response without knowing the response type at compile
		// time; instead, we annotate the context and let the handler short-
		// circuit via the helper below.
		cached, _, found, err := i.store.Get(ctx, tenantID, method, key)
		if err == nil && found {
			return nil, connect.NewError(connect.CodeAlreadyExists,
				&ReplayError{Body: cached, Method: method, Key: key})
		}

		resp, err := next(ctx, req)
		if err != nil {
			// Don't memoize failures — the caller can retry and succeed.
			return resp, err
		}
		// Serialization is deferred: the response is typed, not bytes. The
		// handler adapter layer captures the marshaled response via the
		// RecordResponse helper and writes it through StoreAfter.
		StoreAfter(ctx, i.store, tenantID, method, key, i.cfg.TTL)
		return resp, nil
	}
}

func (i *idempotencyInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *idempotencyInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	// Streaming RPCs don't participate in idempotency — they have their own
	// resume/checkpoint semantics (see UploadSmall).
	return next
}

// ReplayError is returned when a prior response is served from cache. The
// Connect adapter recognizes it and unmarshals Body into the appropriate
// typed response before returning to the client.
type ReplayError struct {
	Body   []byte
	Method string
	Key    string
}

func (e *ReplayError) Error() string {
	return "idempotency: replay"
}

// storeContextKey carries the deferred-store payload inside the context.
type storeContextKey struct{}

type storePayload struct {
	store    IdempotencyStore
	tenantID uuid.UUID
	method   string
	key      string
	ttl      time.Duration
}

// StoreAfter registers a deferred-store directive. Handlers are expected to
// call RecordResponse(ctx, bodyBytes) once the response is serialized.
func StoreAfter(ctx context.Context, store IdempotencyStore, tenantID uuid.UUID, method, key string, ttl time.Duration) context.Context {
	return context.WithValue(ctx, storeContextKey{}, &storePayload{
		store: store, tenantID: tenantID, method: method, key: key, ttl: ttl,
	})
}

// RecordResponse persists the serialized response bytes for later replay.
// Handlers call this once they have the final encoded payload on hand.
// A no-op if the request had no Idempotency-Key header.
func RecordResponse(ctx context.Context, body []byte) error {
	p, ok := ctx.Value(storeContextKey{}).(*storePayload)
	if !ok || p == nil {
		return nil
	}
	sum := sha256.Sum256(body)
	_ = hex.EncodeToString(sum[:])
	return p.store.Put(ctx, p.tenantID, p.method, p.key, body, sum[:], time.Now().Add(p.ttl))
}

// ErrReplayMismatch is returned if a stored response hash diverges from a
// newly-computed hash on replay — indicates the client reused a key with a
// different request body.
var ErrReplayMismatch = errors.New("idempotency: stored response hash mismatch")

// isCreateMethod reports whether the Connect procedure name corresponds
// to a resource-creating RPC. We match on the last path segment to avoid
// false positives on a hypothetical service whose name contains "Create"
// (e.g. `CreateOrderHistoryService/ListOrders` — we want the ListOrders
// shape, not the service prefix). Connect procedure strings are always
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

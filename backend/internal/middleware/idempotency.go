// Idempotency middleware for Connect RPCs.
//
// Current scope: **enforcement only**. Every RPC whose method name
// starts with `Create` is required to carry an `Idempotency-Key`
// header (when RequireOnCreate is set). A missing header on a Create*
// is rejected with InvalidArgument before the handler runs.
//
// Memoize / replay is intentionally NOT implemented here. The naive
// design would be a Get→next→Put dance in this interceptor, but
// connect-go's `AnyResponse` interface has an unexported
// `internalOnly()` marker method (see connect.go in
// connectrpc.com/connect): only types defined inside the connect
// package can implement AnyResponse. That makes it impossible for
// middleware to reconstruct a typed `*connect.Response[T]` from
// cached bytes on a replay hit — we'd need static T, which the
// middleware doesn't have. The correct implementation is per-handler
// memoization (the Stripe / AWS API Gateway pattern: each Create*
// handler does ON CONFLICT DO NOTHING ... RETURNING against the
// resource table itself, keyed by the idempotency header). That work
// is tracked in BACKLOG.md.
//
// Until then, enforcement alone gives us:
//   - Every Create* request has a server-visible client-chosen key
//     (audit trail of intent).
//   - Foundation for the per-handler memoize: when it lands, every
//     client already sends keys.
//
// The store interface + adapter are preserved (unused for now) so the
// memoize work is purely additive.

package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

const idempotencyHeader = "Idempotency-Key"

// IdempotencyStore persists and returns cached responses. Unused by
// the enforcement-only interceptor today; kept on the public surface
// so the postgres adapter and the future per-handler memoize layer
// can be wired without an additional package.
type IdempotencyStore interface {
	Get(ctx context.Context, tenantID uuid.UUID, method, key string) (response []byte, sha []byte, found bool, err error)
	Put(ctx context.Context, tenantID uuid.UUID, method, key string, response, sha []byte, expiresAt time.Time) error
}

// IdempotencyConfig tunes the enforcement gate.
type IdempotencyConfig struct {
	// TTL is how long stored responses survive when memoize lands.
	// Currently unused — kept for API stability across the two phases.
	TTL time.Duration
	// SkipMethods are RPC names (full Connect procedures, e.g.
	// `/paladin.admin.v1.TenantService/CreateTenant`) that bypass the
	// gate. Use for internal seeding RPCs whose upsert semantics make
	// idempotency-keys redundant.
	SkipMethods map[string]bool
	// RequireOnCreate, when true, rejects any RPC whose procedure
	// name's trailing segment starts with `Create` and no
	// Idempotency-Key header is present. Default false to keep the
	// constructor drop-in safe; flip when clients are guaranteed to
	// inject the header.
	RequireOnCreate bool
}

// NewIdempotencyInterceptor returns a Connect interceptor that
// enforces the Idempotency-Key contract on Create* RPCs.
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
			// retries collapse onto a single resource (once memoize
			// lands). SkipMethods above wins, letting us opt a
			// specific seeding/upsert RPC out without disabling the
			// whole policy.
			if i.cfg.RequireOnCreate && isCreateMethod(method) {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					errors.New("idempotency: missing Idempotency-Key header"))
			}
			return next(ctx, req)
		}
		// Key present: no-op today. Once per-handler memoize lands,
		// the handler reads ctx-scoped key + tenant and does its
		// own ON CONFLICT DO NOTHING upsert against the resource
		// table. The middleware deliberately stays out of the
		// response-capture business (see file header).
		return next(ctx, req)
	}
}

func (i *idempotencyInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *idempotencyInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	// Streaming RPCs don't participate in idempotency — they have
	// their own resume/checkpoint semantics (see UploadSmall).
	return next
}

// IdempotencyKeyFromHeader is a small helper for handlers that want
// to read the header from a Connect request. Centralises the canonical
// header name and trims surrounding whitespace.
func IdempotencyKeyFromHeader(req connect.AnyRequest) string {
	return strings.TrimSpace(req.Header().Get(idempotencyHeader))
}

// isCreateMethod reports whether the Connect procedure name
// corresponds to a resource-creating RPC. We match on the last path
// segment to avoid false positives on a hypothetical service whose
// name contains "Create" (e.g. `CreateOrderHistoryService/ListOrders` —
// we want the trailing-method shape, not the service prefix). Connect
// procedure strings are always `/<package>.<Service>/<Method>` per
// the spec.
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

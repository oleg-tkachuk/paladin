package middleware

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/config"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// defaultGRPCTimeout is applied when the caller does not set a deadline.
	defaultGRPCTimeout = 30 * time.Second

	// grpcMetaRequestID is the gRPC metadata key for request correlation IDs.
	grpcMetaRequestID = "x-request-id"

	// grpcMetaTenantID is the gRPC metadata key for tenant identification.
	grpcMetaTenantID = "x-tenant-id"

	// grpcMetaAuthorization is the standard authorization header.
	grpcMetaAuthorization = "authorization"
)

// grpcSkippedMethods contains full method names that bypass tenant enforcement.
// gRPC health checks and reflection are always exempt.
var grpcSkippedMethods = map[string]bool{
	"/grpc.health.v1.Health/Check":                                   true,
	"/grpc.health.v1.Health/Watch":                                   true,
	"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo": true,
	"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo":      true,
}

// GRPCValidator is implemented by request messages that can self-validate.
// The ValidationInterceptor checks for this interface and calls Validate()
// before passing the request to the handler.
type GRPCValidator interface {
	Validate() error
}

// SetupGRPCInterceptors returns the ordered unary server interceptor chain.
// Order matters: each interceptor wraps all subsequent ones.
func SetupGRPCInterceptors(cfg *config.Config, log *zap.Logger) []grpc.UnaryServerInterceptor {
	rl := newGRPCRateLimiter(cfg)

	return []grpc.UnaryServerInterceptor{
		// 1. Panic recovery — must be outermost to catch all panics.
		RecoveryInterceptor(log),
		// 2. Deadline enforcement — attach default timeout when client omits one.
		DeadlineInterceptor(defaultGRPCTimeout),
		// 3. RequestID — extract from metadata or generate; propagate in trailer.
		RequestIDInterceptor(),
		// 4. Rate limiting — per-tenant token bucket; shares logic with HTTP limiter.
		GRPCRateLimitInterceptor(cfg, rl),
		// 5. Context logger — enrich logger with request_id for all downstream logs.
		ContextLoggerInterceptor(log),
		// 6. Access logger — log every RPC with method, code, tenant, latency.
		LoggerInterceptor(log),
		// 7. Auth — extract tenant_id from metadata into context.
		AuthInterceptor(cfg),
		// 8. Tenant enforcement — reject requests without tenant when auth enabled.
		EnforceTenantInterceptor(cfg),
		// 9. Validation — call req.Validate() on messages that implement GRPCValidator.
		ValidationInterceptor(),
	}
}

// RecoveryInterceptor catches panics and translates them to codes.Internal.
func RecoveryInterceptor(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("gRPC panic recovered",
					zap.Any("panic", r),
					zap.String("method", info.FullMethod),
				)
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()

		return handler(ctx, req)
	}
}

// DeadlineInterceptor attaches a default timeout when the incoming context
// has no deadline set by the caller.
func DeadlineInterceptor(defaultTimeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
			defer cancel()
		}

		return handler(ctx, req)
	}
}

// RequestIDInterceptor extracts x-request-id from incoming metadata or generates
// a new UUID if absent. The ID is stored in context and propagated back to the
// caller via the gRPC response trailer.
func RequestIDInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		rid := ""
		if vals := md.Get(grpcMetaRequestID); len(vals) > 0 && vals[0] != "" {
			rid = vals[0]
		}

		if rid == "" {
			rid = uuid.New().String()
		}

		// Store in context for downstream use.
		ctx = context.WithValue(ctx, utils.RequestIDKey, rid)

		// Propagate back to caller via trailer.
		if err := grpc.SetTrailer(ctx, metadata.Pairs(grpcMetaRequestID, rid)); err != nil {
			// Non-fatal: trailer propagation is best-effort.
			_ = err
		}

		return handler(ctx, req)
	}
}

// ContextLoggerInterceptor enriches the context logger with the request ID
// so all downstream log calls automatically include it.
func ContextLoggerInterceptor(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		rid := utils.RequestIDFromContext(ctx, "")
		l := log

		if rid != "" {
			l = l.With(zap.String("request_id", rid))
		}

		ctx = logger.WithContext(ctx, l)

		return handler(ctx, req)
	}
}

// LoggerInterceptor logs every RPC call with method, gRPC code, tenant, and
// latency. Uses Warn for client errors (4xx-class codes) and Error for server
// errors (5xx-class). Success calls are logged at Info.
func LoggerInterceptor(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		code := status.Code(err)
		durationMs := float64(time.Since(start).Nanoseconds()) / 1e6

		tenantID := utils.TenantIDFromContext(ctx, "")
		rid := utils.RequestIDFromContext(ctx, "")

		fields := []zap.Field{
			zap.String("method", info.FullMethod),
			zap.String("code", code.String()),
			zap.Float64("duration_ms", durationMs),
			zap.String("tenant_id", tenantID),
			zap.String("request_id", rid),
		}

		switch {
		case err == nil:
			log.Info("gRPC request", fields...)
		case isClientError(code):
			log.Warn("gRPC client error", append(fields, zap.Error(err))...)
		default:
			log.Error("gRPC server error", append(fields, zap.Error(err))...)
		}

		return resp, err
	}
}

// isClientError returns true for gRPC codes that map to HTTP 4xx (caller's fault).
func isClientError(c codes.Code) bool {
	switch c {
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists,
		codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition,
		codes.Aborted, codes.OutOfRange, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// AuthInterceptor extracts the tenant identity from incoming metadata and
// stores it in context. It does not reject requests — that is done by
// EnforceTenantInterceptor so the order of responsibilities is clear.
func AuthInterceptor(cfg *config.Config) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)

		tenantID := ""

		// Trust x-tenant-id header only when the flag is explicitly enabled.
		if cfg.Security.TrustTenantIDFromRequest {
			if vals := md.Get(grpcMetaTenantID); len(vals) > 0 {
				tenantID = vals[0]
			}
		}

		// Admin bearer key — grants system-level access without a real tenant.
		authHeader := ""
		if vals := md.Get(grpcMetaAuthorization); len(vals) > 0 {
			authHeader = vals[0]
		}

		if tenantID == "" && cfg.Auth.AdminKey != "" && authHeader == "Bearer "+cfg.Auth.AdminKey {
			tenantID = utils.SystemAdminTenant
		}

		// When auth is disabled (local dev), fall back to a well-known sentinel.
		if tenantID == "" && !cfg.Auth.Enabled {
			tenantID = utils.DefaultTenant
		}

		if tenantID != "" {
			ctx = context.WithValue(ctx, utils.TenantIDKey, tenantID)
		}

		return handler(ctx, req)
	}
}

// EnforceTenantInterceptor rejects requests that have no tenant in context
// when auth is enabled. Health and reflection methods are always exempt.
func EnforceTenantInterceptor(cfg *config.Config) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip enforcement for well-known infrastructure methods.
		if grpcSkippedMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		if cfg.Auth.Enabled {
			tenant := utils.TenantIDFromContext(ctx, "")
			if tenant == "" {
				return nil, status.Error(codes.Unauthenticated,
					"missing tenant: supply x-tenant-id metadata or a valid Authorization header")
			}
		}

		return handler(ctx, req)
	}
}

// ValidationInterceptor calls req.Validate() when the request message
// implements GRPCValidator. Returns codes.InvalidArgument on failure.
func ValidationInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if v, ok := req.(GRPCValidator); ok {
			if err := v.Validate(); err != nil {
				var appErr *apperrors.AppError
				if aerr, yes := err.(*apperrors.AppError); yes {
					appErr = aerr
					_ = appErr
				}

				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		}

		return handler(ctx, req)
	}
}

// GRPCRateLimitInterceptor enforces per-tenant rate limits using the shared
// TenantRateLimiter. Returns codes.ResourceExhausted when the limit is exceeded.
func GRPCRateLimitInterceptor(cfg *config.Config, rl *TenantRateLimiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !cfg.RateLimit.Enabled {
			return handler(ctx, req)
		}

		tenantID := utils.TenantIDFromContext(ctx, "unknown")

		if !rl.GetLimiter(tenantID).Allow() {
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded for tenant %q; retry after a moment", tenantID)
		}

		return handler(ctx, req)
	}
}

// newGRPCRateLimiter creates a TenantRateLimiter from config for gRPC use.
func newGRPCRateLimiter(cfg *config.Config) *TenantRateLimiter {
	return NewTenantRateLimiter(
		rate.Limit(cfg.RateLimit.RequestsPerSecond),
		cfg.RateLimit.Burst,
		cfg.RateLimit.MaxTenants,
		cfg.RateLimit.CleanupTTL,
		cfg.RateLimit.CleanupInterval,
	)
}

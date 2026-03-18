package middleware

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/config"

	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// protovalidateMessage is satisfied by any protobuf message.
type protovalidateMessage = proto.Message

// protovalidateValidator is the package-level validator for buf.validate annotations.
var protovalidateValidator, _ = protovalidate.New()

type tenantGetter interface {
	GetTenantId() string
}

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
		// 6. Auth — extract tenant_id from metadata into context.
		AuthInterceptor(cfg),
		// 7. Access logger — log every RPC with method, code, tenant, latency.
		// Moved after Auth so tenantID is available in context.
		LoggerInterceptor(),
		// 8. Tenant enforcement — reject requests without tenant when auth enabled.
		EnforceTenantInterceptor(cfg),
		// 9. Validation — enforce buf.validate annotations via protovalidate.
		GRPCProtoValidationInterceptor(),
	}
}

// SetupConnectInterceptors returns the ordered interceptor chain for Connect RPC.
func SetupConnectInterceptors(cfg *config.Config, log *zap.Logger) []connect.Interceptor {
	rl := newGRPCRateLimiter(cfg)

	return []connect.Interceptor{
		ConnectRecoveryInterceptor(log),
		ConnectRequestIDInterceptor(),
		ConnectContextLoggerInterceptor(log),
		ConnectAuthInterceptor(cfg),
		ConnectLoggerInterceptor(),
		ConnectEnforceTenantInterceptor(cfg),
		ConnectValidationInterceptor(),
		ConnectRateLimitInterceptor(cfg, rl),
		ConnectAuditLogInterceptor(),
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
		// Just ensure the base logger is in context.
		// logger.FromContext will enrich it with request/tenant/trace IDs.
		ctx = logger.WithContext(ctx, log)

		return handler(ctx, req)
	}
}

// LoggerInterceptor logs every RPC call with method, gRPC code, tenant, and
// latency. Uses Warn for client errors (4xx-class codes) and Error for server
// errors (5xx-class). Success calls are logged at Info.
func LoggerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		code := status.Code(err)
		durationMs := float64(time.Since(start).Nanoseconds()) / 1e6

		tenantID := utils.TenantIDFromContext(ctx, "")
		rid := utils.RequestIDFromContext(ctx, "")

		// Use the context-enriched logger to include request_id and trace_id
		log := logger.FromContext(ctx)

		fields := []zap.Field{
			zap.String("protocol", "grpc"),
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

// GRPCProtoValidationInterceptor validates request messages against
// buf.validate annotations using protovalidate. Returns codes.InvalidArgument on failure.
func GRPCProtoValidationInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if msg, ok := req.(protovalidateMessage); ok {
			if err := protovalidateValidator.Validate(msg); err != nil {
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

// Connect RequestID Interceptor
func ConnectRequestIDInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			rid := req.Header().Get(grpcMetaRequestID)
			if rid == "" {
				rid = uuid.New().String()
			}
			ctx = context.WithValue(ctx, utils.RequestIDKey, rid)
			res, err := next(ctx, req)
			if err == nil && res != nil {
				res.Header().Set(grpcMetaRequestID, rid)
			}

			return res, err
		}
	})
}

// Connect Context Logger Interceptor
func ConnectContextLoggerInterceptor(log *zap.Logger) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Just ensure the base logger is in context.
			ctx = logger.WithContext(ctx, log)

			return next(ctx, req)
		}
	})
}

// Connect Logger Interceptor
func ConnectLoggerInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			res, err := next(ctx, req)
			durationMs := float64(time.Since(start).Nanoseconds()) / 1e6

			// Use context-enriched logger
			log := logger.FromContext(ctx)

			fields := []zap.Field{
				zap.String("protocol", "connect"),
				zap.String("method", req.Spec().Procedure),
				zap.Float64("duration_ms", durationMs),
			}

			if err != nil {
				lerr := connect.CodeOf(err)
				fields = append(fields, zap.String("code", lerr.String()))
				log.Error("Connect request error", append(fields, zap.Error(err))...)
			} else {
				log.Info("Connect request", fields...)
			}

			return res, err
		}
	})
}

// Connect Auth Interceptor
func ConnectAuthInterceptor(cfg *config.Config) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			tenantID := ""
			if cfg.Security.TrustTenantIDFromRequest {
				tenantID = req.Header().Get(grpcMetaTenantID)
			}

			authHeader := req.Header().Get(grpcMetaAuthorization)
			if tenantID == "" && cfg.Auth.AdminKey != "" && authHeader == "Bearer "+cfg.Auth.AdminKey {
				tenantID = utils.SystemAdminTenant
			}

			if tenantID == "" && !cfg.Auth.Enabled {
				tenantID = utils.DefaultTenant
			}

			// If still empty, try to extract from the request message itself.
			if tenantID == "" || tenantID == utils.DefaultTenant {
				if msg, ok := req.Any().(tenantGetter); ok {
					if tid := msg.GetTenantId(); tid != "" {
						tenantID = tid
					}
				}
			}

			if tenantID != "" {
				ctx = context.WithValue(ctx, utils.TenantIDKey, tenantID)
			}

			return next(ctx, req)
		}
	})
}

// Connect Enforce Tenant Interceptor
func ConnectEnforceTenantInterceptor(cfg *config.Config) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure
			if grpcSkippedMethods[procedure] {
				return next(ctx, req)
			}

			if cfg.Auth.Enabled {
				tenant := utils.TenantIDFromContext(ctx, "")
				if tenant == "" {
					return nil, connect.NewError(connect.CodeUnauthenticated,
						errors.New("missing tenant: supply x-tenant-id header or a valid Authorization header"))
				}
			}

			return next(ctx, req)
		}
	})
}

// ConnectValidationInterceptor validates proto messages against buf.validate
// annotations (e.g. string.uuid, string.min_len, repeated.min_items).
func ConnectValidationInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if msg, ok := req.Any().(protovalidateMessage); ok {
				if err := protovalidateValidator.Validate(msg); err != nil {
					return nil, connect.NewError(connect.CodeInvalidArgument, err)
				}
			}

			return next(ctx, req)
		}
	})
}

// Connect Rate Limit Interceptor
func ConnectRateLimitInterceptor(cfg *config.Config, rl *TenantRateLimiter) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !cfg.RateLimit.Enabled {
				return next(ctx, req)
			}

			tenantID := utils.TenantIDFromContext(ctx, "unknown")
			if !rl.GetLimiter(tenantID).Allow() {
				return nil, connect.NewError(connect.CodeResourceExhausted,
					fmt.Errorf("rate limit exceeded for tenant %q", tenantID))
			}

			return next(ctx, req)
		}
	})
}

// ConnectRecoveryInterceptor catches panics in Connect RPC handlers and logs them.
func ConnectRecoveryInterceptor(log *zap.Logger) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (res connect.AnyResponse, err error) {
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					logger.FromContext(ctx).Error("Connect RPC panic recovered",
						zap.Any("panic", r),
						zap.String("method", req.Spec().Procedure),
						zap.ByteString("stacktrace", stack),
					)
					err = connect.NewError(connect.CodeInternal, fmt.Errorf("internal server error"))
				}
			}()

			return next(ctx, req)
		}
	})
}

// extractComponentName converts a gRPC method like /paladin.v1.ObjectService/GetObjectMetadata
// or /grpc.health.v1.Health/Check into a snake_case component name like "object_service".
func extractComponentName(fullMethod string) string {
	parts := strings.Split(fullMethod, "/")
	if len(parts) < 3 {
		return "grpc"
	}
	// Service name is typically at index 1 e.g. "paladin.v1.ObjectService"
	svcParts := strings.Split(parts[1], ".")
	svcName := svcParts[len(svcParts)-1]

	// Convert CamelCase to snake_case
	var result strings.Builder
	for i, r := range svcName {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}

	if result.Len() == 0 {
		return "grpc"
	}
	return result.String()
}

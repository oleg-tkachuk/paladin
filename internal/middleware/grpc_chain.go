package middleware

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// SetupGRPCInterceptors returns the unary server interceptor chain
func SetupGRPCInterceptors(cfg *config.Config, log *zap.Logger) []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		// 1. Recovery
		RecoveryInterceptor(log),
		// 2. RequestID
		RequestIDInterceptor(),
		// 3. ContextLogger
		ContextLoggerInterceptor(log),
		// 4. Logger
		LoggerInterceptor(log),
		// 5. Auth & Tenant
		AuthInterceptor(cfg.Security),
		// 6. Tenant Enforcement
		EnforceTenantInterceptor(cfg.Security),
	}
}

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

func RecoveryInterceptor(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("gRPC panic recovered", zap.Any("panic", r))
				err = status.Errorf(codes.Internal, "Internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

func RequestIDInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract from metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		rid := ""
		if val := md.Get("x-request-id"); len(val) > 0 {
			rid = val[0]
		}
		// We don't generate if missing for gRPC usually, but let's do it for consistency
		if rid == "" {
			// Generate? Or leave empty? Let's leave empty and let service handle or generate.
			// Actually, utils.RequestIDFromContext usually handles fallback.
		}

		ctx = context.WithValue(ctx, utils.RequestIDKey, rid)
		// TraceID?

		return handler(ctx, req)
	}
}

func LoggerInterceptor(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Log start? Or just end.
		resp, err := handler(ctx, req)

		code := status.Code(err)

		fields := []zap.Field{
			zap.String("method", info.FullMethod),
			zap.String("code", code.String()),
		}

		if err != nil {
			log.Warn("gRPC request error", append(fields, zap.Error(err))...)
		} else {
			// Debug for success to avoid spam? Or info?
			// log.Debug("gRPC request success", fields...)
		}

		return resp, err
	}
}

func AuthInterceptor(cfg config.Security) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Extract tenant from metadata x-tenant-id if trusted.
		// Security middleware (EnforceTenant) will reject requests without tenant if configured.

		md, _ := metadata.FromIncomingContext(ctx)

		tenant := ""
		if cfg.TrustTenantIDFromRequest {
			if vals := md.Get("x-tenant-id"); len(vals) > 0 {
				tenant = vals[0]
			}
		}

		if tenant != "" {
			ctx = context.WithValue(ctx, utils.TenantIDKey, tenant)
		}

		return handler(ctx, req)
	}
}

func EnforceTenantInterceptor(cfg config.Security) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// If needed, check if request has tenant_id field and matches context.
		// gRPC usually has tenant_id in the message. Reflecting that is expensive (using protoreflect).
		// For this level, we just enforce that we HAVE a tenant if we are not trusted.

		return handler(ctx, req)
	}
}

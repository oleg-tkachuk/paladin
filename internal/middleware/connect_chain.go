package middleware

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// SetupConnectInterceptors returns the ordered Connect interceptor chain.
func SetupConnectInterceptors(cfg *config.Config, log *zap.Logger, auditRepo domain.AuditLogRepository) []connect.Interceptor {
	rl := NewTenantRateLimiter(
		rate.Limit(cfg.RateLimit.RequestsPerSecond),
		cfg.RateLimit.Burst,
		cfg.RateLimit.MaxTenants,
		cfg.RateLimit.CleanupTTL,
		cfg.RateLimit.CleanupInterval,
	)

	return []connect.Interceptor{
		ConnectRecoveryInterceptor(log),
		ConnectRequestIDInterceptor(),
		ConnectAuthInterceptor(cfg),
		ConnectEnforceTenantInterceptor(cfg),
		ConnectRateLimitInterceptor(cfg, rl),
		ConnectAuditLogInterceptor(auditRepo),
	}
}

// ConnectRequestIDInterceptor ensures a request ID is present in context and header.
func ConnectRequestIDInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			requestID := req.Header().Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}

			ctx = context.WithValue(ctx, utils.RequestIDKey, requestID)
			res, err := next(ctx, req)
			if res != nil {
				res.Header().Set("X-Request-ID", requestID)
			}
			return res, err
		}
	})
}

// ConnectAuthInterceptor extracts tenant ID and actor information.
func ConnectAuthInterceptor(cfg *config.Config) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !cfg.Auth.Enabled {
				ctx = context.WithValue(ctx, utils.TenantIDKey, utils.DefaultTenant)
				return next(ctx, req)
			}

			// 1. Try Authorization header for Admin Key
			auth := req.Header().Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token := strings.TrimPrefix(auth, "Bearer ")
				if token == cfg.Auth.AdminKey {
					ctx = context.WithValue(ctx, utils.TenantIDKey, utils.SystemAdminTenant)
					ctx = logger.WithActor(ctx, "system-admin")
					return next(ctx, req)
				}
			}

			// 2. Try X-Tenant-ID if trusted
			if cfg.Security.TrustTenantIDFromRequest {
				tenantID := req.Header().Get("X-Tenant-ID")
				if tenantID != "" {
					ctx = context.WithValue(ctx, utils.TenantIDKey, tenantID)
					return next(ctx, req)
				}
			}

			return next(ctx, req)
		}
	})
}

// ConnectEnforceTenantInterceptor ensures a tenant ID is present if auth is enabled.
func ConnectEnforceTenantInterceptor(cfg *config.Config) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !cfg.Auth.Enabled {
				return next(ctx, req)
			}

			tenantID := utils.TenantIDFromContext(ctx, "")
			if tenantID == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("tenant identification required"))
			}

			return next(ctx, req)
		}
	})
}

// ConnectRateLimitInterceptor applies per-tenant rate limiting.
func ConnectRateLimitInterceptor(cfg *config.Config, rl *TenantRateLimiter) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !cfg.RateLimit.Enabled {
				return next(ctx, req)
			}

			tenantID := utils.TenantIDFromContext(ctx, "")
			if tenantID == "" || tenantID == utils.SystemAdminTenant {
				return next(ctx, req)
			}

			limiter := rl.GetLimiter(tenantID)
			if !limiter.Allow() {
				return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("rate limit exceeded for tenant: %s", tenantID))
			}

			return next(ctx, req)
		}
	})
}

// ConnectRecoveryInterceptor recovers from panics in handlers.
func ConnectRecoveryInterceptor(log *zap.Logger) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			defer func() {
				if r := recover(); r != nil {
					log.Error("recovered from panic in Connect handler",
						zap.Any("panic", r),
						zap.String("stack", string(debug.Stack())),
						zap.String("procedure", req.Spec().Procedure),
					)
				}
			}()
			return next(ctx, req)
		}
	})
}

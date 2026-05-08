package utils

import "context"

type ctxKey string

const (
	RequestIDKey ctxKey = "requestID"
	TenantIDKey  ctxKey = "tenantID"
)

func RequestIDFromContext(ctx context.Context, fallback string) string {
	if v := ctx.Value(RequestIDKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

func TenantIDFromContext(ctx context.Context, fallback string) string {
	if v := ctx.Value(TenantIDKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, TenantIDKey, tenantID)
}

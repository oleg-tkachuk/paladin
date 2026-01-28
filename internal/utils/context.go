package utils

import "context"

type ctxKey string

const (
	TraceIDKey   ctxKey = "traceID"   // backward compatibility
	RequestIDKey ctxKey = "requestID" // preferred
	TenantIDKey  ctxKey = "tenantID"
)

func RequestIDFromContext(ctx context.Context, fallback string) string {
	if v := ctx.Value(RequestIDKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	// fallback to legacy traceID
	if v := ctx.Value(TraceIDKey); v != nil {
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

func TraceIDFromContext(ctx context.Context, fallback string) string {
	if v := ctx.Value(TraceIDKey); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

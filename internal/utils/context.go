package utils

import "context"

type ctxKey string

const (
	TraceIDKey   ctxKey = "traceID"   // backward compatibility
	RequestIDKey ctxKey = "requestID" // preferred
	TenantIDKey  ctxKey = "tenantID"
)

// Sentinel tenant identifiers used internally. These are never real tenant IDs.
const (
	// DefaultTenant is used when auth is disabled (local dev / test).
	DefaultTenant = "1eae4beb-cf17-4ddd-908d-299de9614d59"
	// SystemAdminTenant identifies requests authenticated via the admin key.
	SystemAdminTenant = "f02ddb45-84bb-494f-b29e-326c62a37b72"
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

func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, TenantIDKey, tenantID)
}

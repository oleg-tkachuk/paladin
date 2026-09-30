// Package reqctx carries per-request identifiers on a context: the request ID
// and the tenant ID, which the logger attaches to every line.
package reqctx

import "context"

type ctxKey string

const (
	requestIDKey ctxKey = "requestID"
	tenantIDKey  ctxKey = "tenantID"
)

// WithRequestID returns ctx carrying requestID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// WithTenantID returns ctx carrying tenantID.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantIDKey, tenantID)
}

// RequestID returns the request ID on ctx, or fallback when there is none.
func RequestID(ctx context.Context, fallback string) string {
	return stringValue(ctx, requestIDKey, fallback)
}

// TenantID returns the tenant ID on ctx, or fallback when there is none.
func TenantID(ctx context.Context, fallback string) string {
	return stringValue(ctx, tenantIDKey, fallback)
}

func stringValue(ctx context.Context, key ctxKey, fallback string) string {
	if s, ok := ctx.Value(key).(string); ok && s != "" {
		return s
	}
	return fallback
}

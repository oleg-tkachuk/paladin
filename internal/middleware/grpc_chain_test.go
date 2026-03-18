package middleware

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectRequestIDInterceptor(t *testing.T) {
	interceptor := ConnectRequestIDInterceptor()

	// 1. Existing Request ID
	req1 := &connect.Request[any]{}
	req1.Header().Set("X-Request-ID", "existing-id")

	next1 := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		rid := utils.RequestIDFromContext(ctx, "")
		assert.Equal(t, "existing-id", rid)

		return &connect.Response[any]{}, nil
	}

	fn1 := interceptor.WrapUnary(next1)
	res1, err := fn1(context.Background(), req1)
	require.NoError(t, err)
	assert.Equal(t, "existing-id", res1.Header().Get("X-Request-ID"))

	// 2. New Request ID
	req2 := &connect.Request[any]{}
	next2 := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		rid := utils.RequestIDFromContext(ctx, "")
		assert.NotEmpty(t, rid)

		return &connect.Response[any]{}, nil
	}

	fn2 := interceptor.WrapUnary(next2)
	res2, err := fn2(context.Background(), req2)
	require.NoError(t, err)
	assert.NotEmpty(t, res2.Header().Get("X-Request-ID"))
}

func TestConnectAuthInterceptor(t *testing.T) {
	cfg := &config.Config{
		Auth: config.Auth{
			Enabled:  true,
			AdminKey: "secret-admin-key",
		},
		Security: config.Security{
			TrustTenantIDFromRequest: true,
		},
	}

	interceptor := ConnectAuthInterceptor(cfg)

	// 1. Trust x-tenant-id
	req1 := &connect.Request[any]{}
	req1.Header().Set("X-Tenant-ID", "tenant-123")

	next1 := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		tenant := utils.TenantIDFromContext(ctx, "")
		assert.Equal(t, "tenant-123", tenant)

		return &connect.Response[any]{}, nil
	}

	fn1 := interceptor.WrapUnary(next1)
	_, err := fn1(context.Background(), req1)
	require.NoError(t, err)

	// 2. Admin Key
	req2 := &connect.Request[any]{}
	req2.Header().Set("Authorization", "Bearer secret-admin-key")

	next2 := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		tenant := utils.TenantIDFromContext(ctx, "")
		assert.Equal(t, utils.SystemAdminTenant, tenant)

		return &connect.Response[any]{}, nil
	}

	fn2 := interceptor.WrapUnary(next2)
	_, err = fn2(context.Background(), req2)
	require.NoError(t, err)

	// 3. Auth Disabled -> Default Tenant
	cfgDisabled := &config.Config{Auth: config.Auth{Enabled: false}}
	interceptorDisabled := ConnectAuthInterceptor(cfgDisabled)
	req3 := &connect.Request[any]{}

	next3 := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		tenant := utils.TenantIDFromContext(ctx, "")
		assert.Equal(t, utils.DefaultTenant, tenant)

		return &connect.Response[any]{}, nil
	}

	fn3 := interceptorDisabled.WrapUnary(next3)
	_, err = fn3(context.Background(), req3)
	require.NoError(t, err)
}

func TestConnectEnforceTenantInterceptor(t *testing.T) {
	cfg := &config.Config{Auth: config.Auth{Enabled: true}}
	interceptor := ConnectEnforceTenantInterceptor(cfg)

	// 1. Success with tenant
	req1 := &connect.Request[any]{}
	ctx1 := context.WithValue(context.Background(), utils.TenantIDKey, "tenant-1")

	next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return &connect.Response[any]{}, nil
	}

	fn1 := interceptor.WrapUnary(next)
	_, err := fn1(ctx1, req1)
	require.NoError(t, err)

	// 2. Failure without tenant
	req2 := &connect.Request[any]{}
	ctx2 := context.Background()

	fn2 := interceptor.WrapUnary(next)
	_, err = fn2(ctx2, req2)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestConnectRateLimitInterceptor(t *testing.T) {
	cfg := &config.Config{RateLimit: config.RateLimit{Enabled: true}}
	rl := NewTenantRateLimiter(100, 100, 100, time.Minute, time.Minute)
	interceptor := ConnectRateLimitInterceptor(cfg, rl)

	next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return &connect.Response[any]{}, nil
	}

	// 1. Allowed
	req1 := &connect.Request[any]{}
	ctx1 := context.WithValue(context.Background(), utils.TenantIDKey, "tenant-1")
	fn1 := interceptor.WrapUnary(next)
	_, err := fn1(ctx1, req1)
	require.NoError(t, err)

	// 2. Rate limited (using a tiny bucket for test)
	rl_limited := NewTenantRateLimiter(1, 1, 100, time.Minute, time.Minute)
	interceptor_limited := ConnectRateLimitInterceptor(cfg, rl_limited)

	fn2 := interceptor_limited.WrapUnary(next)
	_, err = fn2(ctx1, req1)
	require.NoError(t, err) // first allowed

	_, err = fn2(ctx1, req1)
	require.Error(t, err) // second limited
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
}

func TestConnectRecoveryInterceptor(t *testing.T) {
	// ... we will use zap.NewNop() for testing.
	// Zap doesn't provide an easy way to verify log contents without custom setup,
	// but we can at least test that it recovers.
}

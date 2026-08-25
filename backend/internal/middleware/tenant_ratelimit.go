package middleware

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/time/rate"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// TenantRateLimitInterceptor caps how fast one tenant can call the data and
// admin planes.
//
// TenantRateLimiter existed with tests for a long time and nothing ever
// constructed it, so the only limits actually in force were the login window
// (credential stuffing) and the per-api-token window. A tenant authenticating
// with a JWT — the console, or any service integrating against the API — had
// no ceiling at all, and one runaway client could saturate the connection
// pool for everyone on the shared Postgres.
//
// Keyed on the caller's own tenant, from the authenticated principal. The
// acting-tenant marker a platform admin sets to reach another tenant's rows is
// established inside the handler, after this interceptor has run, so a platform
// admin's traffic is charged to the platform tenant however many tenants it
// touches. That is the right attribution here: the bucket bounds who is
// generating load, not whose rows it lands on.
//
// Requests with no tenant in context are passed through. Those are the
// pre-auth surfaces (health, login), which have their own defences; charging
// them all to one shared bucket would let any unauthenticated caller deny
// service to the rest.
type TenantRateLimitInterceptor struct {
	limiter *TenantRateLimiter
}

// TenantRateLimitConfig is the operator-facing shape. RPS <= 0 disables
// throttling: the interceptor still sits in the chain and passes everything
// through, which is safer than handing callers something that may be nil in a
// list Connect will happily dereference.
type TenantRateLimitConfig struct {
	// RPS is the sustained per-tenant request rate.
	RPS float64
	// Burst is how far above RPS a tenant may spike before being throttled.
	// <= 0 derives a burst of one second's worth of RPS (minimum 1).
	Burst int
	// MaxTenants bounds the limiter map; least-recently-used buckets are
	// evicted past it so a tenant-id flood cannot grow it without limit.
	MaxTenants int
	// IdleTTL drops a tenant's bucket after this long without traffic.
	IdleTTL time.Duration
	// SweepInterval is how often idle buckets are collected.
	SweepInterval time.Duration
}

// NewTenantRateLimitInterceptor builds the interceptor. A config with
// RPS <= 0 yields a pass-through: no limiter is constructed, so no sweep
// goroutine runs either.
func NewTenantRateLimitInterceptor(cfg TenantRateLimitConfig) *TenantRateLimitInterceptor {
	if cfg.RPS <= 0 {
		return &TenantRateLimitInterceptor{}
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = int(cfg.RPS)
		if burst < 1 {
			burst = 1
		}
	}
	if cfg.MaxTenants <= 0 {
		cfg.MaxTenants = 10_000
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 10 * time.Minute
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = time.Minute
	}
	return &TenantRateLimitInterceptor{
		limiter: NewTenantRateLimiter(
			rate.Limit(cfg.RPS), burst, cfg.MaxTenants, cfg.IdleTTL, cfg.SweepInterval,
		),
	}
}

func (i *TenantRateLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.limiter == nil {
			return next(ctx, req)
		}
		tenant, err := auth.EffectiveTenant(ctx)
		if err != nil {
			// Unauthenticated or tenantless: not this limiter's business.
			return next(ctx, req)
		}
		id := tenant.String()
		if !i.limiter.GetLimiter(id).Allow() {
			recordTenantRateLimitDecision(ctx, id, false)
			cerr := connect.NewError(connect.CodeResourceExhausted,
				errors.New("per-tenant request rate exceeded; retry shortly"))
			// Retry-After is what makes this actionable for an integrating
			// service: ResourceExhausted alone does not say whether to back
			// off or give up.
			cerr.Meta().Set("Retry-After", "1")
			return nil, cerr
		}
		recordTenantRateLimitDecision(ctx, id, true)
		return next(ctx, req)
	}
}

func (i *TenantRateLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler charges one token at stream open. Per-message
// accounting would need the limiter inside the message loop; the streams here
// are event subscriptions, whose cost is in the subscription, not the frame.
func (i *TenantRateLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i.limiter == nil {
			return next(ctx, conn)
		}
		tenant, err := auth.EffectiveTenant(ctx)
		if err != nil {
			return next(ctx, conn)
		}
		id := tenant.String()
		if !i.limiter.GetLimiter(id).Allow() {
			recordTenantRateLimitDecision(ctx, id, false)
			return connect.NewError(connect.CodeResourceExhausted,
				errors.New("per-tenant request rate exceeded; retry shortly"))
		}
		recordTenantRateLimitDecision(ctx, id, true)
		return next(ctx, conn)
	}
}

var _ connect.Interceptor = (*TenantRateLimitInterceptor)(nil)

// ─── Metrics ────────────────────────────────────────────────────────────────

// Cardinality discipline matches internal/auth's api_token instruments:
// tenant_id is the granularity, `allowed` is a bounded enum, and the
// procedure is deliberately left out — it would multiply the series count by
// the whole API surface.
var (
	tenantRLMetricsOnce sync.Once
	tenantRLDecisions   metric.Int64Counter
)

func recordTenantRateLimitDecision(ctx context.Context, tenantID string, allowed bool) {
	tenantRLMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/middleware")
		tenantRLDecisions, _ = meter.Int64Counter(
			"paladin.tenant.ratelimit.decisions",
			metric.WithDescription(
				"Per-tenant request rate-limit decisions. A rising denied count means a "+
					"tenant is being throttled — either a runaway client or a cap set too low."),
		)
	})
	if tenantRLDecisions == nil {
		return
	}
	tenantRLDecisions.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.Bool("allowed", allowed),
	))
}

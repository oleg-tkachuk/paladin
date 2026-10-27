package middleware

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// TenantRateLimitInterceptor caps how fast one tenant can call the data and
// admin planes.
//
// The limit is shared across replicas: the counters live in
// tenant_rate_buckets, the same sliding-window shape api_token uses. An
// in-memory bucket per process would give each pod its own ceiling, so the
// configured rate would silently be multiplied by the replica count — 600/s
// at the chart's default of two api replicas, where the operator wrote 300.
//
// The cost is one statement per request. That is the trade a shared limit
// requires, and it is the same trade the api_token verify path already makes
// on every PAT request.
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
	store TenantRateStore
	// capacity is the trailing-60s ceiling. Zero disables the interceptor.
	capacity int
}

// TenantRateStore is the storage seam: one call that bumps the tenant's
// current bucket and returns the weighted count over the trailing minute
// together with the seconds left in the bucket.
type TenantRateStore interface {
	BumpTenantRate(ctx context.Context, tenantID uuid.UUID) (weighted float64, retryAfterSeconds float64, err error)
}

// TenantRateLimitConfig is the operator-facing shape. RPS <= 0 disables
// throttling: the interceptor still sits in the chain and passes everything
// through, which is safer than handing callers something that may be nil in a
// list Connect will happily dereference.
type TenantRateLimitConfig struct {
	// RPS is the sustained per-tenant request rate. It becomes a per-minute
	// ceiling (RPS × 60) because the window is a minute wide.
	RPS float64
	// Store holds the shared counters. Nil disables throttling for the same
	// reason RPS <= 0 does — there is nowhere to count.
	Store TenantRateStore
}

// NewTenantRateLimitInterceptor builds the interceptor. RPS <= 0 or a nil
// store yields a pass-through.
//
// Burst is not a separate knob here. A trailing-minute window admits a spike
// on its own — a tenant idle for the previous minute may spend the whole
// minute's budget at once — so a second burst parameter would only describe
// the same slack twice.
func NewTenantRateLimitInterceptor(cfg TenantRateLimitConfig) *TenantRateLimitInterceptor {
	if cfg.RPS <= 0 || cfg.Store == nil {
		return &TenantRateLimitInterceptor{}
	}
	capacity := int(cfg.RPS * 60)
	if capacity < 1 {
		capacity = 1
	}
	return &TenantRateLimitInterceptor{store: cfg.Store, capacity: capacity}
}

// admit bumps the tenant's window and decides. A store error admits the
// request and is counted: refusing every tenant because Postgres hiccuped
// would turn a database blip into a full outage, which is the same call the
// api_token limiter makes. The counter is what makes the silence visible —
// while it is non-zero the ceiling is not in force.
func (i *TenantRateLimitInterceptor) admit(ctx context.Context, tenant uuid.UUID) (bool, float64) {
	weighted, retryAfter, err := i.store.BumpTenantRate(ctx, tenant)
	if err != nil {
		recordTenantRateLimitFailOpen(ctx, tenant.String())
		return true, 0
	}
	return weighted <= float64(i.capacity), retryAfter
}

// Intercept charges one token per call, at open. A stream the client sends
// on — client-streaming or bidi — is charged one more for every message after
// the first, inside the message loop: charged at open alone, a tenant could
// hold one stream open and push messages through it at any rate without the
// limiter noticing. A server-streaming call's frames are the server's to
// pace, so its one client message is the one token.
func (i *TenantRateLimitInterceptor) Intercept(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		if i.store == nil {
			return next(ctx, spec, stream)
		}
		tenant, err := auth.EffectiveTenant(ctx)
		if err != nil {
			// Unauthenticated or tenantless: not this limiter's business.
			return next(ctx, spec, stream)
		}
		id := tenant.String()
		allowed, retryAfter := i.admit(ctx, tenant)
		recordTenantRateLimitDecision(ctx, id, allowed)
		if !allowed {
			// Retry-After is what makes this actionable for an integrating
			// service: ResourceExhausted alone does not say whether to back
			// off or give up.
			unary.Info(ctx).ResponseHeader().Set(paladin.HeaderRetryAfter, retryAfterHeader(retryAfter))
			return connect.NewError(connect.CodeResourceExhausted,
				"per-tenant request rate exceeded; retry shortly")
		}
		if spec.StreamType&connect.StreamTypeClient != 0 {
			stream = &meteredStream{ServerStream: stream, limiter: i, ctx: ctx, tenant: tenant}
		}
		return next(ctx, spec, stream)
	}
}

// meteredStream charges the tenant's window for each message the client
// sends after the first, which the call's admission already paid for.
type meteredStream struct {
	connect.ServerStream
	limiter  *TenantRateLimitInterceptor
	ctx      context.Context
	tenant   uuid.UUID
	received int
}

// Receive reads the next message and charges it. A message past the ceiling
// ends the stream with ResourceExhausted; the response headers may already
// be on the wire, so the wait is in the message rather than Retry-After.
func (m *meteredStream) Receive(msg any) error {
	if err := m.ServerStream.Receive(msg); err != nil {
		return err
	}
	m.received++
	if m.received == 1 {
		return nil
	}
	allowed, retryAfter := m.limiter.admit(m.ctx, m.tenant)
	recordTenantRateLimitDecision(m.ctx, m.tenant.String(), allowed)
	if !allowed {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Sprintf(
			"per-tenant request rate exceeded; retry in %ss", retryAfterHeader(retryAfter)))
	}
	return nil
}

// retryAfterHeader renders the seconds left in the bucket, rounded up and
// floored at 1: a client told to retry in 0 seconds retries immediately and
// is refused again.
func retryAfterHeader(seconds float64) string {
	s := int(math.Ceil(seconds))
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s)
}

// ─── Metrics ────────────────────────────────────────────────────────────────

// Cardinality discipline matches internal/auth's api_token instruments:
// tenant_id is the granularity, `allowed` is a bounded enum, and the
// procedure is deliberately left out — it would multiply the series count by
// the whole API surface.
var (
	tenantRLMetricsOnce sync.Once
	tenantRLDecisions   metric.Int64Counter
	tenantRLFailOpen    metric.Int64Counter
)

func initTenantRLMetrics() {
	tenantRLMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/oleg-tkachuk/paladin/internal/middleware")
		tenantRLDecisions, _ = meter.Int64Counter(
			"paladin.tenant.ratelimit.decisions",
			metric.WithDescription(
				"Per-tenant request rate-limit decisions. A rising denied count means a "+
					"tenant is being throttled — either a runaway client or a cap set too low."),
		)
		tenantRLFailOpen, _ = meter.Int64Counter(
			"paladin.tenant.ratelimit.fail_open",
			metric.WithDescription(
				"Requests admitted without a rate-limit decision because the bucket store "+
					"errored. Non-zero means the per-tenant ceiling is not in force."),
		)
	})
}

func recordTenantRateLimitFailOpen(ctx context.Context, tenantID string) {
	initTenantRLMetrics()
	if tenantRLFailOpen == nil {
		return
	}
	tenantRLFailOpen.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tenant_id", tenantID),
	))
}

func recordTenantRateLimitDecision(ctx context.Context, tenantID string, allowed bool) {
	initTenantRLMetrics()
	if tenantRLDecisions == nil {
		return
	}
	tenantRLDecisions.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.Bool("allowed", allowed),
	))
}

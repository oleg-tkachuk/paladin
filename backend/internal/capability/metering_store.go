package capability

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// MeteringStore is a UsageStore decorator that emits OTel metrics on
// every counter mutation. Sits between the auth interceptor /
// handler-side helpers and the concrete (postgres) implementation,
// so the store implementation stays free of metrics concerns and
// every UsageStore user gets the same observability surface.
//
// All instruments are no-ops until otel.SetMeterProvider runs (default
// in test paths; the cmd/server boot path wires the real provider
// before BuildSharedDeps). Cardinality discipline lives in metrics.go.
type MeteringStore[TX any] struct {
	Inner UsageStore[TX]
}

// WithMetering wraps an inner UsageStore with the metering decorator.
// nil inner → nil out (a UsageStore was never wired, so no-op).
func WithMetering[TX any](inner UsageStore[TX]) UsageStore[TX] {
	if inner == nil {
		return nil
	}
	return &MeteringStore[TX]{Inner: inner}
}

// BumpRequest emits paladin.capability.request.bumps with outcome=
// allowed | limit_exceeded.
func (s *MeteringStore[TX]) BumpRequest(ctx context.Context, capID uuid.UUID, maxRequests int64) (int64, error) {
	count, err := s.Inner.BumpRequest(ctx, capID, maxRequests)
	if errors.Is(err, ErrRequestLimitExceeded) {
		recordRequestBump(ctx, uuid.Nil, "limit_exceeded")
		return 0, err
	}
	if err == nil {
		recordRequestBump(ctx, uuid.Nil, "allowed")
	}
	return count, err
}

// Charge emits paladin.capability.charge.{amount,decisions,
// current_spend}.
//
// Outcome accounting:
//
//   - allowed              → both per-cap and per-tenant caps fit
//   - cap_exceeded         → per-cap cap rejected (tenant counter
//     never touched)
//   - tenant_exceeded      → tenant aggregate cap rejected after
//     the per-cap cap accepted; inner store
//     already compensated the per-cap row
func (s *MeteringStore[TX]) Charge(
	ctx context.Context,
	capID uuid.UUID,
	amount, maxBudget float64,
	unitCode string,
	tenantID uuid.UUID,
	op string,
	actor string,
	onCharged func(ctx context.Context, tx TX) error,
) (float64, error) {
	spent, err := s.Inner.Charge(ctx, capID, amount, maxBudget, unitCode, tenantID, op, actor, onCharged)
	switch {
	case err == nil:
		recordChargeAttempt(ctx, tenantID, amount, spent, "allowed")
	case errors.Is(err, ErrBudgetExceeded):
		recordChargeAttempt(ctx, tenantID, amount, 0, "cap_exceeded")
	case errors.Is(err, ErrTenantBudgetExceeded):
		recordChargeAttempt(ctx, tenantID, amount, 0, "tenant_exceeded")
	}
	return spent, err
}

func (s *MeteringStore[TX]) RefundCapability(ctx context.Context, capID uuid.UUID, amount float64) error {
	if err := s.Inner.RefundCapability(ctx, capID, amount); err != nil {
		return err
	}
	recordRefund(ctx, uuid.Nil, amount, "capability")
	return nil
}

func (s *MeteringStore[TX]) RefundTenant(ctx context.Context, tenantID uuid.UUID, amount float64) error {
	if err := s.Inner.RefundTenant(ctx, tenantID, amount); err != nil {
		return err
	}
	recordRefund(ctx, tenantID, amount, "tenant")
	return nil
}

// Pure pass-throughs — Get/Set don't move counters, no metric.

func (s *MeteringStore[TX]) Get(ctx context.Context, capID uuid.UUID) (Usage, error) {
	return s.Inner.Get(ctx, capID)
}

func (s *MeteringStore[TX]) GetTenantBudget(ctx context.Context, tenantID uuid.UUID) (TenantBudget, error) {
	return s.Inner.GetTenantBudget(ctx, tenantID)
}

func (s *MeteringStore[TX]) SetTenantBudget(ctx context.Context, args SetTenantBudgetArgs) (TenantBudget, error) {
	return s.Inner.SetTenantBudget(ctx, args)
}

func (s *MeteringStore[TX]) ListTenantBudgets(ctx context.Context, args ListTenantBudgetsArgs) ([]TenantBudgetSummary, error) {
	return s.Inner.ListTenantBudgets(ctx, args)
}

func (s *MeteringStore[TX]) Delete(ctx context.Context, capID uuid.UUID) error {
	return s.Inner.Delete(ctx, capID)
}

func (s *MeteringStore[TX]) PurgeOrphans(ctx context.Context) (int64, error) {
	return s.Inner.PurgeOrphans(ctx)
}

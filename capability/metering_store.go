package capability

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// MeteringStore is a UsageStore decorator that emits OTel metrics on
// every counter mutation, so the store implementation stays free of
// metrics concerns and every UsageStore user gets the same observability
// surface.
//
// All instruments are no-ops until otel.SetMeterProvider runs.
// Cardinality discipline lives in metrics.go.
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
func (s *MeteringStore[TX]) BumpRequest(ctx context.Context, req RequestBump) (int64, error) {
	count, err := s.Inner.BumpRequest(ctx, req)
	switch {
	case errors.Is(err, ErrRequestLimitExceeded):
		recordRequestBump(ctx, req.TenantID, "limit_exceeded")
	case err == nil:
		recordRequestBump(ctx, req.TenantID, "allowed")
	}
	return count, err
}

// Charge emits paladin.capability.charge.{amount,decisions,current_spend}.
//
// Outcome accounting:
//
//   - allowed         → every ceiling admitted the charge
//   - cap_exceeded    → the capability's ceiling or an ancestor's rejected
//   - tenant_exceeded → the tenant aggregate ceiling rejected
func (s *MeteringStore[TX]) Charge(
	ctx context.Context,
	req ChargeRequest,
	onCharged func(ctx context.Context, tx TX) error,
) (ChargeReceipt, error) {
	receipt, err := s.Inner.Charge(ctx, req, onCharged)
	unit := req.UnitCode
	if unit == "" {
		unit = DefaultUnitCode
	}
	switch {
	case err == nil:
		recordChargeAttempt(ctx, req.TenantID, unit, req.Amount, receipt.Spent, "allowed")
	case errors.Is(err, ErrBudgetExceeded):
		recordChargeAttempt(ctx, req.TenantID, unit, req.Amount, 0, "cap_exceeded")
	case errors.Is(err, ErrTenantBudgetExceeded):
		recordChargeAttempt(ctx, req.TenantID, unit, req.Amount, 0, "tenant_exceeded")
	}
	return receipt, err
}

// Refund emits paladin.capability.refund.amount.
func (s *MeteringStore[TX]) Refund(ctx context.Context, req RefundRequest) (float64, error) {
	refunded, err := s.Inner.Refund(ctx, req)
	if err == nil && refunded > 0 {
		recordRefund(ctx, refunded)
	}
	return refunded, err
}

// Pure pass-throughs — reads and admin writes don't move counters, no metric.

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

package billingh

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// An allowed request acts on the tenant it names, not the caller's own:
// that is the tenant the row-level security of the ledger is bound to.
func TestAuthorizeActsOnTheRequestedTenant(t *testing.T) {
	h := NewHandler(nil, nil, allowAuthorizer{})
	ctx := ctxWithAdmin(t)
	other := uuid.New()
	acting, err := h.authorize(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := auth.EffectiveTenant(acting); err != nil || got != other {
		t.Errorf("effective tenant = %s, %v; want the requested %s", got, err, other)
	}
	if _, err := NewHandler(nil, nil, denyAuthorizer{}).authorize(ctx, other); err == nil {
		t.Error("a denied request was authorised")
	}
}

// budgetReader answers GetTenantBudget with err; the rest of the store is
// never reached.
type budgetReader struct {
	limes.UsageStore[pgx.Tx]
	budget limes.TenantBudget
	err    error
}

func (b budgetReader) GetTenantBudget(context.Context, uuid.UUID) (limes.TenantBudget, error) {
	return b.budget, b.err
}

// A budget the summary cannot read is logged, naming the tenant; a tenant
// with no budget is not an error and logs nothing.
func TestTenantBudgetLogsAnUnreadableBudget(t *testing.T) {
	cases := map[string]struct {
		err     error
		wantOK  bool
		wantLog bool
	}{
		"read":         {nil, true, false},
		"no budget":    {limes.ErrTenantBudgetNotFound, false, false},
		"store failed": {errors.New("connection reset"), false, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.WarnLevel)
			ctx := logger.WithContext(context.Background(), zap.New(core))
			tenant := uuid.New()
			h := NewHandler(nil, budgetReader{budget: limes.TenantBudget{TenantID: tenant}, err: c.err}, allowAuthorizer{})
			if _, ok := h.tenantBudget(ctx, tenant); ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			got := logs.FilterField(zap.String("tenant_id", tenant.String())).Len()
			if (got > 0) != c.wantLog {
				t.Errorf("%d warnings naming the tenant, want logged = %v", got, c.wantLog)
			}
		})
	}
}

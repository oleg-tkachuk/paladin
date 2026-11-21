package billingh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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
	meter.UsageStore
	budget meter.TenantBudget
	err    error
}

func (b budgetReader) GetTenantBudget(context.Context, uuid.UUID) (meter.TenantBudget, error) {
	return b.budget, b.err
}

// A budget the store cannot read is an error, answered by its limes code; a
// tenant with no budget, or no store to read one from, is no budget.
func TestTenantBudget(t *testing.T) {
	boom := errors.New("connection reset")
	cases := map[string]struct {
		usage   meter.UsageStore
		wantOK  bool
		wantErr error
	}{
		"read":         {budgetReader{}, true, nil},
		"no budget":    {budgetReader{err: meter.ErrTenantBudgetNotFound}, false, nil},
		"no store":     {nil, false, nil},
		"store failed": {budgetReader{err: boom}, false, boom},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tenant := uuid.New()
			h := NewHandler(nil, c.usage, allowAuthorizer{})
			_, ok, err := h.tenantBudget(context.Background(), tenant)
			if ok != c.wantOK || !errors.Is(err, c.wantErr) || (err == nil) != (c.wantErr == nil) {
				t.Errorf("ok = %v, err = %v; want %v, %v", ok, err, c.wantOK, c.wantErr)
			}
		})
	}
}

// The platform's currency goes to a caller that may read its own tenant's
// billing, and to no one else.
func TestCurrencyIsTheConfiguredOneBehindTheBillingGate(t *testing.T) {
	ctx := ctxWithAdmin(t)
	got, err := NewHandler(nil, nil, allowAuthorizer{}).WithCurrency("EUR").Currency(ctx)
	if err != nil || got != "EUR" {
		t.Errorf("Currency = %q, %v; want EUR", got, err)
	}
	if _, err := NewHandler(nil, nil, denyAuthorizer{}).WithCurrency("EUR").Currency(ctx); err == nil {
		t.Error("a denied caller was told the currency")
	}
	if _, err := NewHandler(nil, nil, allowAuthorizer{}).Currency(context.Background()); err == nil {
		t.Error("an unauthenticated caller was told the currency")
	}
}

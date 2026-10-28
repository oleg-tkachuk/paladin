package postgres

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// tenantRecorder records the tenant an insert is scoped to.
type tenantRecorder struct {
	limes.IssuanceStore
	scoped uuid.UUID
	scope  bool
}

func (r *tenantRecorder) Insert(ctx context.Context, _ limes.InsertRequest) error {
	r.scoped, _ = auth.EffectiveTenant(ctx)
	_, err := auth.EffectiveTenant(ctx)
	r.scope = err == nil
	return nil
}

// An insert through TenantScopedIssuance is scoped to its capability's
// tenant, which Scope then sets on the transaction.
func TestTenantScopedIssuance(t *testing.T) {
	tenant := uuid.New()
	rec := &tenantRecorder{}
	err := TenantScopedIssuance(rec).Insert(context.Background(), limes.InsertRequest{
		Capability: limes.Capability{Subject: limes.Principal{TenantID: tenant}},
	})
	if err != nil || !rec.scope || rec.scoped != tenant {
		t.Errorf("insert scoped to %s (%v, %v), want %s", rec.scoped, rec.scope, err, tenant)
	}
}

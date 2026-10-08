package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/capability"
)

// tenantRecordingUsage records the tenant each store call would bind to
// paladin.tenant_id. The capability_usage, tenant budget and charges rows are
// all RLS-isolated by the capability's tenant, so a call made on any other
// tenant — or none — is refused by the database with 42501.
type tenantRecordingUsage struct {
	*fakeUsage
	seen map[string]uuid.UUID
}

func newTenantRecordingUsage() *tenantRecordingUsage {
	return &tenantRecordingUsage{fakeUsage: newFakeUsage(), seen: map[string]uuid.UUID{}}
}

func (u *tenantRecordingUsage) record(ctx context.Context, call string) {
	tid, err := EffectiveTenant(ctx)
	if err != nil {
		tid = uuid.Nil
	}
	u.seen[call] = tid
}

func (u *tenantRecordingUsage) Bump(ctx context.Context, req capability.BumpRequest) (int64, error) {
	u.record(ctx, "Bump")
	return u.fakeUsage.Bump(ctx, req)
}

func (u *tenantRecordingUsage) Charge(ctx context.Context, req capability.ChargeRequest, onCharged func(context.Context, pgx.Tx) error) (capability.ChargeReceipt, error) {
	u.record(ctx, "Charge")
	return u.fakeUsage.Charge(ctx, req, onCharged)
}

func (u *tenantRecordingUsage) Refund(ctx context.Context, req capability.RefundRequest) (capability.Nanos, error) {
	u.record(ctx, "Refund")
	return u.fakeUsage.Refund(ctx, req)
}

// Every write to a capability's ledger runs on that capability's tenant. The
// request-count bump runs before the interceptor establishes a principal, so a
// capability-only request had no tenant at all and every capped capability
// failed with Unavailable; a JWT of another tenant carried alongside one had
// the wrong tenant, with the same result.
func TestCapabilityLedgerWritesRunOnTheCapabilitysTenant(t *testing.T) {
	const (
		chargeAmount capability.Nanos = capability.NanosPerUnit / 2
		maxRequests                   = 5
		maxBudget    capability.Nanos = 10 * capability.NanosPerUnit
	)
	capTenant := uuid.New()
	cases := map[string]context.Context{
		"capability only": context.Background(),
		"another tenant's principal alongside": WithPrincipal(context.Background(),
			&Principal{TenantID: uuid.New(), Subject: "user:admin"}),
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			usage := newTenantRecordingUsage()
			c := capWithCaveats(capability.Caveats{MaxRequests: maxRequests, MaxBudgetAmount: maxBudget})
			c.Subject = capability.Principal{TenantID: capTenant, Subject: "agent:a"}
			i := &capabilityInterceptor{usage: usage}

			if err := i.enforceCaveats(base, c); err != nil {
				t.Fatalf("enforceCaveats: %v", err)
			}
			ctx := withLastOpHolder(WithChargeStore(WithCapability(base, c), usage))
			if err := ChargeCapability(ctx, chargeAmount, ""); err != nil {
				t.Fatalf("ChargeCapability: %v", err)
			}
			if err := RefundLastCharge(ctx, chargeAmount); err != nil {
				t.Fatalf("RefundLastCharge: %v", err)
			}

			for _, call := range []string{"Bump", "Charge", "Refund"} {
				got, ok := usage.seen[call]
				if !ok {
					t.Errorf("%s was not called", call)
					continue
				}
				if got != capTenant {
					t.Errorf("%s ran on tenant %v, want the capability's %v", call, got, capTenant)
				}
			}
			if _, err := PrincipalFromContext(base); name == "capability only" && err == nil {
				t.Error("scoping the ledger write must not leak a principal into the caller's context")
			}
		})
	}
}

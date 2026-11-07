//go:build integration

// paladin.capability.charged from the charge to a subscriber: auth's
// ChargeCapability against the Postgres meter on the RLS-enforced pool, the
// event adapter the app wires (app.NewChargeEmitter), its outbox row on the
// charge's own transaction, and the envelope on NATS. The unit tests fake the
// dispatcher and transactional_outbox_test.go fakes the event; neither runs
// the two together.
package integration

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// chargedEventType is the event a capability charge fans out.
const chargedEventType = "paladin.capability.charged"

// chargedAmount is what the test charges: a whole unit and a fraction, so
// both halves of the Money payload carry a value.
var chargedAmount = limes.MustParseAmount("1.25")

// chargedUnits and chargedNanos are chargedAmount as google.type.Money.
const (
	chargedUnits = "1"
	chargedNanos = 250_000_000
)

func TestChargeCapability_ChargedEventReachesSubscriber(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "capability-charged-events")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	// A capability the verifier accepted, put on record in Postgres: the
	// meter refuses to count one it cannot find.
	minter := captest.NewMinter(t)
	verified, _ := minter.Mint(t, limes.Capability{
		Subject: limes.Principal{TenantID: tenant},
		Caveats: limes.Caveats{Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: seedUnitCode}},
	})
	records, err := capabilitypg.New(f.h.PoolMigrate)
	if err != nil {
		t.Fatalf("capability store: %v", err)
	}
	if err := records.Insert(ctx, limes.InsertRequest{
		Capability: verified.Capability(),
		IssuedBy:   limes.Principal{Type: limes.PrincipalTypeService, Subject: captest.Issuer},
	}); err != nil {
		t.Fatalf("put the capability on record: %v", err)
	}

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	callCtx := auth.WithCapability(ctx, verified)
	callCtx = auth.WithChargeStore(callCtx, newUsageStore(t, f.h.PoolApp))
	callCtx = auth.WithChargeEventEmitter(callCtx, app.NewChargeEmitter(probe.d, zap.NewNop()))

	if err := auth.ChargeCapability(callCtx, chargedAmount); err != nil {
		t.Fatalf("ChargeCapability: %v", err)
	}
	if n := countCharges(t, f.h, tenant); n != 1 {
		t.Fatalf("charges rows = %d, want 1", n)
	}

	raw := probe.expect(t, f, tenant.String(), chargedEventType)
	var env struct {
		Data struct {
			TenantID     string `json:"tenant_id"`
			CapabilityID string `json:"capability_id"`
			Amount       struct {
				CurrencyCode string `json:"currency_code"`
				Units        string `json:"units"`
				Nanos        int32  `json:"nanos"`
			} `json:"amount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope data: %v (raw: %s)", err, raw)
	}
	if env.Data.TenantID != tenant.String() {
		t.Errorf("data.tenant_id = %q, want %q", env.Data.TenantID, tenant)
	}
	if want := verified.Capability().ID.String(); env.Data.CapabilityID != want {
		t.Errorf("data.capability_id = %q, want %q", env.Data.CapabilityID, want)
	}
	if got := env.Data.Amount; got.CurrencyCode != seedUnitCode || got.Units != chargedUnits || got.Nanos != chargedNanos {
		t.Errorf("data.amount = %+v, want %s %s.%09d", got, seedUnitCode, chargedUnits, chargedNanos)
	}
}

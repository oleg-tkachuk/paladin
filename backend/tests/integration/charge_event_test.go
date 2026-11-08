//go:build integration

// paladin.capability.charged and paladin.capability.refunded from the meter
// to a subscriber: auth's ChargeCapability and RefundLastCharge against the
// Postgres meter on the RLS-enforced pool, the event adapter the app wires
// (app.NewChargeEmitter), its outbox row on the charge's or the refund's own
// transaction, and the envelope on NATS. The unit tests fake the dispatcher
// and transactional_outbox_test.go fakes the event; neither runs the two
// together.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// errAfterCharge is the failure of a handler's step after its charge.
var errAfterCharge = errors.New("the step after the charge failed")

// chargeThenFail stands in for a handler that charges and then fails, as one
// whose presigned URL could not be issued does.
type chargeThenFail struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
}

func (chargeThenFail) GetVersion(ctx context.Context, _ *iamv1.GetVersionRequest) (*iamv1.VersionInfo, error) {
	if err := auth.ChargeCapability(ctx, chargedAmount); err != nil {
		return nil, err
	}
	return nil, connect.NewError(connect.CodeUnavailable, errAfterCharge.Error())
}

// chargedAmount is what the test charges: a whole unit and a fraction, so
// both halves of the Money payload carry a value.
var chargedAmount = limes.MustParseAmount("1.25")

// chargedUnits and chargedNanos are chargedAmount as google.type.Money.
const (
	chargedUnits = "1"
	chargedNanos = 250_000_000
)

// chargeEventEnvelope is the part of a charge or refund event's envelope the
// tests read.
type chargeEventEnvelope struct {
	Data struct {
		TenantID     string `json:"tenant_id"`
		CapabilityID string `json:"capability_id"`
		ChargeID     string `json:"charge_id"`
		Amount       struct {
			CurrencyCode string `json:"currency_code"`
			Units        string `json:"units"`
			Nanos        int32  `json:"nanos"`
		} `json:"amount"`
	} `json:"data"`
}

// checkChargeEvent decodes raw and checks it reports chargedAmount for the
// capability on tenant, returning the charge it names.
func checkChargeEvent(t *testing.T, raw []byte, tenant uuid.UUID, capability limes.VerifiedCapability) uuid.UUID {
	t.Helper()
	var env chargeEventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope data: %v (raw: %s)", err, raw)
	}
	if env.Data.TenantID != tenant.String() {
		t.Errorf("data.tenant_id = %q, want %q", env.Data.TenantID, tenant)
	}
	if want := capability.Capability().ID.String(); env.Data.CapabilityID != want {
		t.Errorf("data.capability_id = %q, want %q", env.Data.CapabilityID, want)
	}
	if got := env.Data.Amount; got.CurrencyCode != seedUnitCode || got.Units != chargedUnits || got.Nanos != chargedNanos {
		t.Errorf("data.amount = %+v, want %s %s.%09d", got, seedUnitCode, chargedUnits, chargedNanos)
	}
	chargeID, err := uuid.Parse(env.Data.ChargeID)
	if err != nil || chargeID == uuid.Nil() {
		t.Errorf("data.charge_id = %q, want the charge's ledger id", env.Data.ChargeID)
	}
	return chargeID
}

// mintOnRecord mints an unlimited capability for tenant and puts it on record
// in Postgres: the meter refuses to count one it cannot find.
func mintOnRecord(t *testing.T, f *dispatcherFixture, tenant uuid.UUID) (*captest.Minter, limes.VerifiedCapability, string) {
	t.Helper()
	minter := captest.NewMinter(t)
	verified, token := minter.Mint(t, limes.Capability{
		Subject: limes.Principal{TenantID: tenant},
		Caveats: limes.Caveats{Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: seedUnitCode}},
	})
	records, err := capabilitypg.New(f.h.PoolMigrate)
	if err != nil {
		t.Fatalf("capability store: %v", err)
	}
	if err := records.Insert(context.Background(), limes.InsertRequest{
		Capability: verified.Capability(),
		IssuedBy:   limes.Principal{Type: limes.PrincipalTypeService, Subject: captest.Issuer},
	}); err != nil {
		t.Fatalf("put the capability on record: %v", err)
	}
	return minter, verified, token
}

func TestChargeCapability_ChargedEventReachesSubscriber(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "capability-charged-events")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")
	_, verified, _ := mintOnRecord(t, f, tenant)

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	callCtx := auth.WithCapability(ctx, verified)
	callCtx = auth.WithChargeStore(callCtx, newUsageStore(t, f.h.PoolApp))
	callCtx = auth.WithChargeEventEmitter(callCtx, app.NewChargeEmitter(probe.events, zap.NewNop()))

	if err := auth.ChargeCapability(callCtx, chargedAmount); err != nil {
		t.Fatalf("ChargeCapability: %v", err)
	}
	if n := countCharges(t, f.h, tenant); n != 1 {
		t.Fatalf("charges rows = %d, want 1", n)
	}

	checkChargeEvent(t, probe.expect(t, f, tenant.String(), eventtype.CapabilityCharged), tenant, verified)
}

// A call that fails after charging is refunded by the interceptor, and the
// refund reaches a subscriber naming the charge it returns: a subscriber
// summing charged events takes it back out, and nets to nothing.
func TestFailedCall_RefundedEventReachesSubscriber(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)
	url := runEmbeddedNATSForIntegration(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "capability-refunded-events")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")
	minter, verified, token := mintOnRecord(t, f, tenant)

	probe := newLifecycleEventProbe(t, f, tenant.String(), url)
	interceptor := auth.CapabilityEstablishingInterceptor(minter.Verifier, captest.Audience,
		newUsageStore(t, f.h.PoolApp), 0, app.NewChargeEmitter(probe.events, zap.NewNop()))
	server := connect.NewServer(interceptor)
	paladiniamv1connect.RegisterHealthServiceHandler(server, chargeThenFail{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set(auth.HeaderCapability, token)
	_, err := paladiniamv1connect.NewHealthServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))).
		GetVersion(ctx, &iamv1.GetVersionRequest{})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("call = %v, want the handler's Unavailable", err)
	}

	events := probe.expectEach(t, f, tenant.String(), eventtype.CapabilityCharged, eventtype.CapabilityRefunded)
	charged := checkChargeEvent(t, events[eventtype.CapabilityCharged], tenant, verified)
	refunded := checkChargeEvent(t, events[eventtype.CapabilityRefunded], tenant, verified)
	if refunded != charged {
		t.Errorf("refunded charge_id = %s, want the charged event's %s", refunded, charged)
	}
}

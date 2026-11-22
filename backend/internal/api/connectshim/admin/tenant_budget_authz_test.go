package admin

import (
	"context"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/genproto/googleapis/type/money"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	"github.com/oleg-tkachuk/paladin/backend/policies"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// errStoreDown is the store failing, answered Unavailable.
var errStoreDown = fmt.Errorf("%w: test store down", limes.ErrStoreUnavailable)

// codeOK stands for a call that succeeds in a table of expected codes:
// connect.Code has no value for it.
const codeOK connect.Code = 0

// budgetCaller is one caller in the role table: its roles, and whether it
// belongs to the tenant whose budget it asks for. none is the request with
// no principal at all.
type budgetCaller struct {
	name      string
	roles     []string
	ownTenant bool
	none      bool
}

// budgetCallers are every role IAM issues the admin audience to, the tenant
// admin both in and outside the tenant asked for, and no caller.
var budgetCallers = []budgetCaller{
	{name: "platform.admin", roles: []string{apiutil.RolePlatformAdmin}},
	{name: "tenant.admin own tenant", roles: []string{apiutil.RoleTenantAdmin}, ownTenant: true},
	{name: "tenant.admin other tenant", roles: []string{apiutil.RoleTenantAdmin}},
	{name: "bucket.admin own tenant", roles: []string{apiutil.RoleBucketAdmin}, ownTenant: true},
	{name: "iam.admin own tenant", roles: []string{apiutil.RoleIAMAdmin}, ownTenant: true},
	{name: "tenant-provisioner", roles: []string{apiutil.RoleTenantProvisioner}},
	{name: "capability-issuer", roles: []string{apiutil.RoleCapabilityIssuer}},
	{name: "no principal", none: true},
}

// budgetRPC calls one TenantBudgetService RPC on tenant.
type budgetRPC func(ctx context.Context, s *TenantBudgetServer, tenant uuid.UUID) error

var budgetRPCs = map[string]budgetRPC{
	opBudgetGet: func(ctx context.Context, s *TenantBudgetServer, tenant uuid.UUID) error {
		_, err := s.Get(ctx, &pb.TenantBudgetServiceGetRequest{TenantId: tenant.String()})
		return err
	},
	opBudgetListReservations: func(ctx context.Context, s *TenantBudgetServer, tenant uuid.UUID) error {
		_, err := s.ListReservations(ctx, &pb.TenantBudgetServiceListReservationsRequest{TenantId: tenant.String()})
		return err
	},
	opBudgetSet: func(ctx context.Context, s *TenantBudgetServer, tenant uuid.UUID) error {
		_, err := s.Set(ctx, &pb.TenantBudgetServiceSetRequest{
			TenantId:        tenant.String(),
			BudgetCeiling:   limitOf(&money.Money{CurrencyCode: fakePlatformUnit, Units: 100}),
			ResourceVersion: "1",
		})
		return err
	},
	opBudgetSummarize: func(ctx context.Context, s *TenantBudgetServer, _ uuid.UUID) error {
		_, err := s.Summarize(ctx, &pb.TenantBudgetServiceSummarizeRequest{})
		return err
	},
}

// budgetAccess is what each caller is answered by each RPC: reading a tenant's
// budget is its own tenant admin's or the platform admin's; setting one and
// summarizing every tenant's are the platform admin's alone.
var budgetAccess = map[string]map[string]connect.Code{
	opBudgetGet: {
		"platform.admin":            codeOK,
		"tenant.admin own tenant":   codeOK,
		"tenant.admin other tenant": connect.CodePermissionDenied,
		"bucket.admin own tenant":   connect.CodePermissionDenied,
		"iam.admin own tenant":      connect.CodePermissionDenied,
		"tenant-provisioner":        connect.CodePermissionDenied,
		"capability-issuer":         connect.CodePermissionDenied,
		"no principal":              connect.CodeUnauthenticated,
	},
	opBudgetListReservations: {
		"platform.admin":            codeOK,
		"tenant.admin own tenant":   codeOK,
		"tenant.admin other tenant": connect.CodePermissionDenied,
		"bucket.admin own tenant":   connect.CodePermissionDenied,
		"iam.admin own tenant":      connect.CodePermissionDenied,
		"tenant-provisioner":        connect.CodePermissionDenied,
		"capability-issuer":         connect.CodePermissionDenied,
		"no principal":              connect.CodeUnauthenticated,
	},
	opBudgetSet: {
		"platform.admin":            codeOK,
		"tenant.admin own tenant":   connect.CodePermissionDenied,
		"tenant.admin other tenant": connect.CodePermissionDenied,
		"bucket.admin own tenant":   connect.CodePermissionDenied,
		"iam.admin own tenant":      connect.CodePermissionDenied,
		"tenant-provisioner":        connect.CodePermissionDenied,
		"capability-issuer":         connect.CodePermissionDenied,
		"no principal":              connect.CodeUnauthenticated,
	},
	opBudgetSummarize: {
		"platform.admin":            codeOK,
		"tenant.admin own tenant":   connect.CodePermissionDenied,
		"tenant.admin other tenant": connect.CodePermissionDenied,
		"bucket.admin own tenant":   connect.CodePermissionDenied,
		"iam.admin own tenant":      connect.CodePermissionDenied,
		"tenant-provisioner":        connect.CodePermissionDenied,
		"capability-issuer":         connect.CodePermissionDenied,
		"no principal":              connect.CodeUnauthenticated,
	},
}

// budgetTenantPolicies are the tenant policies the table runs under: the one
// a tenant is created with, and none, as the bootstrap tenant has. A tenant
// created before the budget actions existed holds one of these, so the grants
// must come from the built-in policy.
var budgetTenantPolicies = map[string]string{
	"default tenant policy": policies.DefaultTenantPolicy,
	"empty tenant policy":   "",
}

// callerContext is ctx carrying c asking for tenant's budget.
func callerContext(ctx context.Context, c budgetCaller, tenant uuid.UUID) context.Context {
	if c.none {
		return ctx
	}
	own := uuid.New()
	if c.ownTenant {
		own = tenant
	}
	return auth.WithPrincipal(ctx, &auth.Principal{TenantID: own, Subject: "caller@" + c.name, Roles: c.roles})
}

// budgetFixture is a store holding tenant's budget at version 1.
func budgetFixture(tenant uuid.UUID) *fakeUsageStore {
	return &fakeUsageStore{budgets: map[uuid.UUID]meter.TenantBudget{
		tenant: {TenantID: tenant, ResourceVersion: 1},
	}}
}

func TestTenantBudgetServer_AuthorizesEveryRPCByRole(t *testing.T) {
	for policyName, policyText := range budgetTenantPolicies {
		for op, call := range budgetRPCs {
			for _, c := range budgetCallers {
				want, ok := budgetAccess[op][c.name]
				if !ok {
					t.Fatalf("budgetAccess has no answer for %s on %s", c.name, op)
				}
				t.Run(policyName+"/"+op+"/"+c.name, func(t *testing.T) {
					tenant := uuid.New()
					store := budgetFixture(tenant)
					srv := NewTenantBudgetServer(store, &fakeNames{}, cedartest.Engine(policyText))
					err := call(callerContext(context.Background(), c, tenant), srv, tenant)
					if got := connect.CodeOf(err); (err == nil) != (want == codeOK) || (err != nil && got != want) {
						t.Fatalf("err = %v, want %s", err, codeName(want))
					}
					if err != nil && (store.lastSet != (meter.SetTenantBudgetRequest{}) || store.actingOn != uuid.Nil()) {
						t.Errorf("a refused call reached the store")
					}
				})
			}
		}
	}
}

// codeName is want as a failure message says it.
func codeName(want connect.Code) string {
	if want == codeOK {
		return "success"
	}
	return want.String()
}

// The built-in read grant stops at what a tenant's own policy forbids: the
// decision is Cedar's, not the role check's alone.
func TestTenantBudgetServer_TenantPolicyCanForbidTheRead(t *testing.T) {
	const forbidRead = `forbid (principal, action == Action::"ReadTenantBudget", resource);`
	tenant := uuid.New()
	srv := NewTenantBudgetServer(budgetFixture(tenant), &fakeNames{}, cedartest.Engine(forbidRead))
	ctx := callerContext(context.Background(), budgetCaller{name: "tenant.admin", roles: []string{apiutil.RoleTenantAdmin}, ownTenant: true}, tenant)
	if _, err := srv.Get(ctx, &pb.TenantBudgetServiceGetRequest{TenantId: tenant.String()}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
}

// A caller the gate refuses is answered before the subsystem's state: a
// disabled subsystem is Unavailable only to a caller allowed the call.
func TestTenantBudgetServer_RefusesBeforeSayingTheSubsystemIsOff(t *testing.T) {
	srv := NewTenantBudgetServer(nil, nil, budgetPolicy())
	tenant := uuid.New()
	tenantAdmin := callerContext(context.Background(), budgetCaller{name: "tenant.admin", roles: []string{apiutil.RoleTenantAdmin}}, tenant)
	if err := budgetRPCs[opBudgetGet](tenantAdmin, srv, tenant); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("other tenant's admin: err = %v, want PermissionDenied", err)
	}
	if err := budgetRPCs[opBudgetGet](asPlatformAdmin(context.Background()), srv, tenant); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("platform admin: err = %v, want Unavailable", err)
	}
}

// A refusal is logged at Warn, naming the RPC and the tenant; a store
// failure at Error.
func TestTenantBudgetServer_LogsRefusalsAndFaults(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	tenant := uuid.New()

	srv := NewTenantBudgetServer(budgetFixture(tenant), &fakeNames{}, budgetPolicy())
	other := callerContext(ctx, budgetCaller{name: "tenant.admin", roles: []string{apiutil.RoleTenantAdmin}}, tenant)
	if err := budgetRPCs[opBudgetSet](other, srv, tenant); err == nil {
		t.Fatal("Set by another tenant's admin succeeded")
	}
	refused := logs.FilterLevelExact(zapcore.WarnLevel).FilterField(zap.String("op", opBudgetSet)).
		FilterField(zap.String("tenant_id", tenant.String())).All()
	if len(refused) != 1 {
		t.Errorf("refusal logged %d times at Warn, want once: %v", len(refused), logs.All())
	}

	failing := NewTenantBudgetServer(&fakeUsageStore{getErr: errStoreDown}, &fakeNames{}, budgetPolicy())
	if err := budgetRPCs[opBudgetGet](asPlatformAdmin(ctx), failing, tenant); err == nil {
		t.Fatal("Get over a failing store succeeded")
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).FilterField(zap.String("op", opBudgetGet)).Len(); n != 1 {
		t.Errorf("store failure logged %d times at Error, want once: %v", n, logs.All())
	}
}

// The subsystem switched off is the deployment's choice, logged at Warn and
// not as a failure; a tenant with no budget yet is a state, logged at Debug.
func TestTenantBudgetServer_LogsDisabledAndMissingBelowError(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := asPlatformAdmin(logger.WithContext(context.Background(), zap.New(core)))
	tenant := uuid.New()

	if err := budgetRPCs[opBudgetGet](ctx, NewTenantBudgetServer(nil, nil, budgetPolicy()), tenant); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("disabled: err = %v, want Unavailable", err)
	}
	if err := budgetRPCs[opBudgetGet](ctx, NewTenantBudgetServer(&fakeUsageStore{}, &fakeNames{}, budgetPolicy()), tenant); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("no budget: err = %v, want NotFound", err)
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Errorf("%d lines at Error, want none: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zapcore.WarnLevel).Len(); n != 1 {
		t.Errorf("%d lines at Warn, want the disabled one: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zapcore.DebugLevel).Len(); n != 1 {
		t.Errorf("%d lines at Debug, want the missing budget: %v", n, logs.All())
	}
}

// The handler's own checks hold where a tenant's policy grants more: such a
// policy cannot let a tenant admin set its own budget, read another
// tenant's, or summarize every tenant's.
func TestTenantBudgetServer_HandlerChecksOutlastAPermissivePolicy(t *testing.T) {
	const permitEveryone = `permit (principal, action in [Action::"ReadTenantBudget", Action::"ManageTenantBudget"], resource);`
	tenantAdmin := budgetCaller{name: "tenant.admin", roles: []string{apiutil.RoleTenantAdmin}}
	for _, tc := range []struct {
		name string
		op   string
		own  bool
	}{
		{"sets its own budget", opBudgetSet, true},
		{"reads another tenant's budget", opBudgetGet, false},
		{"lists another tenant's holds", opBudgetListReservations, false},
		{"summarizes every tenant's", opBudgetSummarize, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			store := budgetFixture(tenant)
			srv := NewTenantBudgetServer(store, &fakeNames{}, cedartest.Engine(permitEveryone))
			caller := tenantAdmin
			caller.ownTenant = tc.own
			err := budgetRPCs[tc.op](callerContext(context.Background(), caller, tenant), srv, tenant)
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("err = %v, want PermissionDenied", err)
			}
		})
	}
}

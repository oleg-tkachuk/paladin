package capabilityh

import (
	"context"
	"slices"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/meter"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// leaseUnits is a whole amount a test leases, in the capability's unit.
const leaseUnits = 5

// leaseRequests is how many requests a test leases.
const leaseRequests = 3

// leaseTTL is the lease a test asks for, shorter than meter.DefaultLeaseTTL.
const leaseTTL = time.Minute

// leaseFixture is a handler leasing over limes's in-memory meter, with one
// capability issued to lease for.
type leaseFixture struct {
	h     *Handler
	id    string
	usage *memstore.UsageStore
	store *memstore.Store
}

func newLeaseFixture(t *testing.T) leaseFixture {
	t.Helper()
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(m.Issuer, m.Verifier, m.Store, usage, &allowAuthorizer{}).WithLeases(usage)
	res, err := h.Issue(adminCtx(), &adminv1.CapabilityServiceIssueRequest{
		Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: uuid.New().String(), Subject: "agent:lessee"},
		Audience: []string{"paladin-data"},
		Caveats:  openCaveats(limes.OpGet),
	})
	if err != nil {
		t.Fatal(err)
	}
	return leaseFixture{h: h, id: res.GetCapability().GetId(), usage: usage, store: m.Store}
}

func units(unit string, n int64) *money.Money {
	return &money.Money{CurrencyCode: unit, Units: n}
}

// A lease holds what was asked, reads back as it stands, and settles as it
// ended; settled, it is gone.
func TestLeaseOpensReadsAndSettles(t *testing.T) {
	f := newLeaseFixture(t)
	ctx := apiutil.WithResourceSlot(adminCtx())
	opened, err := f.h.Lease(ctx, &adminv1.CapabilityServiceLeaseRequest{
		Id: f.id, Amount: units(apiutil.NoUnit, leaseUnits), Requests: leaseRequests, Ttl: durationpb.New(leaseTTL),
	})
	if err != nil {
		t.Fatal(err)
	}
	l := opened.GetLease()
	if l.GetCapabilityId() != f.id || l.GetAmount().GetUnits() != leaseUnits || l.GetRequests() != leaseRequests ||
		l.GetSpent().GetUnits() != 0 || l.GetRequestsSpent() != 0 || l.GetId() == "" {
		t.Errorf("lease = %v, want %d units and %d requests of %s, none spent", l, leaseUnits, leaseRequests, f.id)
	}
	// The TTL asked for, not the store's default.
	if end := l.GetExpiresAt().AsTime(); !end.Before(time.Now().Add((leaseTTL + meter.DefaultLeaseTTL) / 2)) {
		t.Errorf("lease ends %s, past the default TTL; want a minute from now", end)
	}
	if got := apiutil.ResourceFromContext(ctx); got == "" {
		t.Error("the lease's audit row names no capability")
	}
	// One request and one unit spent under the lease.
	subject, err := limes.StoredMeterSubject(context.Background(), f.store, uuid.MustParse(f.id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.usage.Bump(context.Background(), meter.BumpRequest{Subject: subject}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.usage.Charge(context.Background(), meter.ChargeRequest{Subject: subject, Amount: limes.NanosPerUnit, Op: limes.OpGet}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := f.h.GetLease(adminCtx(), &adminv1.CapabilityServiceGetLeaseRequest{Id: f.id})
	if err != nil || got.GetLease().GetId() != l.GetId() {
		t.Fatalf("GetLease = %v, %v; want lease %s", got, err, l.GetId())
	}
	if g := got.GetLease(); g.GetSpent().GetUnits() != 1 || g.GetRequestsSpent() != 1 {
		t.Errorf("spent %v and %d requests, want 1 unit and 1 request", g.GetSpent(), g.GetRequestsSpent())
	}
	settled, err := f.h.SettleLease(adminCtx(), &adminv1.CapabilityServiceSettleLeaseRequest{Id: f.id})
	if err != nil || settled.GetLease().GetId() != l.GetId() {
		t.Fatalf("SettleLease = %v, %v; want lease %s", settled, err, l.GetId())
	}
	if _, err := f.h.GetLease(adminCtx(), &adminv1.CapabilityServiceGetLeaseRequest{Id: f.id}); codeOf(err) != connect.CodeNotFound {
		t.Errorf("GetLease after settling: code = %v, want NotFound", codeOf(err))
	}
	if _, err := f.h.SettleLease(adminCtx(), &adminv1.CapabilityServiceSettleLeaseRequest{Id: f.id}); codeOf(err) != connect.CodeNotFound {
		t.Errorf("SettleLease again: code = %v, want NotFound", codeOf(err))
	}
}

// actionAuthorizer allows every call and records the action it was asked.
type actionAuthorizer struct{ actions []cedar.Action }

func (a *actionAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.actions = append(a.actions, action)
	return cedar.DecisionAllow, nil
}

// Opening and settling a lease hands out the chain's budget, gated as
// issuing is; reading one is gated as reading a capability is.
func TestLeaseActions(t *testing.T) {
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	authz := &actionAuthorizer{}
	h := NewHandler(m.Issuer, m.Verifier, m.Store, usage, authz).WithLeases(usage)
	id := uuid.New().String()
	_, _ = h.Lease(adminCtx(), &adminv1.CapabilityServiceLeaseRequest{Id: id, Requests: 1})
	_, _ = h.GetLease(adminCtx(), &adminv1.CapabilityServiceGetLeaseRequest{Id: id})
	_, _ = h.SettleLease(adminCtx(), &adminv1.CapabilityServiceSettleLeaseRequest{Id: id})
	want := []cedar.Action{cedar.ActionIssueCapability, cedar.ActionReadCapability, cedar.ActionIssueCapability}
	if !slices.Equal(authz.actions, want) {
		t.Errorf("actions = %v, want %v", authz.actions, want)
	}
}

// Asked again while open, a lease adds to what it holds.
func TestLeaseTopsUp(t *testing.T) {
	f := newLeaseFixture(t)
	req := &adminv1.CapabilityServiceLeaseRequest{Id: f.id, Amount: units(apiutil.NoUnit, leaseUnits)}
	if _, err := f.h.Lease(adminCtx(), req); err != nil {
		t.Fatal(err)
	}
	res, err := f.h.Lease(adminCtx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.GetLease().GetAmount().GetUnits(); got != 2*leaseUnits {
		t.Errorf("topped-up amount = %d, want %d", got, 2*leaseUnits)
	}
}

func TestLeaseRefusals(t *testing.T) {
	f := newLeaseFixture(t)
	unknown := uuid.New().String()
	cases := map[string]struct {
		h    *Handler
		req  *adminv1.CapabilityServiceLeaseRequest
		want connect.Code
	}{
		"denied by policy":   {mkHandler(t, &fakeStore{}, nil, &denyAuthorizer{}), &adminv1.CapabilityServiceLeaseRequest{Id: f.id, Requests: 1}, connect.CodePermissionDenied},
		"not wired":          {mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}), &adminv1.CapabilityServiceLeaseRequest{Id: f.id, Requests: 1}, connect.CodeUnavailable},
		"a bad id":           {f.h, &adminv1.CapabilityServiceLeaseRequest{Id: "nope", Requests: 1}, connect.CodeInvalidArgument},
		"no such capability": {f.h, &adminv1.CapabilityServiceLeaseRequest{Id: unknown, Requests: 1}, connect.CodeNotFound},
		"another unit":       {f.h, &adminv1.CapabilityServiceLeaseRequest{Id: f.id, Amount: units("EUR", leaseUnits)}, connect.CodeInvalidArgument},
		"a bad amount":       {f.h, &adminv1.CapabilityServiceLeaseRequest{Id: f.id, Amount: &money.Money{CurrencyCode: apiutil.NoUnit, Units: -1}}, connect.CodeInvalidArgument},
		"nothing to lease":   {f.h, &adminv1.CapabilityServiceLeaseRequest{Id: f.id}, connect.CodeInvalidArgument},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.h.Lease(adminCtx(), tc.req); codeOf(err) != tc.want {
				t.Errorf("code = %v, want %v (%v)", codeOf(err), tc.want, err)
			}
		})
	}
}

// GetLease and SettleLease refuse as Lease does before they reach the meter.
func TestLeaseReadAndSettleRefusals(t *testing.T) {
	f := newLeaseFixture(t)
	type call func(h *Handler, id string) error
	get := func(h *Handler, id string) error {
		_, err := h.GetLease(adminCtx(), &adminv1.CapabilityServiceGetLeaseRequest{Id: id})
		return err
	}
	settle := func(h *Handler, id string) error {
		_, err := h.SettleLease(adminCtx(), &adminv1.CapabilityServiceSettleLeaseRequest{Id: id})
		return err
	}
	for name, c := range map[string]call{"GetLease": get, "SettleLease": settle} {
		t.Run(name, func(t *testing.T) {
			for what, tc := range map[string]struct {
				h    *Handler
				id   string
				want connect.Code
			}{
				"denied by policy": {mkHandler(t, &fakeStore{}, nil, &denyAuthorizer{}), f.id, connect.CodePermissionDenied},
				"not wired":        {mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}), f.id, connect.CodeUnavailable},
				"a bad id":         {f.h, "nope", connect.CodeInvalidArgument},
				"no lease":         {f.h, f.id, connect.CodeNotFound},
				"no capability":    {f.h, uuid.New().String(), connect.CodeNotFound},
			} {
				if err := c(tc.h, tc.id); codeOf(err) != tc.want {
					t.Errorf("%s: code = %v, want %v (%v)", what, codeOf(err), tc.want, err)
				}
			}
		})
	}
}

package capabilityh

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// leaseTable is the shared table of who may lease for a capability.
const leaseTable = "../../../../../../sdk/testdata/capability_leases.json"

// The roles the lease table seeds, and the one it never issues.
const (
	roleHolder     = "holder"
	roleChild      = "child"
	roleGrandchild = "grandchild"
	roleRevoked    = "revoked"
	roleStranger   = "stranger"
	roleCopy       = "copy"
	roleBiscuit    = "biscuit"
	roleUnknown    = "unknown"
)

// The lease table's calls.
const (
	callLease       = "lease"
	callGetLease    = "get_lease"
	callSettleLease = "settle_lease"
)

type leaseStep struct {
	Call      string `json:"call"`
	Of        string `json:"of"`
	Requests  int64  `json:"requests"`
	Code      string `json:"code"`
	LimesCode string `json:"limes_code"`
	Held      *int64 `json:"held"`
}

type leaseCase struct {
	Name  string      `json:"name"`
	As    string      `json:"as"`
	Steps []leaseStep `json:"steps"`
}

func readLeaseTable(t *testing.T) []leaseCase {
	t.Helper()
	raw, err := os.ReadFile(leaseTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []leaseCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("the lease table has no cases")
	}
	return table.Cases
}

// leaseAnswer is what a step got: its refusal, or the requests its lease
// holds.
type leaseAnswer struct {
	Code, LimesCode string
	Held            int64
}

func (s leaseStep) want() leaseAnswer {
	if s.Code != "" {
		return leaseAnswer{Code: s.Code, LimesCode: s.LimesCode}
	}
	var held int64
	if s.Held != nil {
		held = *s.Held
	}
	return leaseAnswer{Held: held}
}

// leaseLimesCode is the limes code err names in its ErrorInfo.
func leaseLimesCode(err error) string {
	if code := paladin.LimesCode(err); code != "" {
		return code
	}
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return ""
	}
	for _, d := range ce.Details() {
		if v, derr := connectproto.UnmarshalErrorDetail(d); derr == nil {
			if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == paladin.ErrorDomain {
				return info.GetMetadata()[paladin.ErrorMetadataLimesCode]
			}
		}
	}
	return ""
}

func answerOfLease(l *adminv1.CapabilityLease, err error) leaseAnswer {
	if err != nil {
		return leaseAnswer{Code: connect.CodeOf(err).String(), LimesCode: leaseLimesCode(err)}
	}
	return leaseAnswer{Held: l.GetRequests()}
}

// leaseCalls runs a step's call as a caller, on capability id.
type leaseCalls interface {
	Lease(ctx context.Context, req *adminv1.CapabilityServiceLeaseRequest) (*adminv1.CapabilityServiceLeaseResponse, error)
	GetLease(ctx context.Context, req *adminv1.CapabilityServiceGetLeaseRequest) (*adminv1.CapabilityServiceGetLeaseResponse, error)
	SettleLease(ctx context.Context, req *adminv1.CapabilityServiceSettleLeaseRequest) (*adminv1.CapabilityServiceSettleLeaseResponse, error)
}

func runLeaseStep(ctx context.Context, c leaseCalls, s leaseStep, id string) leaseAnswer {
	switch s.Call {
	case callLease:
		res, err := c.Lease(ctx, &adminv1.CapabilityServiceLeaseRequest{Id: id, Requests: s.Requests})
		return answerOfLease(res.GetLease(), err)
	case callGetLease:
		res, err := c.GetLease(ctx, &adminv1.CapabilityServiceGetLeaseRequest{Id: id})
		return answerOfLease(res.GetLease(), err)
	case callSettleLease:
		res, err := c.SettleLease(ctx, &adminv1.CapabilityServiceSettleLeaseRequest{Id: id})
		return answerOfLease(res.GetLease(), err)
	}
	panic("lease table: unknown call " + s.Call)
}

// serverLeaseChain seeds the table's roles on limes's in-memory stores.
func serverLeaseChain(t *testing.T) (*Handler, map[string]limes.VerifiedCapability) {
	t.Helper()
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(m.Issuer, m.Verifier, m.Store, usage, &allowAuthorizer{}).WithLeases(usage)
	tenant := uuid.New()
	root := func(ops ...limes.Op) limes.VerifiedCapability {
		v, _ := m.Mint(t, limes.Capability{
			Subject:  limes.Principal{TenantID: tenant},
			Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
			Caveats:  limes.Caveats{Ops: ops},
		})
		return v
	}
	holder := root(limes.OpGet, limes.OpShare)
	child, _ := m.Delegate(t, holder, narrowed(holder, limes.OpGet, limes.OpShare))
	grandchild, _ := m.Delegate(t, child, narrowed(child, limes.OpGet))
	revoked, _ := m.Delegate(t, holder, narrowed(holder, limes.OpGet))
	if _, err := m.Store.Revoke(context.Background(), limes.RevokeRequest{CapabilityID: revoked.Capability().ID}); err != nil {
		t.Fatal(err)
	}
	copied, _ := m.Attenuated(t, holder, limes.Attenuation{Ops: []limes.Op{limes.OpGet, limes.OpShare}})
	sealed, err := m.Issuer.Biscuit(context.Background(), limes.BiscuitRequest{Capability: holder})
	if err != nil {
		t.Fatal(err)
	}
	return h, map[string]limes.VerifiedCapability{
		roleHolder: holder, roleChild: child, roleGrandchild: grandchild,
		roleRevoked: revoked, roleStranger: root(limes.OpGet), roleCopy: copied,
		roleBiscuit: m.Verify(t, sealed.Token, string(paladin.PlaneAdmin)),
	}
}

// fakeLeaseChain seeds the table's roles on paladintest, through its own
// CapabilityService, and returns each role's id and token.
func fakeLeaseChain(t *testing.T, srv *paladintest.Server) (ids, tokens map[string]string) {
	t.Helper()
	ctx := context.Background()
	admin := srv.Connect().Admin.Capability
	root := func(ops ...limes.Op) *adminv1.CapabilityServiceIssueResponse {
		res, err := admin.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
			Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:lease"},
			Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
			Caveats:  openCaveats(ops...),
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	delegate := func(parent *adminv1.CapabilityServiceIssueResponse, ops ...limes.Op) *adminv1.CapabilityServiceIssueResponse {
		res, err := srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability.Delegate(ctx, &adminv1.CapabilityServiceDelegateRequest{
			ParentId: parent.GetCapability().GetId(), TtlSeconds: delegateTTLSeconds, Caveats: openCaveats(ops...),
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	holder := root(limes.OpGet, limes.OpShare)
	child := delegate(holder, limes.OpGet, limes.OpShare)
	grandchild := delegate(child, limes.OpGet)
	revoked := delegate(holder, limes.OpGet)
	if _, err := admin.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: revoked.GetCapability().GetId()}); err != nil {
		t.Fatal(err)
	}
	stranger := root(limes.OpGet)
	copied, err := paladin.Attenuate(holder.GetBiscuit(), paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet, paladin.OpShare}})
	if err != nil {
		t.Fatal(err)
	}
	ids, tokens = map[string]string{}, map[string]string{roleCopy: copied, roleBiscuit: holder.GetBiscuit()}
	for role, r := range map[string]*adminv1.CapabilityServiceIssueResponse{
		roleHolder: holder, roleChild: child, roleGrandchild: grandchild, roleRevoked: revoked, roleStranger: stranger,
	} {
		ids[role], tokens[role] = r.GetCapability().GetId(), r.GetToken()
	}
	return ids, tokens
}

// Who may lease for a capability, and what each step answers, runs through
// this handler and through paladintest alike; the Python SDK's tests hold
// its fake to the same table.
func TestTheSDKFakeLeasesAsTheServerDoes(t *testing.T) {
	for _, tc := range readLeaseTable(t) {
		t.Run(tc.Name, func(t *testing.T) {
			h, roles := serverLeaseChain(t)
			ctx := adminCtx()
			if tc.As != "" {
				ctx = auth.WithCapability(context.Background(), roles[tc.As])
			}
			for i, s := range tc.Steps {
				id := uuid.New().String()
				if s.Of != roleUnknown {
					id = roles[s.Of].Capability().ID.String()
				}
				if got := runLeaseStep(ctx, h, s, id); got != s.want() {
					t.Errorf("server step %d (%s of %s) answers %+v, the table %+v", i, s.Call, s.Of, got, s.want())
				}
			}

			srv := paladintest.New(t)
			ids, tokens := fakeLeaseChain(t, srv)
			client := srv.Connect().Admin.Capability
			if tc.As != "" {
				client = srv.Connect(paladin.WithCapability(tokens[tc.As])).Admin.Capability
			}
			for i, s := range tc.Steps {
				id := uuid.New().String()
				if s.Of != roleUnknown {
					id = ids[s.Of]
				}
				if got := runLeaseStep(context.Background(), client, s, id); got != s.want() {
					t.Errorf("fake step %d (%s of %s) answers %+v, the table %+v", i, s.Call, s.Of, got, s.want())
				}
			}
		})
	}
}

package capabilityh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// The SDK's fake answers Issue as this handler does: the same Biscuit status,
// whether a Biscuit came back, and the same code for a request either
// refuses. A consumer's unit tests run against the fake, so a rule it does
// not share is one those tests pass and the cluster fails — a hand-written
// fake that sealed a Biscuit regardless did exactly that.
func TestTheSDKFakeIssuesAsTheServerDoes(t *testing.T) {
	// Within the TTL below, and past it.
	later := timestamppb.New(time.Now().Add(time.Minute))
	pastExpiry := timestamppb.New(time.Now().Add(time.Hour))
	cases := map[string]func(*adminv1.CapabilityServiceIssueRequest){
		"no share":    func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats = openCaveats(limes.OpGet) },
		"share":       func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats = openCaveats(limes.OpGet, limes.OpShare) },
		"key-bound":   func(r *adminv1.CapabilityServiceIssueRequest) { r.ConfirmationJkt = testJKT },
		"not yet":     func(r *adminv1.CapabilityServiceIssueRequest) { r.NotBefore = later },
		"past expiry": func(r *adminv1.CapabilityServiceIssueRequest) { r.NotBefore = pastExpiry },
		"bare op":     func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats = openCaveats(limes.OpGet, "presign") },
		"misspelt op": func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats = openCaveats("Get") },
		"no ops":      func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats = openCaveats() },
		"a data-plane op": func(r *adminv1.CapabilityServiceIssueRequest) {
			r.Caveats = openCaveats(limes.OpGet, limes.Op("object:presign"))
		},
		"no request ceiling": func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats.RequestCeiling = nil },
		"no budget ceiling":  func(r *adminv1.CapabilityServiceIssueRequest) { r.Caveats.BudgetCeiling = nil },
		"key-bound, required": func(r *adminv1.CapabilityServiceIssueRequest) {
			r.ConfirmationJkt, r.RequireBiscuit = testJKT, true
		},
		"not yet, required": func(r *adminv1.CapabilityServiceIssueRequest) { r.NotBefore, r.RequireBiscuit = later, true },
		// require_biscuit is refused before what limes refuses as it mints.
		"a bare op, key-bound, required": func(r *adminv1.CapabilityServiceIssueRequest) {
			r.Caveats, r.ConfirmationJkt, r.RequireBiscuit = openCaveats("presign"), testJKT, true
		},
		"past expiry, required": func(r *adminv1.CapabilityServiceIssueRequest) { r.NotBefore, r.RequireBiscuit = pastExpiry, true },
		"no share, required": func(r *adminv1.CapabilityServiceIssueRequest) {
			r.Caveats, r.RequireBiscuit = openCaveats(limes.OpGet), true
		},
	}
	fake := paladintest.New(t).Connect().Admin.Capability
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := &adminv1.CapabilityServiceIssueRequest{
				Subject: &adminv1.CapabilityPrincipal{
					Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: uuid.NewString(), Subject: "agent:worker",
				},
				Audience:   []string{"paladin-data"},
				TtlSeconds: 300,
				Caveats:    openCaveats(limes.OpGet, limes.OpList),
			}
			mutate(req)
			server, serverErr := h.Issue(adminCtx(), proto.CloneOf(req))
			faked, fakeErr := fake.Issue(context.Background(), proto.CloneOf(req))
			if connect.CodeOf(serverErr) != connect.CodeOf(fakeErr) {
				t.Fatalf("the server answers %v, the fake %v", serverErr, fakeErr)
			}
			if serverErr != nil {
				return
			}
			if server.GetBiscuitStatus() != faked.GetBiscuitStatus() || (server.GetBiscuit() == "") != (faked.GetBiscuit() == "") {
				t.Errorf("the server answers %v with a Biscuit %t, the fake %v with one %t",
					server.GetBiscuitStatus(), server.GetBiscuit() != "", faked.GetBiscuitStatus(), faked.GetBiscuit() != "")
			}
		})
	}
}

// The SDK's fake answers Delegate as this handler does, for the rules a
// consumer's tests meet: a parent without share, a child narrowed off share,
// one it would bind to a key, and an op the parent lacks.
func TestTheSDKFakeDelegatesAsTheServerDoes(t *testing.T) {
	type delegation struct {
		parentOps []limes.Op
		mutate    func(*adminv1.CapabilityServiceDelegateRequest)
	}
	keep := func(*adminv1.CapabilityServiceDelegateRequest) {}
	narrowTo := func(ops ...limes.Op) func(*adminv1.CapabilityServiceDelegateRequest) {
		return func(r *adminv1.CapabilityServiceDelegateRequest) { r.Caveats = openCaveats(ops...) }
	}
	cases := map[string]delegation{
		"the parent's caveats":   {[]limes.Op{limes.OpGet, limes.OpShare}, keep},
		"a parent without share": {[]limes.Op{limes.OpGet}, keep},
		"narrowed off share":     {[]limes.Op{limes.OpGet, limes.OpShare}, narrowTo(limes.OpGet)},
		"an op the parent lacks": {[]limes.Op{limes.OpGet, limes.OpShare}, narrowTo(limes.OpPut)},
		"narrowed to a bare op":  {[]limes.Op{limes.OpGet, limes.OpShare}, narrowTo("presign")},
		"wider and key-bound, required": {[]limes.Op{limes.OpGet, limes.OpShare}, func(r *adminv1.CapabilityServiceDelegateRequest) {
			r.Caveats, r.ConfirmationJkt, r.RequireBiscuit = openCaveats(limes.OpPut), testJKT, true
		}},
		"a key-bound child, required": {[]limes.Op{limes.OpGet, limes.OpShare}, func(r *adminv1.CapabilityServiceDelegateRequest) {
			r.ConfirmationJkt, r.RequireBiscuit = testJKT, true
		}},
	}
	srv := paladintest.New(t)
	m := captest.NewMinter(t)
	h := delegateHandler(m, &denyAuthorizer{})
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			parent := mintParent(t, m, tc.parentOps...)
			serverReq := delegateReq(parent)
			tc.mutate(serverReq)
			server, serverErr := h.Delegate(auth.WithCapability(context.Background(), parent), serverReq)

			issueReq := &adminv1.CapabilityServiceIssueRequest{
				Subject: &adminv1.CapabilityPrincipal{
					Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:parent",
				},
				Audience: []string{"paladin-data"},
				Caveats:  openCaveats(tc.parentOps...),
			}
			fakeParent, err := srv.Connect().Admin.Capability.Issue(context.Background(), issueReq)
			if err != nil {
				t.Fatal(err)
			}
			fakeReq := &adminv1.CapabilityServiceDelegateRequest{ParentId: fakeParent.GetCapability().GetId(), TtlSeconds: delegateTTLSeconds}
			tc.mutate(fakeReq)
			faked, fakeErr := srv.Connect(paladin.WithCapability(fakeParent.GetToken())).Admin.Capability.Delegate(context.Background(), fakeReq)

			if connect.CodeOf(serverErr) != connect.CodeOf(fakeErr) {
				t.Fatalf("the server answers %v, the fake %v", serverErr, fakeErr)
			}
			if serverErr == nil && server.GetBiscuitStatus() != faked.GetBiscuitStatus() {
				t.Errorf("the server answers %v, the fake %v", server.GetBiscuitStatus(), faked.GetBiscuitStatus())
			}
		})
	}
}

package capabilityh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
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

// listScenario is one principal the fidelity test issues a capability to,
// and what becomes of it.
type listScenario struct {
	kind    adminv1.PrincipalKind
	subject string
	ttl     int64
	revoke  bool
}

// expiringTTLSeconds is the lifetime of the capability the listing test
// waits out, so it lists one expired.
const expiringTTLSeconds = 1

// The SDK's fake lists a tenant's capabilities as this handler does over
// limes's own record listing: the same capabilities in the same order, each
// under the same disposition, for every filter, order and page, and the same
// refusals of a page token it did not hand out.
func TestTheSDKFakeListsAsTheServerDoes(t *testing.T) {
	agent, user := adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, adminv1.PrincipalKind_PRINCIPAL_KIND_USER
	scenario := []listScenario{
		{kind: agent, subject: "agent:one", ttl: 300},
		{kind: agent, subject: "agent:two", ttl: 300, revoke: true},
		{kind: user, subject: "user:one", ttl: 300},
		{kind: user, subject: "user:gone", ttl: expiringTTLSeconds},
		{kind: agent, subject: "agent:one", ttl: 300},
	}
	srv := paladintest.New(t)
	fake := srv.Connect().Admin.Capability
	m := captest.NewMinter(t)
	h := NewHandler(m.Issuer, m.Verifier, m.Store, nil, &allowAuthorizer{}).WithRecords(memstore.NewCensus(m.Store))
	tenant := srv.Tenant()
	var expiry time.Time
	for _, p := range scenario {
		req := &adminv1.CapabilityServiceIssueRequest{
			Subject:    &adminv1.CapabilityPrincipal{Kind: p.kind, TenantId: tenant, Subject: p.subject},
			Audience:   []string{"paladin-data"},
			TtlSeconds: p.ttl,
			Caveats:    openCaveats(limes.OpGet),
		}
		server, err := h.Issue(adminCtx(), proto.CloneOf(req))
		if err != nil {
			t.Fatalf("server Issue %s: %v", p.subject, err)
		}
		faked, err := fake.Issue(context.Background(), proto.CloneOf(req))
		if err != nil {
			t.Fatalf("fake Issue %s: %v", p.subject, err)
		}
		for _, c := range []*adminv1.Capability{server.GetCapability(), faked.GetCapability()} {
			if e := c.GetExpiresAt().AsTime(); p.ttl == expiringTTLSeconds && e.After(expiry) {
				expiry = e
			}
		}
		if p.revoke {
			if _, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: server.GetCapability().GetId()}); err != nil {
				t.Fatalf("server Revoke: %v", err)
			}
			if _, err := fake.Revoke(context.Background(), &adminv1.CapabilityServiceRevokeRequest{Id: faked.GetCapability().GetId()}); err != nil {
				t.Fatalf("fake Revoke: %v", err)
			}
		}
	}
	time.Sleep(time.Until(expiry) + time.Millisecond)

	all := []adminv1.CapabilityDisposition{
		adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE,
		adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED,
		adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED,
	}
	cases := map[string]*adminv1.CapabilityServiceListRequest{
		"the tenant alone":                {},
		"every disposition, newest first": {Dispositions: all, SortOrder: commonv1.SortOrder_SORT_ORDER_DESC},
		"a kind":                          {PrincipalKind: agent, Dispositions: all},
		"a subject":                       {Subject: "agent:one", Dispositions: all},
		"revoked":                         {Dispositions: all[2:]},
		"the include flags":               {IncludeExpired: true, IncludeRevoked: true},
		"a page at a time":                {Dispositions: all, PageSize: 2},
		"a page at a time, newest first":  {Dispositions: all, PageSize: 2, SortOrder: commonv1.SortOrder_SORT_ORDER_DESC},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			req.TenantId = tenant
			server := listAll(t, func(r *adminv1.CapabilityServiceListRequest) (*adminv1.CapabilityServiceListResponse, error) {
				return h.List(adminCtx(), r)
			}, req)
			faked := listAll(t, func(r *adminv1.CapabilityServiceListRequest) (*adminv1.CapabilityServiceListResponse, error) {
				return fake.List(context.Background(), r)
			}, req)
			if diff := cmp.Diff(server, faked); diff != "" {
				t.Errorf("listings differ (-server +fake):\n%s", diff)
			}
		})
	}

	// A page token for the other order, and one neither handed out.
	for name, order := range map[string]commonv1.SortOrder{"ascending": commonv1.SortOrder_SORT_ORDER_ASC, "descending": commonv1.SortOrder_SORT_ORDER_DESC} {
		t.Run("a token of the other order, "+name, func(t *testing.T) {
			first := &adminv1.CapabilityServiceListRequest{TenantId: tenant, Dispositions: all, PageSize: 1, SortOrder: order}
			serverPage, err := h.List(adminCtx(), proto.CloneOf(first))
			if err != nil {
				t.Fatal(err)
			}
			fakePage, err := fake.List(context.Background(), proto.CloneOf(first))
			if err != nil {
				t.Fatal(err)
			}
			other := commonv1.SortOrder_SORT_ORDER_DESC
			if order == other {
				other = commonv1.SortOrder_SORT_ORDER_ASC
			}
			_, serverErr := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: tenant, SortOrder: other, PageToken: serverPage.GetNextPageToken()})
			_, fakeErr := fake.List(context.Background(), &adminv1.CapabilityServiceListRequest{TenantId: tenant, SortOrder: other, PageToken: fakePage.GetNextPageToken()})
			if connect.CodeOf(serverErr) != connect.CodeInvalidArgument || connect.CodeOf(fakeErr) != connect.CodeInvalidArgument {
				t.Errorf("the server answers %v, the fake %v, want InvalidArgument", serverErr, fakeErr)
			}
		})
	}
	// A page token handed back with other filters, which limes refuses as it
	// refuses one of the other order.
	for name, mutate := range map[string]func(*adminv1.CapabilityServiceListRequest){
		"another kind":       func(r *adminv1.CapabilityServiceListRequest) { r.PrincipalKind = user },
		"another subject":    func(r *adminv1.CapabilityServiceListRequest) { r.Subject = "agent:one" },
		"other dispositions": func(r *adminv1.CapabilityServiceListRequest) { r.Dispositions = all[:1] },
	} {
		t.Run("a token of "+name, func(t *testing.T) {
			first := &adminv1.CapabilityServiceListRequest{TenantId: tenant, Dispositions: all, PageSize: 1}
			serverPage, err := h.List(adminCtx(), proto.CloneOf(first))
			if err != nil {
				t.Fatal(err)
			}
			fakePage, err := fake.List(context.Background(), proto.CloneOf(first))
			if err != nil {
				t.Fatal(err)
			}
			again := func(token string) *adminv1.CapabilityServiceListRequest {
				r := proto.CloneOf(first)
				r.PageToken = token
				mutate(r)
				return r
			}
			_, serverErr := h.List(adminCtx(), again(serverPage.GetNextPageToken()))
			_, fakeErr := fake.List(context.Background(), again(fakePage.GetNextPageToken()))
			if connect.CodeOf(serverErr) != connect.CodeInvalidArgument || connect.CodeOf(fakeErr) != connect.CodeInvalidArgument {
				t.Errorf("the server answers %v, the fake %v, want InvalidArgument", serverErr, fakeErr)
			}
		})
	}
	_, serverErr := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: tenant, PageToken: "garbage"})
	_, fakeErr := fake.List(context.Background(), &adminv1.CapabilityServiceListRequest{TenantId: tenant, PageToken: "garbage"})
	if connect.CodeOf(serverErr) != connect.CodeInvalidArgument || connect.CodeOf(fakeErr) != connect.CodeInvalidArgument {
		t.Errorf("a token neither handed out: the server answers %v, the fake %v, want InvalidArgument", serverErr, fakeErr)
	}
}

// listedCapability is what the fidelity test compares of a listed
// capability: the ids differ between the two, the rest must not.
type listedCapability struct {
	Kind        adminv1.PrincipalKind
	Subject     string
	Disposition adminv1.CapabilityDisposition
}

// listAll is every page of req, as list answers it.
func listAll(t *testing.T, list func(*adminv1.CapabilityServiceListRequest) (*adminv1.CapabilityServiceListResponse, error), req *adminv1.CapabilityServiceListRequest) []listedCapability {
	t.Helper()
	out := []listedCapability{}
	r := proto.CloneOf(req)
	for {
		resp, err := list(r)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, c := range resp.GetCapabilities() {
			out = append(out, listedCapability{c.GetSubject().GetKind(), c.GetSubject().GetSubject(), c.GetDisposition()})
		}
		if resp.GetNextPageToken() == "" {
			return out
		}
		r = proto.CloneOf(req)
		r.PageToken = resp.GetNextPageToken()
	}
}

// The SDK's fake gives Get the disposition this handler gives it: active,
// revoked itself, and revoked only through the parent it was delegated from,
// which holds no revocation of its own.
func TestTheSDKFakeGetsTheDispositionAsTheServerDoes(t *testing.T) {
	ctx := context.Background()
	m := captest.NewMinter(t)
	h := NewHandler(m.Issuer, m.Verifier, m.Store, nil, &allowAuthorizer{}).WithRecords(memstore.NewCensus(m.Store))
	active := mintParent(t, m, limes.OpGet)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	child, err := h.Delegate(auth.WithCapability(ctx, parent), delegateReq(parent))
	if err != nil {
		t.Fatalf("server Delegate: %v", err)
	}
	if first, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: parent.Capability().ID.String()}); err != nil || first.GetAlreadyRevoked() {
		t.Fatalf("server Revoke: %v, already revoked %v", err, first.GetAlreadyRevoked())
	}

	srv := paladintest.New(t)
	fake := srv.Connect().Admin.Capability
	issue := func(ops ...limes.Op) *adminv1.CapabilityServiceIssueResponse {
		resp, err := fake.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
			Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:parent"},
			Audience: []string{"paladin-data"},
			Caveats:  openCaveats(ops...),
		})
		if err != nil {
			t.Fatalf("fake Issue: %v", err)
		}
		return resp
	}
	fakeActive := issue(limes.OpGet)
	fakeParent := issue(limes.OpGet, limes.OpShare)
	fakeChild, err := srv.Connect(paladin.WithCapability(fakeParent.GetToken())).Admin.Capability.Delegate(ctx,
		&adminv1.CapabilityServiceDelegateRequest{ParentId: fakeParent.GetCapability().GetId(), TtlSeconds: delegateTTLSeconds})
	if err != nil {
		t.Fatalf("fake Delegate: %v", err)
	}
	if first, err := fake.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: fakeParent.GetCapability().GetId()}); err != nil || first.GetAlreadyRevoked() {
		t.Fatalf("fake Revoke: %v, already revoked %v", err, first.GetAlreadyRevoked())
	}

	serverParent, fakeParentID := parent.Capability().ID.String(), fakeParent.GetCapability().GetId()
	// Each case: the capability on each side, and on each side the
	// capability whose entry stops it — "" for none.
	for name, ids := range map[string][4]string{
		"active":                     {active.Capability().ID.String(), fakeActive.GetCapability().GetId(), "", ""},
		"revoked":                    {serverParent, fakeParentID, serverParent, fakeParentID},
		"revoked through its parent": {child.GetCapability().GetId(), fakeChild.GetCapability().GetId(), serverParent, fakeParentID},
	} {
		t.Run(name, func(t *testing.T) {
			server, err := h.Get(adminCtx(), &adminv1.CapabilityServiceGetRequest{Id: ids[0]})
			if err != nil {
				t.Fatalf("server Get: %v", err)
			}
			faked, err := fake.Get(ctx, &adminv1.CapabilityServiceGetRequest{Id: ids[1]})
			if err != nil {
				t.Fatalf("fake Get: %v", err)
			}
			if s, f := server.GetCapability().GetDisposition(), faked.GetCapability().GetDisposition(); s != f || s == adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_UNSPECIFIED {
				t.Errorf("the server answers %v, the fake %v", s, f)
			}
			if s, f := server.GetStoppedBy().GetCapabilityId(), faked.GetStoppedBy().GetCapabilityId(); s != ids[2] || f != ids[3] {
				t.Errorf("stopped by %q on the server, %q on the fake; want %q and %q", s, f, ids[2], ids[3])
			}
			// An entry of its own only where it was revoked itself.
			if s, f := server.GetRevocation() != nil, faked.GetRevocation() != nil; s != f {
				t.Errorf("an entry of its own on the server %v, on the fake %v", s, f)
			}
		})
	}

	// A second revoke keeps the first entry and says so, on both.
	server, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: serverParent, Reason: "again"})
	if err != nil {
		t.Fatalf("server Revoke again: %v", err)
	}
	faked, err := fake.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: fakeParentID, Reason: "again"})
	if err != nil {
		t.Fatalf("fake Revoke again: %v", err)
	}
	if !server.GetAlreadyRevoked() || !faked.GetAlreadyRevoked() {
		t.Errorf("a second revoke: already revoked on the server %v, on the fake %v", server.GetAlreadyRevoked(), faked.GetAlreadyRevoked())
	}
	if server.GetRevocation().GetReason() == "again" || faked.GetRevocation().GetReason() == "again" {
		t.Errorf("a second revoke replaced the first entry: server %v, fake %v", server.GetRevocation(), faked.GetRevocation())
	}
}

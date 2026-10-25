package capabilityh

import (
	"context"
	"encoding/json"
	"os"
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

// issuanceTable is how CapabilityService answers Issue and Delegate, which
// the server and both SDKs' fakes are held to
// (sdk/testdata/capability_issuance.json).
const issuanceTable = "../../../../../../sdk/testdata/capability_issuance.json"

// The table's not_before values.
const (
	notBeforeLater      = "later"
	notBeforePastExpiry = "past_expiry"
)

// issuanceTTLSeconds is the lifetime an issue case asks for, which
// notBeforePastExpiry lies beyond and notBeforeLater within.
const issuanceTTLSeconds = 300

// issuanceWant is the answer a case gets: the code it is refused with, or
// its Biscuit status and whether a Biscuit came back.
type issuanceWant struct {
	Code          string `json:"code"`
	BiscuitStatus string `json:"biscuit_status"`
	Biscuit       bool   `json:"biscuit"`
}

// issuanceCase is one request of the table, Issue's or Delegate's.
type issuanceCase struct {
	Name             string    `json:"name"`
	Ops              *[]string `json:"ops"`
	ParentOps        []string  `json:"parent_ops"`
	ChildOps         *[]string `json:"child_ops"`
	KeyBound         bool      `json:"key_bound"`
	NotBefore        string    `json:"not_before"`
	RequireBiscuit   bool      `json:"require_biscuit"`
	NoRequestCeiling bool      `json:"no_request_ceiling"`
	NoBudgetCeiling  bool      `json:"no_budget_ceiling"`
	// What a delegation's parent restricts, and its child asks for.
	ParentPrefixes       []string     `json:"parent_prefixes"`
	ParentIdempotencyKey bool         `json:"parent_idempotency_key"`
	ChildPrefixes        []string     `json:"child_prefixes"`
	ChildTaintedRead     bool         `json:"child_tainted_read"`
	ChildAudience        []string     `json:"child_audience"`
	Want                 issuanceWant `json:"want"`
}

func readIssuanceTable(t *testing.T) (issue, delegate []issuanceCase) {
	t.Helper()
	raw, err := os.ReadFile(issuanceTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Issue    []issuanceCase `json:"issue"`
		Delegate []issuanceCase `json:"delegate"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Issue) == 0 || len(table.Delegate) == 0 {
		t.Fatal("the issuance table has no cases")
	}
	return table.Issue, table.Delegate
}

func opsOf(names []string) []limes.Op {
	ops := make([]limes.Op, 0, len(names))
	for _, n := range names {
		ops = append(ops, limes.Op(n))
	}
	return ops
}

// gotIssuance is what an Issue or a Delegate answered, as the table spells it.
func gotIssuance(resp *adminv1.CapabilityServiceIssueResponse, err error) issuanceWant {
	if err != nil {
		return issuanceWant{Code: connect.CodeOf(err).String()}
	}
	return issuanceWant{BiscuitStatus: resp.GetBiscuitStatus().String(), Biscuit: resp.GetBiscuit() != ""}
}

// A consumer's unit tests run against the SDK's fake, so a rule it does not
// share is one those tests pass and the cluster fails — a hand-written fake
// that sealed a Biscuit regardless did exactly that. Each Issue case of the
// shared table runs through this handler and through paladintest, and both
// must give the table's answer; the Python SDK's tests hold its fake to the
// same table.
func TestTheSDKFakeIssuesAsTheServerDoes(t *testing.T) {
	cases, _ := readIssuanceTable(t)
	fake := paladintest.New(t).Connect().Admin.Capability
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{})
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			req := &adminv1.CapabilityServiceIssueRequest{
				Subject: &adminv1.CapabilityPrincipal{
					Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: uuid.NewString(), Subject: "agent:worker",
				},
				Audience:       paladin.PlaneNames(paladin.PlaneData),
				TtlSeconds:     issuanceTTLSeconds,
				Caveats:        openCaveats(limes.OpGet, limes.OpList),
				RequireBiscuit: tc.RequireBiscuit,
			}
			if tc.Ops != nil {
				req.Caveats = openCaveats(opsOf(*tc.Ops)...)
			}
			if tc.KeyBound {
				req.ConfirmationJkt = testJKT
			}
			switch tc.NotBefore {
			case notBeforeLater:
				req.NotBefore = timestamppb.New(time.Now().Add(time.Minute))
			case notBeforePastExpiry:
				req.NotBefore = timestamppb.New(time.Now().Add(time.Hour))
			}
			if tc.NoRequestCeiling {
				req.Caveats.RequestCeiling = nil
			}
			if tc.NoBudgetCeiling {
				req.Caveats.BudgetCeiling = nil
			}
			if got := gotIssuance(h.Issue(adminCtx(), proto.CloneOf(req))); got != tc.Want {
				t.Errorf("the server answers %+v, the table %+v", got, tc.Want)
			}
			if got := gotIssuance(fake.Issue(context.Background(), proto.CloneOf(req))); got != tc.Want {
				t.Errorf("the fake answers %+v, the table %+v", got, tc.Want)
			}
		})
	}
}

// Each Delegate case of the shared table — a parent without share, a child
// narrowed off share, one bound to a key, an op the parent lacks — runs
// through this handler and through paladintest alike.
func TestTheSDKFakeDelegatesAsTheServerDoes(t *testing.T) {
	_, cases := readIssuanceTable(t)
	srv := paladintest.New(t)
	m := captest.NewMinter(t)
	h := delegateHandler(m, &denyAuthorizer{})
	mutate := func(tc issuanceCase, r *adminv1.CapabilityServiceDelegateRequest) {
		if tc.ChildOps != nil {
			r.Caveats = openCaveats(opsOf(*tc.ChildOps)...)
			r.Caveats.ResourcePrefixes = tc.ChildPrefixes
			r.Caveats.AllowTaintedRead = tc.ChildTaintedRead
		}
		r.Audience = tc.ChildAudience
		if tc.KeyBound {
			r.ConfirmationJkt = testJKT
		}
		r.RequireBiscuit = tc.RequireBiscuit
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			parent, _ := m.Mint(t, limes.Capability{
				Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
				Caveats: limes.Caveats{
					Ops: opsOf(tc.ParentOps), ResourcePrefixes: tc.ParentPrefixes, IdempotencyKeyRequired: tc.ParentIdempotencyKey,
				},
			})
			serverReq := delegateReq(parent)
			mutate(tc, serverReq)
			if got := gotIssuance(h.Delegate(auth.WithCapability(context.Background(), parent), serverReq)); got != tc.Want {
				t.Errorf("the server answers %+v, the table %+v", got, tc.Want)
			}

			issueReq := &adminv1.CapabilityServiceIssueRequest{
				Subject: &adminv1.CapabilityPrincipal{
					Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:parent",
				},
				Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
				Caveats:  openCaveats(opsOf(tc.ParentOps)...),
			}
			issueReq.Caveats.ResourcePrefixes = tc.ParentPrefixes
			issueReq.Caveats.IdempotencyKeyRequired = tc.ParentIdempotencyKey
			fakeParent, err := srv.Connect().Admin.Capability.Issue(context.Background(), issueReq)
			if err != nil {
				t.Fatal(err)
			}
			fakeReq := &adminv1.CapabilityServiceDelegateRequest{ParentId: fakeParent.GetCapability().GetId(), TtlSeconds: delegateTTLSeconds}
			mutate(tc, fakeReq)
			faked, fakeErr := srv.Connect(paladin.WithCapability(fakeParent.GetToken())).Admin.Capability.Delegate(context.Background(), fakeReq)
			if got := gotIssuance(faked, fakeErr); got != tc.Want {
				t.Errorf("the fake answers %+v, the table %+v", got, tc.Want)
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
	if first, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: parent.Capability().ID.String()}); err != nil || first.GetAlreadyRevoked() ||
		first.GetBefore() != adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE {
		t.Fatalf("server Revoke: %v, already revoked %v, before %v", err, first.GetAlreadyRevoked(), first.GetBefore())
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
	if first, err := fake.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: fakeParent.GetCapability().GetId()}); err != nil || first.GetAlreadyRevoked() ||
		first.GetBefore() != adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE {
		t.Fatalf("fake Revoke: %v, already revoked %v, before %v", err, first.GetAlreadyRevoked(), first.GetBefore())
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
	if s, f := server.GetBefore(), faked.GetBefore(); s != adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED || f != s {
		t.Errorf("a second revoke: before %v on the server, %v on the fake; want revoked", s, f)
	}

	// The child, stopped only through its parent, has no entry of its own:
	// its revoke writes one, but stops nothing new, and both say so.
	server, err = h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: child.GetCapability().GetId()})
	if err != nil {
		t.Fatalf("server Revoke child: %v", err)
	}
	faked, err = fake.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: fakeChild.GetCapability().GetId()})
	if err != nil {
		t.Fatalf("fake Revoke child: %v", err)
	}
	if server.GetAlreadyRevoked() || faked.GetAlreadyRevoked() {
		t.Errorf("the child's revoke: already revoked on the server %v, on the fake %v; want neither", server.GetAlreadyRevoked(), faked.GetAlreadyRevoked())
	}
	if s, f := server.GetBefore(), faked.GetBefore(); s != adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED || f != s {
		t.Errorf("the child's revoke: before %v on the server, %v on the fake; want revoked", s, f)
	}
}

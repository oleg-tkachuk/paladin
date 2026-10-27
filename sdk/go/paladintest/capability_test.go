package paladintest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// A JWK thumbprint to bind a capability to: its shape is all the server
// checks before minting.
const testJKT = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"

// issueRequest asks for a capability of the fake's tenant with ops and
// ceilings of no limit, for the data plane and the admin plane Delegate is
// on, by their labels: a bearer audience there is refused, as the server
// refuses it.
func issueRequest(srv *paladintest.Server, ops ...paladin.Op) *adminv1.CapabilityServiceIssueRequest {
	return &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:worker",
		},
		Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
		Caveats: &adminv1.CapabilityCaveats{
			Ops:            paladin.OpNames(ops...),
			RequestCeiling: &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
			BudgetCeiling:  &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{}},
		},
	}
}

// issue mints a capability through the fake's CapabilityService.
func issue(t *testing.T, admin paladinadminv1connect.CapabilityServiceClient, req *adminv1.CapabilityServiceIssueRequest) *adminv1.CapabilityServiceIssueResponse {
	t.Helper()
	resp, err := admin.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return resp
}

// A capability without share is answered with a Biscuit, as the server
// answers it, and whatever CredentialOf picks is let through under strict
// authentication: what a consumer that handed the Biscuit on relies on.
func TestIssueSealsABiscuitWithoutShare(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	resp := issue(t, admin, issueRequest(srv, paladin.OpGet, paladin.OpList))
	if resp.GetBiscuitStatus() != adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED || resp.GetBiscuit() == "" || resp.GetToken() == "" {
		t.Fatalf("status %v, biscuit %t, token %t", resp.GetBiscuitStatus(), resp.GetBiscuit() != "", resp.GetToken() != "")
	}
	cred, err := paladin.CredentialOf(resp)
	if err != nil || cred.Form != paladin.FormBiscuit {
		t.Fatalf("CredentialOf = %+v, %v", cred, err)
	}
	for name, value := range map[string]string{"its Biscuit": resp.GetBiscuit(), "its token": resp.GetToken()} {
		if err := getObject(srv.Connect(paladin.WithCapability(value)), obj); err != nil {
			t.Errorf("%s: %v, want the object", name, err)
		}
	}
	got := resp.GetCapability()
	if got.GetId() == "" || got.GetSubject().GetTenantId() != srv.Tenant() || got.GetExpiresAt() == nil {
		t.Errorf("capability = %v", got)
	}
}

// The reasons a capability goes without its Biscuit are the server's: a key
// binding and a later not-before, not its ops.
func TestIssueNamesWhyThereIsNoBiscuit(t *testing.T) {
	srv := paladintest.New(t)
	admin := srv.Connect().Admin.Capability
	bound := issueRequest(srv, paladin.OpGet)
	bound.ConfirmationJkt = testJKT
	later := issueRequest(srv, paladin.OpGet)
	// Within DefaultCapabilityTTL: a not-before past the expiry is refused.
	later.NotBefore = timestamppb.New(time.Now().Add(time.Minute))
	for name, tc := range map[string]struct {
		req  *adminv1.CapabilityServiceIssueRequest
		want adminv1.BiscuitStatus
	}{
		"key-bound":     {bound, adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
		"not yet valid": {later, adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID},
	} {
		resp := issue(t, admin, tc.req)
		if resp.GetBiscuitStatus() != tc.want || resp.GetBiscuit() != "" {
			t.Errorf("%s: status %v, biscuit %q", name, resp.GetBiscuitStatus(), resp.GetBiscuit())
		}
		if cred, err := paladin.CredentialOf(resp); err != nil || cred.Form != paladin.FormToken || cred.Value != resp.GetToken() {
			t.Errorf("%s: CredentialOf = %+v, %v; want the token", name, cred, err)
		}
		if _, err := paladin.BiscuitOf(resp); !errors.Is(err, paladin.ErrNoBiscuit) {
			t.Errorf("%s: BiscuitOf = %v, want ErrNoBiscuit", name, err)
		}

		tc.req.RequireBiscuit = true
		before := len(srv.Calls(paladinadminv1connect.CapabilityServiceIssueProcedure, nil))
		_, err := admin.Issue(context.Background(), tc.req)
		if !errors.Is(err, paladin.ErrFailedPrecondition) || !strings.Contains(err.Error(), tc.want.String()) {
			t.Errorf("%s with require_biscuit: %v, want FailedPrecondition naming %s", name, err, tc.want)
		}
		if after := len(srv.Calls(paladinadminv1connect.CapabilityServiceIssueProcedure, nil)); after != before+1 {
			t.Errorf("%s: %d calls recorded, want one more", name, after-before)
		}
	}
}

// What the server refuses at Issue, the fake refuses: an operation limes does
// not define and is not namespaced — the bare "presign" a consumer of v39
// still sent — and a capability with no ceilings.
func TestIssueRefusesWhatTheServerRefuses(t *testing.T) {
	srv := paladintest.New(t)
	admin := srv.Connect().Admin.Capability
	noCeilings := issueRequest(srv, paladin.OpGet)
	noCeilings.Caveats.RequestCeiling = nil
	noBudget := issueRequest(srv, paladin.OpGet)
	noBudget.Caveats.BudgetCeiling = nil
	for name, tc := range map[string]struct {
		req  *adminv1.CapabilityServiceIssueRequest
		want string
	}{
		"a bare presign":     {issueRequest(srv, paladin.OpGet, "presign"), `"presign"`},
		"a misspelt op":      {issueRequest(srv, "Get"), `"Get"`},
		"no request ceiling": {noCeilings, "request_ceiling"},
		"no budget ceiling":  {noBudget, "budget_ceiling"},
	} {
		_, err := admin.Issue(context.Background(), tc.req)
		if !errors.Is(err, paladin.ErrInvalidArgument) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want InvalidArgument naming %s", name, err, tc.want)
		}
	}
	pastExpiry := issueRequest(srv, paladin.OpGet)
	pastExpiry.NotBefore = timestamppb.New(time.Now().Add(paladintest.DefaultCapabilityTTL + time.Minute))
	if _, err := admin.Issue(context.Background(), pastExpiry); !errors.Is(err, paladin.ErrInvalidArgument) {
		t.Errorf("a not-before past the expiry: %v, want InvalidArgument", err)
	}
	if _, err := admin.Issue(context.Background(), issueRequest(srv, paladin.OpObjectPresign, paladin.OpGet)); err != nil {
		t.Errorf("object:presign: %v, want it issued", err)
	}
}

// Under strict authentication Issue mints for the caller's own tenant only.
func TestStrictIssueStaysInTheCallersTenant(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	elsewhere := issueRequest(srv, paladin.OpGet)
	elsewhere.Subject.TenantId = uuid.New().String()
	if _, err := admin.Issue(context.Background(), elsewhere); !errors.Is(err, paladin.ErrPermissionDenied) {
		t.Errorf("issue for another tenant: %v, want PermissionDenied", err)
	}
	if _, err := srv.Connect().Admin.Capability.Issue(context.Background(), issueRequest(srv, paladin.OpGet)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("issue with no credential: %v, want Unauthenticated", err)
	}
}

// Delegate narrows the presented capability, by the server's rules.
func TestDelegate(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	parent := issue(t, admin, issueRequest(srv, paladin.OpGet, paladin.OpList, paladin.OpShare))
	noShare := issue(t, admin, issueRequest(srv, paladin.OpGet))
	as := func(token string) paladinadminv1connect.CapabilityServiceClient {
		return srv.Connect(paladin.WithCapability(token)).Admin.Capability
	}
	narrow := func(parentID string, ops ...paladin.Op) *adminv1.CapabilityServiceDelegateRequest {
		return &adminv1.CapabilityServiceDelegateRequest{ParentId: parentID, Caveats: &adminv1.CapabilityCaveats{Ops: paladin.OpNames(ops...)}}
	}
	ctx := context.Background()

	child, err := as(parent.GetToken()).Delegate(ctx, narrow(parent.GetCapability().GetId(), paladin.OpGet))
	if err != nil || child.GetBiscuitStatus() != adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		t.Fatalf("delegate: %v, status %v", err, child.GetBiscuitStatus())
	}
	if child.GetCapability().GetParentId() != parent.GetCapability().GetId() || child.GetCapability().GetSubject().GetTenantId() != srv.Tenant() {
		t.Errorf("child = %v", child.GetCapability())
	}
	if err := getObject(srv.Connect(paladin.WithCapability(child.GetBiscuit())), obj); err != nil {
		t.Errorf("the child's Biscuit: %v, want the object", err)
	}

	for name, tc := range map[string]struct {
		client paladinadminv1connect.CapabilityServiceClient
		req    *adminv1.CapabilityServiceDelegateRequest
		want   error
	}{
		"a parent without share":  {as(noShare.GetToken()), narrow(noShare.GetCapability().GetId(), paladin.OpGet), paladin.ErrPermissionDenied},
		"another parent_id":       {as(parent.GetToken()), narrow(noShare.GetCapability().GetId(), paladin.OpGet), paladin.ErrPermissionDenied},
		"a Biscuit copy":          {as(parent.GetBiscuit()), narrow(parent.GetCapability().GetId(), paladin.OpGet), paladin.ErrFailedPrecondition},
		"an op the parent lacks":  {as(parent.GetToken()), narrow(parent.GetCapability().GetId(), paladin.OpPut), paladin.ErrPermissionDenied},
		"a bare presign":          {as(parent.GetToken()), narrow(parent.GetCapability().GetId(), "presign"), paladin.ErrInvalidArgument},
		"no capability presented": {srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability, narrow(parent.GetCapability().GetId(), paladin.OpGet), paladin.ErrFailedPrecondition},
		"a key-bound child, asked": {as(parent.GetToken()), func() *adminv1.CapabilityServiceDelegateRequest {
			r := narrow(parent.GetCapability().GetId(), paladin.OpGet)
			r.ConfirmationJkt, r.RequireBiscuit = testJKT, true
			return r
		}(), paladin.ErrFailedPrecondition},
	} {
		if _, err := tc.client.Delegate(ctx, tc.req); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

// Delegate refuses what limes refuses as too wide — an audience, a resource
// or a binding the parent lacks — and cuts a child that would outlive its
// parent to the parent's end, as the server's issuer does.
func TestDelegateNarrowsByLimes(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	c := srv.Collection()
	prefix := paladin.ObjectResource(c.Tenant, c.Collection, "")
	req := issueRequest(srv, paladin.OpGet, paladin.OpShare)
	// Delegate is an admin call, so the parent holds the admin plane alone.
	req.Audience = paladin.PlaneNames(paladin.PlaneAdmin)
	req.Caveats.ResourcePrefixes = []string{prefix}
	parent := issue(t, admin, req)
	as := srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability
	ctx := context.Background()
	scoped := func() *adminv1.CapabilityServiceDelegateRequest {
		return &adminv1.CapabilityServiceDelegateRequest{
			ParentId: parent.GetCapability().GetId(),
			Caveats:  &adminv1.CapabilityCaveats{Ops: paladin.OpNames(paladin.OpGet), ResourcePrefixes: []string{prefix}},
		}
	}

	wider := scoped()
	wider.Audience = paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin)
	unscoped := scoped()
	unscoped.Caveats.ResourcePrefixes = nil
	for name, r := range map[string]*adminv1.CapabilityServiceDelegateRequest{
		"an audience the parent lacks": wider,
		"no resource restriction":      unscoped,
	} {
		if _, err := as.Delegate(ctx, r); !errors.Is(err, paladin.ErrPermissionDenied) || !strings.Contains(err.Error(), "wider than parent") {
			t.Errorf("%s: %v, want PermissionDenied as too wide", name, err)
		}
	}

	long := scoped()
	long.TtlSeconds = int64((paladintest.DefaultCapabilityTTL + time.Hour) / time.Second)
	child, err := as.Delegate(ctx, long)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := child.GetCapability().GetExpiresAt().AsTime(), parent.GetCapability().GetExpiresAt().AsTime(); !got.Equal(want) {
		t.Errorf("child expires %s, want the parent's %s", got, want)
	}
}

// Revoke refuses a capability's token and Biscuit from then on, and with
// cascade_children its children's; Get reads what the fake issued.
func TestRevokeAndGet(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	ctx := context.Background()
	parent := issue(t, admin, issueRequest(srv, paladin.OpGet, paladin.OpShare))
	child, err := srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability.Delegate(ctx,
		&adminv1.CapabilityServiceDelegateRequest{ParentId: parent.GetCapability().GetId()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := admin.Get(ctx, &adminv1.CapabilityServiceGetRequest{Id: parent.GetCapability().GetId()})
	if err != nil || got.GetCapability().GetId() != parent.GetCapability().GetId() {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if _, err := admin.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: parent.GetCapability().GetId(), CascadeChildren: true}); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{parent.GetToken(), parent.GetBiscuit(), child.GetToken()} {
		wantRefused(t, getObject(srv.Connect(paladin.WithCapability(token)), obj), capabilityRevoked)
	}
	unknown := uuid.New().String()
	if _, err := admin.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: unknown}); !errors.Is(err, paladin.ErrNotFound) {
		t.Errorf("revoke an unknown id: %v, want NotFound", err)
	}
	if _, err := admin.Get(ctx, &adminv1.CapabilityServiceGetRequest{Id: unknown}); !errors.Is(err, paladin.ErrNotFound) {
		t.Errorf("get an unknown id: %v, want NotFound", err)
	}
}

// FailRPC takes CapabilityService's procedures, and an RPC the fake does not
// serve answers Unimplemented.
func TestCapabilityServiceFaultsAndGaps(t *testing.T) {
	srv := paladintest.New(t)
	admin := srv.Connect().Admin.Capability
	srv.FailRPC(paladinadminv1connect.CapabilityServiceIssueProcedure, 1, connect.CodeUnavailable)
	if _, err := admin.Issue(context.Background(), issueRequest(srv, paladin.OpGet)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("a failed Issue: %v, want Unavailable", err)
	}
	if _, err := admin.GetUsage(context.Background(), &adminv1.CapabilityServiceGetUsageRequest{Id: uuid.New().String()}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("GetUsage: %v, want Unimplemented", err)
	}
}

// A capability delegated from a revoked one is listed as revoked, as the
// server lists it, though it holds no revocation of its own; and the
// tenant's listing is the tenant's alone.
func TestListCountsAChildOfARevokedParentRevoked(t *testing.T) {
	srv := paladintest.New(t)
	admin := srv.Connect().Admin.Capability
	ctx := context.Background()
	parent := issue(t, admin, issueRequest(srv, paladin.OpGet, paladin.OpShare))
	child, err := srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability.Delegate(ctx,
		&adminv1.CapabilityServiceDelegateRequest{ParentId: parent.GetCapability().GetId()})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	elsewhere := issueRequest(srv, paladin.OpGet)
	elsewhere.Subject.TenantId = uuid.New().String()
	issue(t, admin, elsewhere)
	if _, err := admin.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: parent.GetCapability().GetId()}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	resp, err := admin.List(ctx, &adminv1.CapabilityServiceListRequest{
		TenantId:     srv.Tenant(),
		Dispositions: []adminv1.CapabilityDisposition{adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got []string
	for _, c := range resp.GetCapabilities() {
		got = append(got, c.GetId())
	}
	want := []string{parent.GetCapability().GetId(), child.GetCapability().GetId()}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("revoked = %v, want the parent then its child %v", got, want)
	}
	active, err := admin.List(ctx, &adminv1.CapabilityServiceListRequest{TenantId: srv.Tenant()})
	if err != nil || len(active.GetCapabilities()) != 0 {
		t.Errorf("active = %v, %v; want none", active.GetCapabilities(), err)
	}
}

// Under strict authentication a caller lists its own tenant's capabilities
// only, as the server refuses another's to all but a platform role.
func TestStrictListStaysInTheCallersTenant(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	if _, err := admin.List(context.Background(), &adminv1.CapabilityServiceListRequest{TenantId: srv.Tenant()}); err != nil {
		t.Errorf("own tenant: %v", err)
	}
	_, err := admin.List(context.Background(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String()})
	if !errors.Is(err, paladin.ErrPermissionDenied) {
		t.Errorf("another tenant: %v, want PermissionDenied", err)
	}
}

// limes binds a listing's cursor to its tenant: one handed back for another
// tenant is refused rather than paged on, as the server refuses it.
func TestListRefusesACursorOfAnotherTenant(t *testing.T) {
	srv := paladintest.New(t)
	admin := srv.Connect().Admin.Capability
	ctx := context.Background()
	for range 2 {
		issue(t, admin, issueRequest(srv, paladin.OpGet))
	}
	first, err := admin.List(ctx, &adminv1.CapabilityServiceListRequest{TenantId: srv.Tenant(), PageSize: 1})
	if err != nil || first.GetNextPageToken() == "" {
		t.Fatalf("first page: %v, next %q", err, first.GetNextPageToken())
	}
	_, err = admin.List(ctx, &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String(), PageToken: first.GetNextPageToken()})
	if !errors.Is(err, paladin.ErrInvalidArgument) {
		t.Errorf("another tenant's cursor: %v, want InvalidArgument", err)
	}
	if _, err := admin.List(ctx, &adminv1.CapabilityServiceListRequest{TenantId: srv.Tenant(), PageToken: first.GetNextPageToken()}); err != nil {
		t.Errorf("its own tenant's cursor: %v", err)
	}
}

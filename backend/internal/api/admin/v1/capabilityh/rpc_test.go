package capabilityh

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// Drives Issue / Revoke / List / GetUsage through the handler with the
// package's existing fakes. The Cedar gate is asserted per RPC — these mint
// and revoke authority, so an ungated path is the worst failure mode here.

func adminCtx() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "operator", TenantID: uuid.New(), Roles: []string{"platform.admin"},
	})
}

func codeOf(err error) connect.Code { return connect.CodeOf(err) }

// recordingStore extends the package fake with argument capture and
// configurable failures for the paths the existing tests do not drive.
type recordingStore struct {
	fakeStore
	revokeArgs *capability.RevokeRequest
	revokeErr  error
	revokeCtx  context.Context
	listArgs   *capability.ListByPrincipalRequest
	listOut    []capability.Capability
	listNext   string
	listErr    error
}

func (s *recordingStore) Revoke(ctx context.Context, args capability.RevokeRequest) error {
	s.revokeArgs = &args
	s.revokeCtx = ctx
	return s.revokeErr
}

func (s *recordingStore) ListByPrincipal(_ context.Context, args capability.ListByPrincipalRequest) ([]capability.Capability, string, error) {
	s.listArgs = &args
	return s.listOut, s.listNext, s.listErr
}

// fakeUsage embeds the interface so only Get needs implementing; any other
// method the handler might start calling panics loudly instead of silently
// returning a zero value.
type fakeUsage struct {
	capability.UsageStore[pgx.Tx]
	out capability.Usage
	err error
	got uuid.UUID
	ctx context.Context
}

func (u *fakeUsage) GetUsage(ctx context.Context, id uuid.UUID) (capability.Usage, error) {
	u.got = id
	u.ctx = ctx
	return u.out, u.err
}

// ─── NewHandler ────────────────────────────────────────────────────────────

// Wiring bugs must fail at boot, not on the first request that would otherwise
// mint an ungated capability.
func TestNewHandlerPanicsOnMissingDependencies(t *testing.T) {
	store := &fakeStore{}
	issuer := mkIssuer(t, store)

	cases := map[string]func(){
		"nil issuer": func() { NewHandler(nil, store, nil, &allowAuthorizer{}) },
		"nil store":  func() { NewHandler(issuer, nil, nil, &allowAuthorizer{}) },
		"nil policy": func() { NewHandler(issuer, store, nil, nil) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("want a panic")
				}
			}()
			fn()
		})
	}
}

// usage may legitimately be nil — the operator simply hasn't wired the
// counter store.
func TestNewHandlerAllowsNilUsage(t *testing.T) {
	store := &fakeStore{}
	if h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{}); h == nil {
		t.Fatal("NewHandler returned nil")
	}
}

// ─── Issue ─────────────────────────────────────────────────────────────────

func TestIssueMintsAToken(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})
	tenant := uuid.New()

	resp, err := h.Issue(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: tenant.String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if resp.Msg.GetToken() == "" {
		t.Error("want a minted token")
	}
	if resp.Msg.GetCapability() == nil {
		t.Error("want the capability echoed back")
	}
	// The same capability as a Biscuit, which its holder can attenuate.
	if b := resp.Msg.GetBiscuit(); !capability.IsBiscuit(b) {
		t.Errorf("want a Biscuit beside the token, got %q", b)
	} else if _, err := capability.Attenuate(b, capability.Attenuation{Ops: []capability.Op{capability.OpGet}}); err != nil {
		t.Errorf("the returned Biscuit does not attenuate: %v", err)
	}
}

func TestIssueIsCedarGated(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &denyAuthorizer{})

	_, err := h.Issue(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{TenantId: uuid.New().String()},
	}))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// A malformed subject must be rejected before anything is minted.
func TestIssueRejectsBadSubject(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})

	for name, req := range map[string]*adminv1.CapabilityServiceIssueRequest{
		"no subject":  {},
		"bad tenant":  {Subject: &adminv1.CapabilityPrincipal{TenantId: "nope"}},
		"bad lineage": {Subject: &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: uuid.New().String(), RunId: "nope"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.Issue(adminCtx(), connect.NewRequest(req))
			if codeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", codeOf(err))
			}
		})
	}
}

// ─── Revoke ────────────────────────────────────────────────────────────────

func TestRevokeForwardsArgs(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})
	id := uuid.New()

	_, err := h.Revoke(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{
		Id: id.String(), Reason: "leaked", CascadeChildren: true,
	}))
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.revokeArgs == nil {
		t.Fatal("store.Revoke was not called")
	}
	if store.revokeArgs.ID != id || store.revokeArgs.Reason != "leaked" {
		t.Errorf("args = %+v", store.revokeArgs)
	}
	// Cascade is what tears down the delegated children of a leaked token.
	if !store.revokeArgs.CascadeChildren {
		t.Error("CascadeChildren must be forwarded")
	}
	// The actor is the audit trail for who revoked it.
	if store.revokeArgs.Actor != "operator" {
		t.Errorf("Actor = %q, want operator", store.revokeArgs.Actor)
	}
}

func TestRevokeIsCedarGated(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &denyAuthorizer{})

	_, err := h.Revoke(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
	if store.revokeArgs != nil {
		t.Error("a denied revoke must not reach the store")
	}
}

func TestRevokeRejectsBadID(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	_, err := h.Revoke(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{Id: "nope"}))
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestRevokeStoreErrorIsInternal(t *testing.T) {
	store := &recordingStore{revokeErr: errors.New("db down")}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	_, err := h.Revoke(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
}

// ─── List ──────────────────────────────────────────────────────────────────

func TestListForwardsFiltersAndPaging(t *testing.T) {
	tenant := uuid.New()
	store := &recordingStore{listNext: "cursor-2"}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	resp, err := h.List(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceListRequest{
		TenantId:       tenant.String(),
		PrincipalKind:  adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT,
		Subject:        "agent-1",
		IncludeExpired: true,
		IncludeRevoked: true,
		PageToken:      "cursor-1",
		PageSize:       25,
	}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.listArgs == nil {
		t.Fatal("store.ListByPrincipal was not called")
	}
	a := store.listArgs
	if a.TenantID != tenant || a.Subject != "agent-1" || a.PrincipalType != capability.PrincipalAgent {
		t.Errorf("filters = %+v", a)
	}
	// Include flags widen the result set; dropping one silently hides revoked
	// tokens from an operator auditing a leak.
	if !a.IncludeExpired || !a.IncludeRevoked {
		t.Errorf("include flags = %v / %v", a.IncludeExpired, a.IncludeRevoked)
	}
	if a.Cursor != "cursor-1" || a.Limit != 25 {
		t.Errorf("paging = %q / %d", a.Cursor, a.Limit)
	}
	if resp.Msg.GetNextPageToken() != "cursor-2" {
		t.Errorf("next token = %q", resp.Msg.GetNextPageToken())
	}
}

func TestListEmptyIsNotNil(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	resp, err := h.List(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp.Msg.GetCapabilities() == nil {
		t.Error("an empty list must serialise as [], not null")
	}
}

func TestListIsCedarGated(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &denyAuthorizer{})

	_, err := h.List(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestListRejectsBadTenant(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceListRequest{TenantId: "nope"}))
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestListStoreErrorIsInternal(t *testing.T) {
	store := &recordingStore{listErr: errors.New("db down")}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
}

// A page token the store cannot read is a bad request, not a server fault.
func TestListBadPageTokenIsInvalidArgument(t *testing.T) {
	store := &recordingStore{listErr: fmt.Errorf("%w: cursor", capability.ErrInvalidRequest)}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	_, err := h.List(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceListRequest{
		TenantId: uuid.New().String(), PageToken: "garbage",
	}))
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

// ─── GetUsage ──────────────────────────────────────────────────────────────

// An unwired counter store must be visibly Unavailable rather than reporting
// zero usage, which would read as "this capability spent nothing".
func TestGetUsageUnwiredIsUnavailable(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", codeOf(err))
	}
}

func TestGetUsageReturnsCounters(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{}
	usage := &fakeUsage{out: capability.Usage{
		CapabilityID: id, RequestCount: 7, SpentAmount: capability.MustParseAmount("42"), UnitCode: "EUR",
	}}
	h := NewHandler(mkIssuer(t, store), store, usage, &allowAuthorizer{})

	resp, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
		Id: id.String(),
	}))
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if usage.got != id {
		t.Errorf("looked up %v, want %v", usage.got, id)
	}
	if resp.Msg.GetRequestCount() != 7 || !proto.Equal(resp.Msg.GetSpent(), &money.Money{CurrencyCode: "EUR", Units: 42}) {
		t.Errorf("counters = %d / %v", resp.Msg.GetRequestCount(), resp.Msg.GetSpent())
	}
}

// A row written before the currency rename carries no unit; the response must
// still name one so clients never render a bare number.
func TestGetUsageDefaultsUnitCode(t *testing.T) {
	store := &fakeStore{}
	usage := &fakeUsage{out: capability.Usage{CapabilityID: uuid.New()}}
	h := NewHandler(mkIssuer(t, store), store, usage, &allowAuthorizer{})

	resp, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if got := resp.Msg.GetSpent().GetCurrencyCode(); got != capability.DefaultUnitCode {
		t.Errorf("unit = %q, want the %q default", got, capability.DefaultUnitCode)
	}
}

func TestGetUsageNotFound(t *testing.T) {
	store := &fakeStore{}
	usage := &fakeUsage{err: capability.ErrUsageNotFound}
	h := NewHandler(mkIssuer(t, store), store, usage, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
}

func TestGetUsageStoreErrorIsInternal(t *testing.T) {
	store := &fakeStore{}
	usage := &fakeUsage{err: errors.New("db down")}
	h := NewHandler(mkIssuer(t, store), store, usage, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
}

func TestGetUsageRejectsBadID(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, &fakeUsage{}, &allowAuthorizer{})

	_, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{Id: "nope"}))
	if codeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestGetUsageIsCedarGated(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, &fakeUsage{}, &denyAuthorizer{})

	_, err := h.GetUsage(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// ─── cross-tenant issuance ─────────────────────────────────────────────────

// tenantCtx is a caller scoped to one tenant with a tenant-level role — the
// shape that could previously mint for anybody.
func tenantCtx(tenant uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tenant-operator", TenantID: tenant, Roles: []string{"tenant.admin"},
	})
}

// Since ADR-0010 a capability authenticates AS its subject's tenant, so minting
// one for another tenant is a full cross-tenant grant. Cedar authorises
// IssueCapability against the caller's own tenant policy and says nothing
// about the subject's tenant, so the handler has to.
func TestIssue_NonPlatformAdminCannotIssueForAnotherTenant(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})
	caller, victim := uuid.New(), uuid.New()

	_, err := h.Issue(tenantCtx(caller), connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: victim.String(), Subject: "victim",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	}))

	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// Issuing within one's own tenant stays available to a tenant-level caller.
func TestIssue_TenantAdminMayIssueForItsOwnTenant(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})
	own := uuid.New()

	_, err := h.Issue(tenantCtx(own), connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: own.String(), Subject: "self",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	}))
	if err != nil {
		t.Fatalf("Issue for own tenant: %v", err)
	}
}

// A platform admin still mints for anybody — that is how a consumer serving
// many tenants gets per-tenant credentials. Through the real engine and an
// empty tenant policy: the grant has to come from the built-in policy.
func TestIssue_PlatformAdminMayIssueForAnotherTenant(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, cedartest.Engine(""))

	_, err := h.Issue(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "svc",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	}))
	if err != nil {
		t.Fatalf("Issue as platform admin: %v", err)
	}
}

// issuerCtx is the narrow grant: may mint a capability for any tenant, and
// carries no other authority.
func issuerCtx(tenant uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "apikey:acme", TenantID: tenant,
		Roles: []string{"platform.capability-issuer"},
	})
}

// The whole point of the narrow role: a consumer serving many tenants mints
// per-tenant capabilities without holding platform.admin.
//
// Through the real engine, not a stub that allows everything: with the stub
// this test passed for as long as no policy granted the role anything, while
// every Issue it made in a cluster was denied.
func TestIssue_CapabilityIssuerMayIssueForAnotherTenant(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, cedartest.Engine(""))

	_, err := h.Issue(issuerCtx(uuid.New()), connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "svc",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	}))
	if err != nil {
		t.Fatalf("Issue as capability-issuer: %v", err)
	}
}

// ─── another tenant's capability ───────────────────────────────────────────

// lookupStore records the context the owner lookup ran on.
type lookupStore struct {
	recordingStore
	getCtx context.Context
}

func (s *lookupStore) Get(ctx context.Context, id uuid.UUID) (capability.Capability, error) {
	s.getCtx = ctx
	return s.recordingStore.Get(ctx, id)
}

func callerCtx(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "operator", TenantID: tenant, Roles: roles,
	})
}

// Revoke and GetUsage name a capability by id alone, and its rows are
// RLS-isolated by its own tenant. A platform admin's connection is scoped to
// the platform tenant, so before this both answered NotFound for every
// capability the admin had just issued to a tenant.
func TestRevokeAndGetUsageActOnTheCapabilitysTenant(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, capability.OpGet)

	// The roles are admitted by the built-in policy alone; the tenant-confined
	// caller holds no role, so its own tenant's policy has to grant it.
	const tenantGrant = `permit(principal, action in [Action::"RevokeCapability", Action::"ReadCapability"], resource);`
	cases := map[string]struct {
		ctx          context.Context
		tenantPolicy string
		wantActing   bool
	}{
		"platform admin":         {callerCtx(uuid.New(), "platform.admin"), "", true},
		"capability issuer":      {callerCtx(uuid.New(), "platform.capability-issuer"), "", true},
		"tenant-confined caller": {callerCtx(uuid.New()), tenantGrant, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
			usage := &fakeUsage{out: capability.Usage{CapabilityID: target.ID}}
			h := NewHandler(mkIssuer(t, &store.fakeStore), store, usage, cedartest.Engine(tc.tenantPolicy))

			if _, err := h.Revoke(tc.ctx, connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{
				Id: target.ID.String(),
			})); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if _, err := h.GetUsage(tc.ctx, connect.NewRequest(&adminv1.CapabilityServiceGetUsageRequest{
				Id: target.ID.String(),
			})); err != nil {
				t.Fatalf("GetUsage: %v", err)
			}

			for call, ctx := range map[string]context.Context{"Revoke": store.revokeCtx, "GetUsage": usage.ctx} {
				acting, ok := auth.ActingTenant(ctx)
				switch {
				case tc.wantActing && (!ok || acting != owner):
					t.Errorf("%s ran acting on %v (set=%v), want the owner %v", call, acting, ok, owner)
				case !tc.wantActing && ok:
					t.Errorf("%s acted on %v for a caller confined to its own tenant", call, acting)
				}
			}
			if tc.wantActing {
				if store.getCtx == nil || !auth.CrossTenantRead(store.getCtx) {
					t.Error("the owner lookup must read across tenants, or RLS hides the record")
				}
			} else if store.getCtx != nil {
				t.Error("a tenant-confined caller must not get a cross-tenant owner lookup")
			}
		})
	}
}

// An id that matches no capability stays NotFound for an admin too, rather
// than turning into Internal on the owner lookup.
func TestRevokeUnknownIDIsNotFoundForAnAdmin(t *testing.T) {
	store := &recordingStore{revokeErr: capability.ErrNotFound}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	_, err := h.Revoke(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{
		Id: uuid.New().String(),
	}))
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", codeOf(err))
	}
}

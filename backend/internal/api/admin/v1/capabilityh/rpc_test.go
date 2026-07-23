package capabilityh

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/auth"
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
	revokeArgs *capability.RevokeArgs
	revokeErr  error
	listArgs   *capability.ListByPrincipalArgs
	listOut    []capability.Capability
	listNext   string
	listErr    error
}

func (s *recordingStore) Revoke(_ context.Context, args capability.RevokeArgs) error {
	s.revokeArgs = &args
	return s.revokeErr
}

func (s *recordingStore) ListByPrincipal(_ context.Context, args capability.ListByPrincipalArgs) ([]capability.Capability, string, error) {
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
}

func (u *fakeUsage) Get(_ context.Context, id uuid.UUID) (capability.Usage, error) {
	u.got = id
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
	if a.TenantID != tenant || a.Subject != "agent-1" || a.PrincipalT != capability.PrincipalAgent {
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
		CapabilityID: id, RequestCount: 7, SpentAmount: 42, UnitCode: "EUR",
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
	if resp.Msg.GetRequestCount() != 7 || resp.Msg.GetSpentAmount() != 42 {
		t.Errorf("counters = %d / %v", resp.Msg.GetRequestCount(), resp.Msg.GetSpentAmount())
	}
	if resp.Msg.GetUnitCode() != "EUR" {
		t.Errorf("UnitCode = %q, want EUR", resp.Msg.GetUnitCode())
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
	if resp.Msg.GetUnitCode() != capability.DefaultUnitCode {
		t.Errorf("UnitCode = %q, want the %q default", resp.Msg.GetUnitCode(), capability.DefaultUnitCode)
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

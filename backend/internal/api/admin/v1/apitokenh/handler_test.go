package apitokenh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// stubIssuer is the seam the issuer-interface extraction made testable.
type stubIssuer struct {
	req api_token.IssueRequest
	tok *api_token.Token
	err error
}

func (s *stubIssuer) Issue(_ context.Context, r api_token.IssueRequest) (*api_token.Token, error) {
	s.req = r
	if s.err != nil {
		return nil, s.err
	}
	if s.tok != nil {
		return s.tok, nil
	}
	return &api_token.Token{
		ID: uuid.New(), TenantID: r.TenantID, Name: r.Name,
		Plaintext: "paladin_pat_secret123", CreatedBy: r.CreatedBy,
	}, nil
}

type fakeStore struct {
	get         api_token.Token
	getErr      error
	list        []api_token.Token
	revokeCalls int
}

func (f *fakeStore) Insert(context.Context, api_token.Token, []byte) error { return nil }
func (f *fakeStore) FindByDigest(context.Context, []byte) (api_token.Token, error) {
	return api_token.Token{}, api_token.ErrTokenNotFound
}
func (f *fakeStore) Get(context.Context, uuid.UUID) (api_token.Token, error) {
	return f.get, f.getErr
}
func (f *fakeStore) Revoke(context.Context, uuid.UUID) error { f.revokeCalls++; return nil }
func (f *fakeStore) TouchLastUsed(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}
func (f *fakeStore) ListByTenant(context.Context, api_token.ListByTenantArgs) ([]api_token.Token, string, error) {
	return f.list, "", nil
}
func (f *fakeStore) PurgeExpired(context.Context, time.Duration) (int64, error) { return 0, nil }

type fakeLimiter struct{ snap ratelimit.Snapshot }

func (fakeLimiter) Allow(context.Context, uuid.UUID, int) (ratelimit.Decision, error) {
	return ratelimit.Decision{Allowed: true}, nil
}
func (f fakeLimiter) Usage(context.Context, uuid.UUID) (ratelimit.Snapshot, error) {
	return f.snap, nil
}
func (fakeLimiter) Sweep(context.Context, time.Duration) (int64, error) { return 0, nil }

func ctxAs(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "admin", TenantID: uuid.New(), Roles: roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

func newHandler(iss tokenIssuer, store api_token.Store, authz cedar.Authorizer) *Handler {
	return NewHandler(iss, store, fakeLimiter{}, authz)
}

// ─── Create ──────────────────────────────────────────────────────────────────

func TestCreate_CedarDenied(t *testing.T) {
	h := newHandler(&stubIssuer{}, &fakeStore{}, denyAuthorizer{})
	_, err := h.Create(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: uuid.NewString(), Name: "ci",
	}))
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestCreate_InvalidTenant(t *testing.T) {
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})
	_, err := h.Create(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: "not-a-uuid", Name: "ci",
	}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestCreate_SuccessReturnsPlaintextOnce(t *testing.T) {
	tid := uuid.New()
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, allowAuthorizer{})
	// Valid resource scopes (tenant:/backend:/bucket:/collection:/*). These must
	// pass the mint-time auth.ParseScope validation and reach the issuer verbatim.
	scopes := []string{"bucket:ci-bucket", "collection:ci-bucket/artifacts"}
	resp, err := h.Create(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: tid.String(), Name: "ci-runner", Scopes: scopes,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Msg.GetToken() != "paladin_pat_secret123" {
		t.Errorf("Create should return the one-time plaintext, got %q", resp.Msg.GetToken())
	}
	if resp.Msg.GetApiToken().GetName() != "ci-runner" {
		t.Errorf("response metadata name = %q, want ci-runner", resp.Msg.GetApiToken().GetName())
	}
	// The issuer received the request faithfully, with the caller stamped and
	// the scopes passed through to issuer→store→row unchanged.
	if iss.req.TenantID != tid || iss.req.Name != "ci-runner" || iss.req.CreatedBy != "admin" {
		t.Errorf("issuer got %+v, want tenant=%s name=ci-runner createdBy=admin", iss.req, tid)
	}
	if len(iss.req.Scopes) != len(scopes) {
		t.Fatalf("issuer scopes = %v, want %v", iss.req.Scopes, scopes)
	}
	for i := range scopes {
		if iss.req.Scopes[i] != scopes[i] {
			t.Errorf("issuer scope[%d] = %q, want %q", i, iss.req.Scopes[i], scopes[i])
		}
	}
}

// TestCreate_RejectsMalformedScope proves the mint-path validation: a scope
// string that auth.ParseScope can't decode is rejected at the edge (InvalidArgument)
// and never reaches the issuer — so an operator can't mint a token that would be
// dead-on-arrival (fail-closed) on the data plane.
func TestCreate_RejectsMalformedScope(t *testing.T) {
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, allowAuthorizer{})
	_, err := h.Create(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: uuid.NewString(), Name: "bad", Scopes: []string{"api:read"}, // legacy vocab — no longer valid
	}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
	if iss.req.Name != "" {
		t.Error("issuer must NOT be called when a scope is invalid")
	}
}

// TestCreateThenList_ScopesRoundTrip proves scopes survive the row and come back
// on the read path: a token stored WITH scopes surfaces them on List's proto
// mapping (tokenToProto → APIToken.Scopes), the same mapping GetSelf/Get use.
func TestCreateThenList_ScopesRoundTrip(t *testing.T) {
	tid := uuid.New()
	scopes := []string{"tenant:" + tid.String(), "bucket:reports"}
	store := &fakeStore{list: []api_token.Token{
		{ID: uuid.New(), TenantID: tid, Name: "scoped", Scopes: scopes},
	}}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})
	resp, err := h.List(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceListRequest{
		TenantId: tid.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := resp.Msg.GetApiTokens()
	if len(got) != 1 {
		t.Fatalf("got %d tokens, want 1", len(got))
	}
	if gs := got[0].GetScopes(); len(gs) != len(scopes) || gs[0] != scopes[0] || gs[1] != scopes[1] {
		t.Errorf("round-tripped scopes = %v, want %v", gs, scopes)
	}
}

// ─── Revoke ──────────────────────────────────────────────────────────────────

func TestRevoke_InvalidID(t *testing.T) {
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})
	_, err := h.Revoke(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceRevokeRequest{Id: "x"}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestRevoke_Success(t *testing.T) {
	store := &fakeStore{}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})
	_, err := h.Revoke(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceRevokeRequest{
		Id: uuid.NewString(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.revokeCalls != 1 {
		t.Errorf("Revoke called %d times, want 1", store.revokeCalls)
	}
}

// ─── List ────────────────────────────────────────────────────────────────────

func TestList_InvalidTenant(t *testing.T) {
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})
	_, err := h.List(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceListRequest{TenantId: "x"}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestList_MapsRows(t *testing.T) {
	store := &fakeStore{list: []api_token.Token{
		{ID: uuid.New(), Name: "a"}, {ID: uuid.New(), Name: "b"},
	}}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})
	resp, err := h.List(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceListRequest{
		TenantId: uuid.NewString(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.GetApiTokens()) != 2 {
		t.Errorf("got %d tokens, want 2", len(resp.Msg.GetApiTokens()))
	}
}

// ─── GetSelf ─────────────────────────────────────────────────────────────────

func TestGetSelf_NotTokenAuth(t *testing.T) {
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})
	_, err := h.GetSelf(context.Background(), connect.NewRequest(&adminv1.APITokenServiceGetSelfRequest{}))
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound (not token-authenticated)", code(err))
	}
}

func TestGetSelf_ReturnsContextToken(t *testing.T) {
	tok := &api_token.Token{ID: uuid.New(), Name: "self"}
	ctx := auth.WithAPIToken(context.Background(), tok)
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})
	resp, err := h.GetSelf(ctx, connect.NewRequest(&adminv1.APITokenServiceGetSelfRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Msg.GetApiToken().GetName() != "self" {
		t.Errorf("got %q, want the context token", resp.Msg.GetApiToken().GetName())
	}
}

// ─── GetUsage ────────────────────────────────────────────────────────────────

func TestGetUsage_NotFound(t *testing.T) {
	store := &fakeStore{getErr: api_token.ErrTokenNotFound}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})
	_, err := h.GetUsage(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceGetUsageRequest{
		Id: uuid.NewString(),
	}))
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGetUsage_Success(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{get: api_token.Token{ID: id, RateLimitRPM: 60}}
	h := NewHandler(&stubIssuer{}, store,
		fakeLimiter{snap: ratelimit.Snapshot{CurrentBucketCount: 5, WeightedCount: 7}},
		allowAuthorizer{})
	resp, err := h.GetUsage(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceGetUsageRequest{
		Id: id.String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Msg.GetLimitRpm() != 60 || resp.Msg.GetWeightedCount() != 7 {
		t.Errorf("usage = limit %v weighted %v, want 60 / 7",
			resp.Msg.GetLimitRpm(), resp.Msg.GetWeightedCount())
	}
}

// Compile-time interface assertions — including the new issuer seam.
var (
	_ tokenIssuer       = (*stubIssuer)(nil)
	_ api_token.Store   = (*fakeStore)(nil)
	_ ratelimit.Limiter = fakeLimiter{}
	_ cedar.Authorizer  = allowAuthorizer{}
)

// ─── cross-tenant minting ────────────────────────────────────────────────────

// ctxAsTenant pins the caller's tenant so the cross-tenant branch can be
// exercised in both directions.
func ctxAsTenant(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "admin", TenantID: tenant, Roles: roles,
	})
}

// Provisioning a new tenant's first service credential is the reason this RPC
// takes a tenant_id at all. RLS used to refuse the write; now the handler
// authorises it and the store scopes it.
func TestCreate_PlatformAdminMintsForAnotherTenant(t *testing.T) {
	caller, target := uuid.New(), uuid.New()
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, allowAuthorizer{})

	_, err := h.Create(ctxAsTenant(caller, "platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: target.String(), Name: "consumer-service", Audience: []string{"data"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iss.req.TenantID != target {
		t.Errorf("issued for tenant %v, want the requested %v", iss.req.TenantID, target)
	}
}

// ...and everyone else stays inside their own tenant, because the database is
// no longer the thing stopping them.
func TestCreate_NonPlatformAdminCannotMintForAnotherTenant(t *testing.T) {
	caller, target := uuid.New(), uuid.New()
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, allowAuthorizer{})

	_, err := h.Create(ctxAsTenant(caller, "tenant.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: target.String(), Name: "sneaky", Audience: []string{"data"},
	}))

	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
	if iss.req.Name != "" {
		t.Errorf("issuer was reached with %+v; the mint must not happen at all", iss.req)
	}
}

// A tenant-level admin minting for their OWN tenant is ordinary and must keep
// working — the new check must not turn every non-platform caller away.
func TestCreate_NonPlatformAdminMintsForOwnTenant(t *testing.T) {
	own := uuid.New()
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})

	_, err := h.Create(ctxAsTenant(own, "tenant.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: own.String(), Name: "own", Audience: []string{"data"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── roles on tokens ───────────────────────────────────────────────────────

// A token that could grant itself a role would make every other gate
// decorative — mint one with platform.admin and the tenant checks stop meaning
// anything. Granting is therefore platform-only, stricter than minting.
func TestCreate_GrantingRolesRequiresPlatformAdmin(t *testing.T) {
	own := uuid.New()
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, allowAuthorizer{})

	_, err := h.Create(ctxAsTenant(own, "tenant.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: own.String(), Name: "self-promotion", Audience: []string{"data"},
		Roles: []string{"platform.admin"},
	}))

	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
	if iss.req.Name != "" {
		t.Errorf("issuer was reached with %+v; the mint must not happen", iss.req)
	}
}

// A platform admin grants the narrow role, which is how a consumer gets one.
func TestCreate_PlatformAdminMayGrantTheNarrowRole(t *testing.T) {
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, allowAuthorizer{})

	_, err := h.Create(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: uuid.NewString(), Name: "consumer-issuer", Audience: []string{"admin"},
		Roles: []string{"platform.capability-issuer"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(iss.req.Roles) != 1 || iss.req.Roles[0] != "platform.capability-issuer" {
		t.Errorf("roles reached the issuer as %v", iss.req.Roles)
	}
}

// A roleless mint by a tenant-level caller is unaffected.
func TestCreate_NoRolesNeedsNoPlatformAdmin(t *testing.T) {
	own := uuid.New()
	h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})

	_, err := h.Create(ctxAsTenant(own, "tenant.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		TenantId: own.String(), Name: "ordinary", Audience: []string{"data"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

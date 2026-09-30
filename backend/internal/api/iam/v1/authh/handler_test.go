package authh

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

// stubMinter is the seam the issuer-interface extraction made testable.
type stubMinter struct {
	accessClaims issuer.AccessClaims
	calls        int
}

func (m *stubMinter) MintAccess(c issuer.AccessClaims) (string, time.Time, error) {
	m.accessClaims = c
	m.calls++
	return "access-tok", time.Unix(1_900_000_000, 0), nil
}
func (m *stubMinter) MintRefresh(issuer.RefreshClaims) (string, time.Time, error) {
	return "refresh-tok", time.Unix(1_900_003_600, 0), nil
}

// fakeUsers implements authstore.UserRepository; only the lookups + password
// mutation used by Login/ChangePassword carry behavior.
type fakeUsers struct {
	user        authstore.User
	err         error
	global      []authstore.User
	pwdHashSets int
	touched     int
}

func (f *fakeUsers) GetByID(context.Context, uuid.UUID) (authstore.User, error) {
	return f.user, f.err
}

// GetBySubject resolves within ONE tenant, as the real query does. Returning
// f.user regardless of tenant made this fake unable to express "not a member
// of that tenant" — the exact condition SwitchTenant asks it about.
func (f *fakeUsers) GetBySubject(_ context.Context, tenantID uuid.UUID, subject string) (authstore.User, error) {
	if f.err != nil {
		return authstore.User{}, f.err
	}
	for _, u := range f.global {
		if u.TenantID == tenantID && u.Subject == subject {
			return u, nil
		}
	}
	if f.user.TenantID == tenantID && f.user.Subject == subject {
		return f.user, nil
	}
	return authstore.User{}, authstore.ErrNotFound
}
func (f *fakeUsers) FindBySubjectGlobal(context.Context, string) ([]authstore.User, error) {
	return f.global, f.err
}

// ListMembershipsBySubject: for a fake, memberships and subject
// matches are the same set — the production cap that separates
// them is exactly what this does not model.
func (f *fakeUsers) ListMembershipsBySubject(ctx context.Context, subject string, _ time.Time, _ uuid.UUID, _ int32) ([]authstore.User, error) {
	return f.FindBySubjectGlobal(ctx, subject)
}
func (f *fakeUsers) Create(context.Context, authstore.User) (authstore.User, error) {
	return authstore.User{}, nil
}
func (f *fakeUsers) Update(context.Context, authstore.User, int64) (authstore.User, error) {
	return authstore.User{}, nil
}
func (f *fakeUsers) Delete(context.Context, uuid.UUID, int64) error { return nil }
func (f *fakeUsers) List(context.Context, authstore.ListUsersArgs) ([]authstore.User, string, error) {
	return nil, "", nil
}
func (f *fakeUsers) UpdatePasswordHash(context.Context, uuid.UUID, []byte) error {
	f.pwdHashSets++
	return nil
}
func (f *fakeUsers) TouchLogin(context.Context, uuid.UUID, time.Time) error { f.touched++; return nil }

type fakeRefresh struct {
	inserts        int
	revokeForUsers int
	revokeFamilies int
	getTok         authstore.RefreshToken
	getErr         error
	anyTok         authstore.RefreshToken
	anyErr         error
	superseded     []uuid.UUID
	// supersedeErr stands in for a conditional UPDATE that matched no row —
	// the database's verdict that this caller lost a rotation race.
	supersedeErr error
}

func (f *fakeRefresh) Insert(context.Context, authstore.RefreshToken) error { f.inserts++; return nil }
func (f *fakeRefresh) Get(context.Context, uuid.UUID) (authstore.RefreshToken, error) {
	return f.getTok, f.getErr
}
func (f *fakeRefresh) Revoke(context.Context, uuid.UUID) error { return nil }

// supersedes records rotation-supersession separately from revocation for
// cause — the distinction ExchangeAudience's grace window turns on.
func (f *fakeRefresh) Supersede(_ context.Context, jti uuid.UUID) error {
	if f.supersedeErr != nil {
		return f.supersedeErr
	}
	f.superseded = append(f.superseded, jti)
	return nil
}

// GetAny returns the row regardless of its revoked state. anyTok, when set,
// stands in for "the row as it is in the database now".
func (f *fakeRefresh) GetAny(context.Context, uuid.UUID) (authstore.RefreshToken, error) {
	if f.anyErr != nil {
		return authstore.RefreshToken{}, f.anyErr
	}
	return f.anyTok, nil
}
func (f *fakeRefresh) RevokeForUser(context.Context, uuid.UUID) (int64, error) {
	f.revokeForUsers++
	return 0, nil
}
func (f *fakeRefresh) RevokeFamilyOf(context.Context, uuid.UUID) (int64, error) {
	f.revokeFamilies++
	return 0, nil
}
func (f *fakeRefresh) PurgeExpired(context.Context, time.Time) (int64, error) { return 0, nil }

// stubDecoder implements RefreshTokenDecoder.
type stubDecoder struct {
	jti, uid, tid uuid.UUID
	err           error
}

func (d stubDecoder) DecodeRefresh(string) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	return d.jti, d.uid, d.tid, d.err
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// userWithPassword returns a user whose PasswordHash verifies against pw.
func userWithPassword(t *testing.T, pw string, roles ...string) authstore.User {
	t.Helper()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return authstore.User{
		UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1",
		PasswordHash: hash, Roles: roles,
	}
}

func newHandler(users authstore.UserRepository, refresh authstore.RefreshTokenRepository, iss tokenMinter) *Handler {
	return NewHandler(users, refresh, iss, stubDecoder{}, allowAuthorizer{})
}

// ─── Login ───────────────────────────────────────────────────────────────────

func TestLogin_InputValidation(t *testing.T) {
	h := newHandler(&fakeUsers{}, &fakeRefresh{}, &stubMinter{})
	if _, err := h.Login(context.Background(), LoginInput{Password: "x"}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("missing subject: code = %v, want InvalidArgument", code(err))
	}
	if _, err := h.Login(context.Background(), LoginInput{Subject: "u1"}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("missing password: code = %v, want InvalidArgument", code(err))
	}
}

func TestLogin_UnknownSubject(t *testing.T) {
	users := &fakeUsers{err: authstore.ErrNotFound}
	h := newHandler(users, &fakeRefresh{}, &stubMinter{})
	_, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "x", TenantHint: uuid.New()})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated (no existence leak)", code(err))
	}
}

func TestLogin_DisabledUser(t *testing.T) {
	u := userWithPassword(t, "pw")
	u.Disabled = true
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{})
	_, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "pw", TenantHint: u.TenantID})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestLogin_FederatedNoPassword(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"} // no hash
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{})
	_, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "pw", TenantHint: u.TenantID})
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", code(err))
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	u := userWithPassword(t, "correct")
	minter := &stubMinter{}
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, minter)
	_, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "wrong", TenantHint: u.TenantID})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
	if minter.calls != 0 {
		t.Error("no token must be minted on a failed password check")
	}
}

func TestLogin_AudienceEscalationDenied(t *testing.T) {
	// A plain tenant.user must not be able to mint a paladin-admin audience token.
	u := userWithPassword(t, "pw", "tenant.user")
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{})
	_, err := h.Login(context.Background(), LoginInput{
		Subject: "u1", Password: "pw", TenantHint: u.TenantID, RequestedAudience: auth.AudienceAdmin,
	})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (audience escalation)", code(err))
	}
}

func TestLogin_Success(t *testing.T) {
	u := userWithPassword(t, "pw", "tenant.user")
	users := &fakeUsers{user: u}
	refresh := &fakeRefresh{}
	minter := &stubMinter{}
	h := newHandler(users, refresh, minter)
	out, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "pw", TenantHint: u.TenantID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.AccessToken != "access-tok" || out.RefreshToken != "refresh-tok" {
		t.Errorf("tokens = %q / %q, want the minted pair", out.AccessToken, out.RefreshToken)
	}
	if refresh.inserts != 1 {
		t.Errorf("refresh row inserts = %d, want 1", refresh.inserts)
	}
	if users.touched != 1 {
		t.Errorf("TouchLogin calls = %d, want 1", users.touched)
	}
	// Access token minted for this user, defaulting to the data audience.
	if minter.accessClaims.Subject != u.UserID.String() || minter.accessClaims.Audience != auth.AudienceData {
		t.Errorf("minted claims mismatch: %+v", minter.accessClaims)
	}
}

// Multi-tenant subject, no hint: the password picks the memberships it
// opens and the most recently used one wins — the login form has no tenant
// field, so the old "supply X-Tenant-Id" InvalidArgument locked multi-tenant
// subjects out of the UI entirely. Re-scoping is the tenant switcher's job.
func TestLogin_MultiTenantSubject_MostRecentWins(t *testing.T) {
	older, newer := userWithPassword(t, "pw"), userWithPassword(t, "pw")
	t1 := time.Unix(1_700_000_000, 0)
	t2 := time.Unix(1_800_000_000, 0)
	older.LastLoginAt = &t1
	newer.LastLoginAt = &t2
	users := &fakeUsers{global: []authstore.User{older, newer}}
	minter := &stubMinter{}
	h := newHandler(users, &fakeRefresh{}, minter)

	out, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "pw"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if out.User.TenantID != newer.TenantID {
		t.Errorf("landed in tenant %s, want the most recently used %s", out.User.TenantID, newer.TenantID)
	}
	if minter.accessClaims.TenantID != newer.TenantID {
		t.Errorf("access token tenant = %s, want %s", minter.accessClaims.TenantID, newer.TenantID)
	}
}

func TestLogin_MultiTenantSubject_PasswordPicksTheMembership(t *testing.T) {
	// Same subject, DIFFERENT passwords per tenant: only the matching row
	// logs in — even when the non-matching one has a fresher last_login.
	match, other := userWithPassword(t, "right-pw"), userWithPassword(t, "other-pw")
	fresh := time.Unix(1_900_000_000, 0)
	other.LastLoginAt = &fresh
	users := &fakeUsers{global: []authstore.User{match, other}}
	h := newHandler(users, &fakeRefresh{}, &stubMinter{})

	out, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "right-pw"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if out.User.TenantID != match.TenantID {
		t.Errorf("landed in tenant %s, want the password-matching %s", out.User.TenantID, match.TenantID)
	}
}

func TestLogin_MultiTenantSubject_DisabledSkippedAndAllBadIsUnauthenticated(t *testing.T) {
	disabled := userWithPassword(t, "pw")
	disabled.Disabled = true
	federated := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"} // no hash
	users := &fakeUsers{global: []authstore.User{disabled, federated}}
	minter := &stubMinter{}
	h := newHandler(users, &fakeRefresh{}, minter)

	_, err := h.Login(context.Background(), LoginInput{Subject: "u1", Password: "pw"})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated (generic — no per-tenant detail leak)", code(err))
	}
	if minter.calls != 0 {
		t.Error("no token must be minted when no membership accepts the password")
	}
}

// ─── ChangePassword ──────────────────────────────────────────────────────────

func ctxAsUser(id uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: id.String()})
}

// ─── Memberships / SwitchTenant ───────────────────────────────────────────────

func TestListMyMemberships_Success(t *testing.T) {
	caller := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice", Roles: []string{"platform.admin"}}
	other := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice", Roles: []string{"tenant.user"}}
	users := &fakeUsers{user: caller, global: []authstore.User{caller, other}}
	h := newHandler(users, &fakeRefresh{}, &stubMinter{})

	ms, _, err := h.ListMyMemberships(ctxAsUser(caller.UserID), ListMembershipsInput{})
	if err != nil {
		t.Fatalf("ListMyMemberships: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("memberships = %d, want 2", len(ms))
	}
	var current int
	for _, m := range ms {
		if m.Current {
			current++
			if m.TenantID != caller.TenantID {
				t.Errorf("current membership = %s, want caller tenant %s", m.TenantID, caller.TenantID)
			}
		}
	}
	if current != 1 {
		t.Errorf("current memberships = %d, want exactly 1", current)
	}
}

func TestListMyMemberships_Unauthenticated(t *testing.T) {
	h := newHandler(&fakeUsers{}, &fakeRefresh{}, &stubMinter{})
	if _, _, err := h.ListMyMemberships(context.Background(), ListMembershipsInput{}); code(err) != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", code(err))
	}
}

func TestSwitchTenant_Success(t *testing.T) {
	caller := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice", Roles: []string{"tenant.user"}}
	targetTenant := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: targetTenant, Subject: "alice", Roles: []string{"platform.admin"}}
	users := &fakeUsers{user: caller, global: []authstore.User{caller, target}}
	refresh := &fakeRefresh{}
	minter := &stubMinter{}
	h := newHandler(users, refresh, minter)

	out, err := h.SwitchTenant(ctxAsUser(caller.UserID), targetTenant, "")
	if err != nil {
		t.Fatalf("SwitchTenant: %v", err)
	}
	if out.User.TenantID != targetTenant {
		t.Errorf("minted user tenant = %s, want %s", out.User.TenantID, targetTenant)
	}
	// The access token must be scoped to the TARGET tenant, not the caller's.
	if minter.accessClaims.TenantID != targetTenant {
		t.Errorf("access-token tenant = %s, want target %s", minter.accessClaims.TenantID, targetTenant)
	}
	if refresh.inserts != 1 {
		t.Errorf("refresh inserts = %d, want 1 (fresh family)", refresh.inserts)
	}
	if users.touched != 1 {
		t.Errorf("TouchLogin calls = %d, want 1", users.touched)
	}
}

func TestSwitchTenant_NotAMember(t *testing.T) {
	caller := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice"}
	users := &fakeUsers{user: caller, global: []authstore.User{caller}} // only the current tenant
	refresh := &fakeRefresh{}
	h := newHandler(users, refresh, &stubMinter{})

	if _, err := h.SwitchTenant(ctxAsUser(caller.UserID), uuid.New(), ""); code(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", code(err))
	}
	if refresh.inserts != 0 {
		t.Errorf("refresh inserts = %d, want 0", refresh.inserts)
	}
}

func TestSwitchTenant_DisabledInTarget(t *testing.T) {
	caller := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice"}
	targetTenant := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: targetTenant, Subject: "alice", Disabled: true}
	users := &fakeUsers{user: caller, global: []authstore.User{caller, target}}
	refresh := &fakeRefresh{}
	h := newHandler(users, refresh, &stubMinter{})

	if _, err := h.SwitchTenant(ctxAsUser(caller.UserID), targetTenant, ""); code(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", code(err))
	}
	if refresh.inserts != 0 {
		t.Errorf("refresh inserts = %d, want 0 (no token for disabled target)", refresh.inserts)
	}
}

func TestSwitchTenant_TargetRequired(t *testing.T) {
	caller := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice"}
	h := newHandler(&fakeUsers{user: caller}, &fakeRefresh{}, &stubMinter{})
	if _, err := h.SwitchTenant(ctxAsUser(caller.UserID), uuid.Nil, ""); code(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code(err))
	}
}

func TestChangePassword_NewRequired(t *testing.T) {
	id := uuid.New()
	h := newHandler(&fakeUsers{}, &fakeRefresh{}, &stubMinter{})
	if err := h.ChangePassword(ctxAsUser(id), "old", ""); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestChangePassword_WrongOld(t *testing.T) {
	u := userWithPassword(t, "real-old")
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{})
	err := h.ChangePassword(ctxAsUser(u.UserID), "guessed", "newpw")
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
}

func TestChangePassword_Success(t *testing.T) {
	u := userWithPassword(t, "old")
	users := &fakeUsers{user: u}
	refresh := &fakeRefresh{}
	h := newHandler(users, refresh, &stubMinter{})
	if err := h.ChangePassword(ctxAsUser(u.UserID), "old", "brand-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if users.pwdHashSets != 1 {
		t.Errorf("UpdatePasswordHash calls = %d, want 1", users.pwdHashSets)
	}
	if refresh.revokeForUsers != 1 {
		t.Error("changing the password must revoke all refresh tokens")
	}
}

// ─── assertAudienceAllowed (in-package) ──────────────────────────────────────

func TestAssertAudienceAllowed(t *testing.T) {
	h := &Handler{}
	user := authstore.User{Roles: []string{"tenant.user"}}
	admin := authstore.User{Roles: []string{"iam.admin"}}

	if err := h.assertAudienceAllowed(user, auth.AudienceData); err != nil {
		t.Errorf("data audience should be allowed: %v", err)
	}
	if err := h.assertAudienceAllowed(user, auth.AudienceIAM); err != nil {
		t.Errorf("iam audience should be allowed: %v", err)
	}
	if err := h.assertAudienceAllowed(user, auth.AudienceAdmin); code(err) != connect.CodePermissionDenied {
		t.Errorf("tenant.user → admin audience: code = %v, want PermissionDenied", code(err))
	}
	if err := h.assertAudienceAllowed(admin, auth.AudienceAdmin); err != nil {
		t.Errorf("admin role → admin audience should pass: %v", err)
	}
	if err := h.assertAudienceAllowed(user, "paladin-bogus"); code(err) != connect.CodeInvalidArgument {
		t.Errorf("unknown audience: code = %v, want InvalidArgument", code(err))
	}
}

// ─── RefreshToken ────────────────────────────────────────────────────────────

func TestRefreshToken_EmptyToken(t *testing.T) {
	h := newHandler(&fakeUsers{}, &fakeRefresh{}, &stubMinter{})
	if _, err := h.RefreshToken(context.Background(), RefreshInput{}); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestRefreshToken_DecoderError(t *testing.T) {
	h := NewHandler(&fakeUsers{}, &fakeRefresh{}, &stubMinter{},
		stubDecoder{err: errors.New("bad signature")}, allowAuthorizer{})
	_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "garbage"})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated (decode failure)", code(err))
	}
}

// ─── Spent-token handling: destructive on rotation, not on exchange ─────────

// A spent refresh token reaching the two paths must be answered differently.
// Rotation is the call an attacker has to make to obtain a usable token, and a
// second presentation there means two holders — revoke the family. Exchange
// consumes nothing, so a token a concurrent rotation spent a millisecond
// earlier arrives routinely (the console's BFF fires /me and its audience
// exchanges together on one cookie); revoking there took the live successor
// with it and signed the operator out over a race.

func revokedTokenHandler(t *testing.T) (*Handler, *fakeRefresh) {
	t.Helper()
	uid, tid := uuid.New(), uuid.New()
	refresh := &fakeRefresh{getErr: authstore.ErrTokenRevoked}
	users := &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid, Subject: "u1"}}
	h := NewHandler(users, refresh, &stubMinter{},
		stubDecoder{jti: uuid.New(), uid: uid, tid: tid}, allowAuthorizer{})
	return h, refresh
}

func TestExchangeAudience_SpentTokenRejectedWithoutRevokingTheFamily(t *testing.T) {
	h, refresh := revokedTokenHandler(t)

	_, err := h.ExchangeAudience(context.Background(), ExchangeAudienceInput{
		RefreshToken: "spent", TargetAudience: auth.AudienceAdmin,
	})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
	// The point of the change: the caller is refused and everything else
	// survives. A revocation here would take the successor the legitimate
	// holder is already using.
	if refresh.revokeFamilies != 0 {
		t.Errorf("RevokeFamilyOf calls = %d, want 0 — exchange consumes nothing, so a spent token is a race, not a theft signal", refresh.revokeFamilies)
	}
	if refresh.revokeForUsers != 0 {
		t.Errorf("RevokeForUser calls = %d, want 0", refresh.revokeForUsers)
	}
}

func TestRefreshToken_SpentTokenStillRevokesTheFamily(t *testing.T) {
	h, refresh := revokedTokenHandler(t)

	_, err := h.RefreshToken(context.Background(), RefreshInput{RefreshToken: "spent"})
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
	// Guards the half that must NOT move: RFC 6819 containment lives on the
	// consuming path, and softening exchange must not soften this one.
	if refresh.revokeFamilies != 1 {
		t.Errorf("RevokeFamilyOf calls = %d, want 1 — rotation keeps full reuse detection", refresh.revokeFamilies)
	}
}

// ─── WhoAmI route table (ADR-0014 Phase 4) ──────────────────────────────────

type stubRouteLister struct {
	routes   []CollectionRoute
	next     string // next_page_token to return (non-empty ⇒ RoutesTruncated)
	err      error
	calls    int
	gotTID   uuid.UUID
	gotToken string
}

func (s *stubRouteLister) ListCollectionRoutes(_ context.Context, tid uuid.UUID, pageToken string) ([]CollectionRoute, string, error) {
	s.calls++
	s.gotTID = tid
	s.gotToken = pageToken
	return s.routes, s.next, s.err
}

// whoAmICtx builds a context carrying a principal whose subject is the given
// user id, as the auth middleware would install post-authn.
func whoAmICtx(u authstore.User) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject:  u.UserID.String(),
		TenantID: u.TenantID,
		Audience: auth.AudienceIAM,
	})
}

func TestWhoAmI_NoRouteListerReturnsIdentityOnly(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"}
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{})
	out, err := h.WhoAmI(whoAmICtx(u), "")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if len(out.Routes) != 0 {
		t.Fatalf("routes = %d, want 0 when no lister wired", len(out.Routes))
	}
}

func TestWhoAmI_ReturnsRoutesFromLister(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"}
	want := []CollectionRoute{{
		Canonical:  "storageBackends/primary/buckets/paladin/tenants/" + u.TenantID.String() + "/collections/invoices",
		TenantPath: "tenants/" + u.TenantID.String() + "/collections/invoices",
		BareAlias:  "invoices",
		Backend:    "primary",
		Bucket:     "paladin",
	}}
	lister := &stubRouteLister{routes: want}
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{}).WithCollectionRoutes(lister)

	out, err := h.WhoAmI(whoAmICtx(u), "")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if lister.calls != 1 || lister.gotTID != u.TenantID {
		t.Fatalf("lister calls=%d tid=%s, want 1 call for tenant %s", lister.calls, lister.gotTID, u.TenantID)
	}
	if len(out.Routes) != 1 || out.Routes[0] != want[0] {
		t.Fatalf("routes = %+v, want %+v", out.Routes, want)
	}
	if out.RoutesTruncated {
		t.Error("RoutesTruncated should be false for a complete table")
	}
}

func TestWhoAmI_PropagatesRoutesTruncated(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"}
	lister := &stubRouteLister{
		routes: []CollectionRoute{{Canonical: "c", TenantPath: "t", Backend: "b", Bucket: "bk"}},
		next:   "cursor-page-2",
	}
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{}).WithCollectionRoutes(lister)

	out, err := h.WhoAmI(whoAmICtx(u), "")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if !out.RoutesTruncated {
		t.Error("RoutesTruncated should propagate from the lister")
	}
	if out.NextPageToken != "cursor-page-2" {
		t.Errorf("NextPageToken = %q, want %q", out.NextPageToken, "cursor-page-2")
	}
}

// TestWhoAmI_ForwardsRoutePageToken proves the caller's page token reaches the
// lister and a second page (empty next) marks the table exhausted — the full
// page-through path (ADR-0014 Phase 4 DoD option a).
func TestWhoAmI_ForwardsRoutePageToken(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"}
	lister := &stubRouteLister{
		routes: []CollectionRoute{{Canonical: "c2", TenantPath: "t2", Backend: "b", Bucket: "bk"}},
		next:   "", // last page
	}
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{}).WithCollectionRoutes(lister)

	out, err := h.WhoAmI(whoAmICtx(u), "cursor-page-2")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if lister.gotToken != "cursor-page-2" {
		t.Errorf("lister got token %q, want the forwarded %q", lister.gotToken, "cursor-page-2")
	}
	if out.RoutesTruncated || out.NextPageToken != "" {
		t.Errorf("last page must not be truncated: truncated=%v next=%q", out.RoutesTruncated, out.NextPageToken)
	}
}

func TestWhoAmI_RouteListerErrorDegradesToEmpty(t *testing.T) {
	// A route-source failure (incl. a Cedar denial) must not fail WhoAmI —
	// identity still returns, just without routes.
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"}
	lister := &stubRouteLister{err: connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))}
	h := newHandler(&fakeUsers{user: u}, &fakeRefresh{}, &stubMinter{}).WithCollectionRoutes(lister)

	out, err := h.WhoAmI(whoAmICtx(u), "")
	if err != nil {
		t.Fatalf("WhoAmI should not fail on route error: %v", err)
	}
	if len(out.Routes) != 0 {
		t.Fatalf("routes = %d, want 0 on lister error", len(out.Routes))
	}
}

// Compile-time interface assertions — including the new minter seam.
var (
	_ tokenMinter                      = (*stubMinter)(nil)
	_ authstore.UserRepository         = (*fakeUsers)(nil)
	_ authstore.RefreshTokenRepository = (*fakeRefresh)(nil)
	_ RefreshTokenDecoder              = stubDecoder{}
	_ cedar.Authorizer                 = allowAuthorizer{}
	_ CollectionRouteLister            = (*stubRouteLister)(nil)
)

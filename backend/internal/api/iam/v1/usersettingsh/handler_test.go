package usersettingsh

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin-private/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin-private/internal/auth/store"
	"github.com/oleg-tkachuk/paladin-private/internal/policy/cedar"
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

// fakeRepo implements usersettingsh.Repository.
type fakeRepo struct {
	get         Settings
	getErr      error
	upserted    Settings
	upsertErr   error
	listErr     error
	deleteCalls int
	deleteErr   error
}

func (f *fakeRepo) Get(context.Context, uuid.UUID) (Settings, error) { return f.get, f.getErr }
func (f *fakeRepo) Upsert(_ context.Context, s Settings) (Settings, error) {
	f.upserted = s
	if f.upsertErr != nil {
		return Settings{}, f.upsertErr
	}
	return s, nil
}
func (f *fakeRepo) ListByTenant(context.Context, uuid.UUID, int32) ([]Settings, error) {
	return nil, f.listErr
}
func (f *fakeRepo) Delete(context.Context, uuid.UUID) error {
	f.deleteCalls++
	return f.deleteErr
}

// fakeUsers implements authstore.UserRepository; only GetByID/GetBySubject are
// exercised here.
type fakeUsers struct {
	user authstore.User
	err  error
}

func (f *fakeUsers) GetByID(context.Context, uuid.UUID) (authstore.User, error) {
	return f.user, f.err
}
func (f *fakeUsers) GetBySubject(context.Context, uuid.UUID, string) (authstore.User, error) {
	return f.user, f.err
}
func (f *fakeUsers) Create(context.Context, authstore.User) (authstore.User, error) {
	return authstore.User{}, nil
}
func (f *fakeUsers) FindBySubjectGlobal(context.Context, string) ([]authstore.User, error) {
	return nil, nil
}
func (f *fakeUsers) Update(context.Context, authstore.User, int64) (authstore.User, error) {
	return authstore.User{}, nil
}
func (f *fakeUsers) Delete(context.Context, uuid.UUID, int64) error { return nil }
func (f *fakeUsers) List(context.Context, authstore.ListUsersArgs) ([]authstore.User, string, error) {
	return nil, "", nil
}
func (f *fakeUsers) UpdatePasswordHash(context.Context, uuid.UUID, []byte) error { return nil }
func (f *fakeUsers) TouchLogin(context.Context, uuid.UUID, time.Time) error      { return nil }

func code(err error) connect.Code { return connect.CodeOf(err) }

// ctxAsUser sets sub=userID so subjectToUserID's UUID fast-path resolves.
func ctxAsUser(userID, tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: userID.String(), TenantID: tenant, Roles: roles,
	})
}

// ─── GetMine ─────────────────────────────────────────────────────────────────

func TestGetMine_DefaultsWhenNoRow(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	repo := &fakeRepo{getErr: ErrNotFound}
	users := &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}
	h := NewHandler(repo, users, allowAuthorizer{})
	got, err := h.GetMine(ctxAsUser(uid, tid))
	if err != nil {
		t.Fatalf("GetMine should never NotFound for a fresh user, got %v", err)
	}
	if got.Timezone != "UTC" {
		t.Errorf("default timezone = %q, want UTC", got.Timezone)
	}
}

func TestGetMine_ReturnsStoredRow(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	repo := &fakeRepo{get: Settings{UserID: uid, TenantID: tid, Timezone: "Europe/Kyiv"}}
	users := &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}
	h := NewHandler(repo, users, allowAuthorizer{})
	got, err := h.GetMine(ctxAsUser(uid, tid))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Timezone != "Europe/Kyiv" {
		t.Errorf("got %q, want the stored timezone", got.Timezone)
	}
}

// ─── UpdateMine ──────────────────────────────────────────────────────────────

func TestUpdateMine_AppliesMaskAndUpserts(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	repo := &fakeRepo{getErr: ErrNotFound} // start from defaults
	users := &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}
	h := NewHandler(repo, users, allowAuthorizer{})
	_, err := h.UpdateMine(ctxAsUser(uid, tid), UpdateMineInput{
		UpdateMask: []string{"theme"}, Theme: "dark",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.upserted.Theme != "dark" {
		t.Errorf("upserted theme = %q, want dark", repo.upserted.Theme)
	}
	// Untouched fields keep their default.
	if repo.upserted.Timezone != "UTC" {
		t.Errorf("timezone should keep its default UTC, got %q", repo.upserted.Timezone)
	}
}

func TestUpdateMine_UnknownMaskField(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	repo := &fakeRepo{getErr: ErrNotFound}
	users := &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}
	h := NewHandler(repo, users, allowAuthorizer{})
	_, err := h.UpdateMine(ctxAsUser(uid, tid), UpdateMineInput{
		UpdateMask: []string{"nope"},
	})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestUpdateMine_OversizedPreferencesRejected(t *testing.T) {
	uid, tid := uuid.New(), uuid.New()
	repo := &fakeRepo{getErr: ErrNotFound}
	users := &fakeUsers{user: authstore.User{UserID: uid, TenantID: tid}}
	h := NewHandler(repo, users, allowAuthorizer{})
	big := append([]byte(`{"x":"`), bytes.Repeat([]byte("a"), preferencesCap)...)
	big = append(big, []byte(`"}`)...)
	_, err := h.UpdateMine(ctxAsUser(uid, tid), UpdateMineInput{
		UpdateMask: []string{"preferences"}, Preferences: big,
	})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (oversized preferences)", code(err))
	}
}

// ─── GetForUser ──────────────────────────────────────────────────────────────

func TestGetForUser_UserNotFound(t *testing.T) {
	h := NewHandler(&fakeRepo{}, &fakeUsers{err: authstore.ErrNotFound}, allowAuthorizer{})
	_, err := h.GetForUser(ctxAsUser(uuid.New(), uuid.New(), apiutil.RolePlatformAdmin), uuid.New())
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGetForUser_CrossTenantHidden(t *testing.T) {
	tid := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: uuid.New()} // other tenant
	h := NewHandler(&fakeRepo{}, &fakeUsers{user: target}, allowAuthorizer{})
	_, err := h.GetForUser(ctxAsUser(uuid.New(), tid, apiutil.RoleTenantAdmin), target.UserID)
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound (cross-tenant hidden)", code(err))
	}
}

func TestGetForUser_CedarDenied(t *testing.T) {
	tid := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: tid}
	h := NewHandler(&fakeRepo{getErr: ErrNotFound}, &fakeUsers{user: target}, denyAuthorizer{})
	_, err := h.GetForUser(ctxAsUser(uuid.New(), tid, apiutil.RoleTenantAdmin), target.UserID)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestGetForUser_SettingsDefaultWhenMissing(t *testing.T) {
	tid := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: tid}
	h := NewHandler(&fakeRepo{getErr: ErrNotFound}, &fakeUsers{user: target}, allowAuthorizer{})
	got, err := h.GetForUser(ctxAsUser(uuid.New(), tid, apiutil.RolePlatformAdmin), target.UserID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Timezone != "UTC" {
		t.Errorf("missing settings should serve defaults, got tz %q", got.Timezone)
	}
}

// ─── ListByTenant ────────────────────────────────────────────────────────────

func TestListByTenant_CrossTenantDenied(t *testing.T) {
	h := NewHandler(&fakeRepo{}, &fakeUsers{}, allowAuthorizer{})
	_, err := h.ListByTenant(ctxAsUser(uuid.New(), uuid.New(), apiutil.RoleTenantAdmin), uuid.New(), 50)
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestListByTenant_PlatformAdminOK(t *testing.T) {
	h := NewHandler(&fakeRepo{}, &fakeUsers{}, allowAuthorizer{})
	_, err := h.ListByTenant(ctxAsUser(uuid.New(), uuid.New(), apiutil.RolePlatformAdmin), uuid.New(), 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── DeleteForUser ───────────────────────────────────────────────────────────

func TestDeleteForUser_CrossTenantHidden(t *testing.T) {
	tid := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	repo := &fakeRepo{}
	h := NewHandler(repo, &fakeUsers{user: target}, allowAuthorizer{})
	err := h.DeleteForUser(ctxAsUser(uuid.New(), tid, apiutil.RoleTenantAdmin), target.UserID)
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
	if repo.deleteCalls != 0 {
		t.Error("Delete must not run when the target is hidden")
	}
}

func TestDeleteForUser_Success(t *testing.T) {
	tid := uuid.New()
	target := authstore.User{UserID: uuid.New(), TenantID: tid}
	repo := &fakeRepo{}
	h := NewHandler(repo, &fakeUsers{user: target}, allowAuthorizer{})
	if err := h.DeleteForUser(ctxAsUser(uuid.New(), tid, apiutil.RolePlatformAdmin), target.UserID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.deleteCalls != 1 {
		t.Errorf("Delete called %d times, want 1", repo.deleteCalls)
	}
}

// ─── Pure helpers ────────────────────────────────────────────────────────────

func TestApplyMask_UnknownField(t *testing.T) {
	s := DefaultsFor(uuid.New(), uuid.New())
	if err := applyMask(&s, UpdateMineInput{UpdateMask: []string{"bogus"}}); err == nil {
		t.Error("applyMask should reject an unknown field")
	}
}

func TestValidate(t *testing.T) {
	ok := DefaultsFor(uuid.New(), uuid.New())
	if err := validate(ok); err != nil {
		t.Errorf("defaults should validate, got %v", err)
	}
	bad := ok
	bad.Preferences = []byte(strings.Repeat("x", preferencesCap+1))
	if err := validate(bad); err == nil {
		t.Error("validate should reject oversized preferences")
	}
	invalidJSON := ok
	invalidJSON.Preferences = []byte("{not json")
	if err := validate(invalidJSON); err == nil {
		t.Error("validate should reject non-JSON preferences")
	}
}

// Compile-time interface assertions.
var (
	_ Repository               = (*fakeRepo)(nil)
	_ authstore.UserRepository = (*fakeUsers)(nil)
	_ cedar.Authorizer         = allowAuthorizer{}
)

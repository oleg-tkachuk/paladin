package userh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// fakeUserRepo implements authstore.UserRepository. `user` is what GetByID
// returns; Create/Update capture their argument so the tests can assert the
// hash/mask/scope mutations the handler applies.
type fakeUserRepo struct {
	user        authstore.User
	getErr      error
	createErr   error
	created     authstore.User
	updateErr   error
	updated     authstore.User
	deleteErr   error
	pwdHashSets int
}

func (f *fakeUserRepo) Create(_ context.Context, u authstore.User) (authstore.User, error) {
	f.created = u
	if f.createErr != nil {
		return authstore.User{}, f.createErr
	}
	u.UserID = uuid.New()
	return u, nil
}
func (f *fakeUserRepo) GetByID(context.Context, uuid.UUID) (authstore.User, error) {
	return f.user, f.getErr
}
func (f *fakeUserRepo) GetBySubject(context.Context, uuid.UUID, string) (authstore.User, error) {
	return authstore.User{}, authstore.ErrNotFound
}
func (f *fakeUserRepo) FindBySubjectGlobal(context.Context, string) ([]authstore.User, error) {
	return nil, nil
}

// ListMembershipsBySubject: for a fake, memberships and subject
// matches are the same set — the production cap that separates
// them is exactly what this does not model.
func (f *fakeUserRepo) ListMembershipsBySubject(ctx context.Context, subject string, _ time.Time, _ uuid.UUID, _ int32) ([]authstore.User, error) {
	return f.FindBySubjectGlobal(ctx, subject)
}
func (f *fakeUserRepo) Update(_ context.Context, u authstore.User, _ int64) (authstore.User, error) {
	f.updated = u
	if f.updateErr != nil {
		return authstore.User{}, f.updateErr
	}
	return u, nil
}
func (f *fakeUserRepo) Delete(context.Context, uuid.UUID, int64) error { return f.deleteErr }
func (f *fakeUserRepo) List(context.Context, authstore.ListUsersArgs) ([]authstore.User, string, error) {
	return nil, "", nil
}
func (f *fakeUserRepo) UpdatePasswordHash(context.Context, uuid.UUID, []byte) error {
	f.pwdHashSets++
	return nil
}
func (f *fakeUserRepo) TouchLogin(context.Context, uuid.UUID, time.Time) error { return nil }

func ctxAs(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "admin", TenantID: tenant, Roles: roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// ─── CreateUser ──────────────────────────────────────────────────────────────

func TestCreateUser_CrossTenantDenied(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeUserRepo{}, allowAuthorizer{})
	_, err := h.CreateUser(ctxAs(caller, apiutil.RoleTenantAdmin), CreateUserInput{
		TenantID: uuid.New(), Subject: "u1", InitialPassword: "pw",
	})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestCreateUser_SubjectRequired(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeUserRepo{}, allowAuthorizer{})
	_, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), CreateUserInput{
		TenantID: caller, Subject: "", InitialPassword: "pw",
	})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

// Input is checked before Cedar. With an empty subject the request names no
// user, so authorizing it first evaluated ManageUser against the Tenant and a
// denying policy answered PermissionDenied for what is a malformed request.
func TestCreateUser_InvalidInputIsRejectedBeforeAuthorization(t *testing.T) {
	caller := uuid.New()
	for name, in := range map[string]CreateUserInput{
		"no subject":  {TenantID: caller, Subject: "", InitialPassword: "pw"},
		"no password": {TenantID: caller, Subject: "u1", InitialPassword: ""},
	} {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(&fakeUserRepo{}, denyAuthorizer{})
			_, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), in)
			if code(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", code(err))
			}
		})
	}
}

func TestCreateUser_PasswordRequired(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeUserRepo{}, allowAuthorizer{})
	_, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), CreateUserInput{
		TenantID: caller, Subject: "u1", InitialPassword: "",
	})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestCreateUser_SubjectTakenMapped(t *testing.T) {
	caller := uuid.New()
	repo := &fakeUserRepo{createErr: authstore.ErrSubjectTaken}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), CreateUserInput{
		TenantID: caller, Subject: "u1", InitialPassword: "pw",
	})
	if code(err) != connect.CodeAlreadyExists {
		t.Fatalf("code = %v, want AlreadyExists", code(err))
	}
}

func TestCreateUser_SuccessHashesPassword(t *testing.T) {
	caller := uuid.New()
	repo := &fakeUserRepo{}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), CreateUserInput{
		TenantID: caller, Subject: "u1", InitialPassword: "s3cret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.created.PasswordHash) == 0 {
		t.Error("password should be hashed before persistence, not stored cleartext")
	}
	if string(repo.created.PasswordHash) == "s3cret" {
		t.Error("password hash equals the plaintext — not hashed")
	}
}

func TestCreateUser_CedarDenied(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeUserRepo{}, denyAuthorizer{})
	_, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), CreateUserInput{
		TenantID: caller, Subject: "u1", InitialPassword: "pw",
	})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

// ─── GetUser ─────────────────────────────────────────────────────────────────

func TestGetUser_NotFound(t *testing.T) {
	h := NewHandler(&fakeUserRepo{getErr: authstore.ErrNotFound}, allowAuthorizer{})
	_, err := h.GetUser(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New())
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGetUser_CrossTenantHidden(t *testing.T) {
	caller := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{UserID: uuid.New(), TenantID: uuid.New()}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.GetUser(ctxAs(caller, apiutil.RoleTenantAdmin), repo.user.UserID)
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound (cross-tenant hidden)", code(err))
	}
}

func TestGetUser_Success(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{UserID: id, TenantID: caller, Subject: "u1"}}
	h := NewHandler(repo, allowAuthorizer{})
	got, err := h.GetUser(ctxAs(caller, apiutil.RoleTenantAdmin), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Subject != "u1" {
		t.Errorf("got subject %q, want u1", got.Subject)
	}
}

// ─── UpdateUser ──────────────────────────────────────────────────────────────

func TestUpdateUser_CrossTenantDenied(t *testing.T) {
	caller := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{UserID: uuid.New(), TenantID: uuid.New()}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.UpdateUser(ctxAs(caller, apiutil.RoleTenantAdmin),
		UpdateUserInput{UserID: repo.user.UserID})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestUpdateUser_MaskAppliesDisplayName(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{UserID: id, TenantID: caller, DisplayName: "old"}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.UpdateUser(ctxAs(caller, apiutil.RolePlatformAdmin), UpdateUserInput{
		UserID: id, UpdateMask: []string{"display_name"}, DisplayName: "new",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.updated.DisplayName != "new" {
		t.Errorf("persisted display_name = %q, want new", repo.updated.DisplayName)
	}
}

func TestUpdateUser_VersionMismatchAborts(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeUserRepo{
		user:      authstore.User{UserID: id, TenantID: caller},
		updateErr: authstore.ErrVersionMismatch,
	}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.UpdateUser(ctxAs(caller, apiutil.RolePlatformAdmin),
		UpdateUserInput{UserID: id, ExpectedVersion: 1})
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── DeleteUser ──────────────────────────────────────────────────────────────

func TestDeleteUser_VersionMismatchAborts(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeUserRepo{
		user:      authstore.User{UserID: id, TenantID: caller},
		deleteErr: authstore.ErrVersionMismatch,
	}
	h := NewHandler(repo, allowAuthorizer{})
	if err := h.DeleteUser(ctxAs(caller, apiutil.RolePlatformAdmin), id, 1); code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── GrantScopes ─────────────────────────────────────────────────────────────

func TestGrantScopes_NotFound(t *testing.T) {
	h := NewHandler(&fakeUserRepo{getErr: authstore.ErrNotFound}, allowAuthorizer{})
	_, err := h.GrantScopes(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New(),
		[]auth.Scope{{Type: auth.ScopeBucket, Value: "b1"}})
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGrantScopes_MergesAndPersists(t *testing.T) {
	id := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{
		UserID: id, TenantID: uuid.New(),
		Scopes: []auth.Scope{{Type: auth.ScopeTenant, Value: "t1"}},
	}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.GrantScopes(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), id,
		[]auth.Scope{{Type: auth.ScopeBucket, Value: "b1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.updated.Scopes) != 2 {
		t.Errorf("scopes after grant = %d, want 2 (merged)", len(repo.updated.Scopes))
	}
}

// ─── ResetPassword ───────────────────────────────────────────────────────────

func TestResetPassword_GeneratesWhenEmpty(t *testing.T) {
	id := uuid.New()
	repo := &fakeUserRepo{user: authstore.User{UserID: id, TenantID: uuid.New()}}
	h := NewHandler(repo, allowAuthorizer{})
	pw, err := h.ResetPassword(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), id, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pw == "" {
		t.Error("empty input should yield a generated password")
	}
	if repo.pwdHashSets != 1 {
		t.Errorf("UpdatePasswordHash called %d times, want 1", repo.pwdHashSets)
	}
}

// Compile-time interface assertions.
var (
	_ authstore.UserRepository = (*fakeUserRepo)(nil)
	_ cedar.Authorizer         = allowAuthorizer{}
)

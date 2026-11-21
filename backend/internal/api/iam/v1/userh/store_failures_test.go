package userh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// errStoreDown is a store failing for a reason of its own.
var errStoreDown = errors.New("connection reset")

// failingPasswordRepo fails UpdatePasswordHash with err.
type failingPasswordRepo struct {
	fakeUserRepo
	err error
}

func (f *failingPasswordRepo) UpdatePasswordHash(context.Context, uuid.UUID, []byte) error {
	return f.err
}

// observed is a handler over repo whose log lines are recorded.
func observed(repo authstore.UserRepository) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewHandler(repo, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

// A user that cannot be read because the store fails is the server's fault,
// not a user that is not there: every RPC that loads one used to answer
// NOT_FOUND, telling an operator the user was gone while the database was.
func TestAStoreFailingToLoadTheUserIsInternal(t *testing.T) {
	tenant := uuid.New()
	target := UserRef{TenantID: tenant, UserID: uuid.New()}
	ctx := ctxAs(tenant, apiutil.RolePlatformAdmin)
	rpcs := map[string]func(h *Handler) error{
		"GetUser":    func(h *Handler) error { _, err := h.GetUser(ctx, target); return err },
		"UpdateUser": func(h *Handler) error { _, err := h.UpdateUser(ctx, UpdateUserInput{User: target}); return err },
		"DeleteUser": func(h *Handler) error { return h.DeleteUser(ctx, target, 0) },
		"GrantScopes": func(h *Handler) error {
			_, err := h.GrantScopes(ctx, target, nil)
			return err
		},
		"RevokeScopes": func(h *Handler) error {
			_, err := h.RevokeScopes(ctx, target, nil)
			return err
		},
		"ResetPassword": func(h *Handler) error { _, err := h.ResetPassword(ctx, target, "pw"); return err },
		"ListOAuthGrants": func(h *Handler) error {
			_, err := h.WithOAuthGrants(&fakeGrants{}).ListOAuthGrants(ctx, target, false)
			return err
		},
	}
	for name, call := range rpcs {
		t.Run(name, func(t *testing.T) {
			h, logs := observed(&fakeUserRepo{getErr: errStoreDown})
			if err := call(h); code(err) != connect.CodeInternal {
				t.Fatalf("code = %v, want Internal", code(err))
			}
			if logs.FilterLevelExact(zapcore.ErrorLevel).FilterField(zap.String("user_id", target.UserID.String())).Len() != 1 {
				t.Errorf("want one error line naming the user, got %v", logs.All())
			}
		})
	}
	t.Run("a user that is not there is still not found", func(t *testing.T) {
		h, logs := observed(&fakeUserRepo{getErr: authstore.ErrNotFound})
		if err := h.DeleteUser(ctx, target, 0); code(err) != connect.CodeNotFound {
			t.Fatalf("code = %v, want NotFound", code(err))
		}
		if logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
			t.Errorf("want one warn line, got %v", logs.All())
		}
	})
}

// A scope change that loses its write answers what the store says: a user
// removed meanwhile is NOT_FOUND, not INTERNAL.
func TestScopeChangesMapTheStoreAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"user removed", authstore.ErrNotFound, connect.CodeNotFound},
		{"changed meanwhile", authstore.ErrVersionMismatch, connect.CodeAborted},
		{"store failing", errStoreDown, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
			h := NewHandler(&fakeUserRepo{user: u, updateErr: tc.err}, allowAuthorizer{})
			ctx := ctxAs(u.TenantID, apiutil.RolePlatformAdmin)
			scopes := []auth.Scope{{Type: auth.ScopeBucket, Value: "b"}}
			if _, err := h.GrantScopes(ctx, ref(u), scopes); code(err) != tc.want {
				t.Errorf("grant: code = %v, want %v", code(err), tc.want)
			}
			if _, err := h.RevokeScopes(ctx, ref(u), scopes); code(err) != tc.want {
				t.Errorf("revoke: code = %v, want %v", code(err), tc.want)
			}
		})
	}
}

func TestResetPasswordMapsTheStoreAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"user removed", authstore.ErrNotFound, connect.CodeNotFound},
		{"store failing", errStoreDown, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
			h := NewHandler(&failingPasswordRepo{fakeUserRepo: fakeUserRepo{user: u}, err: tc.err}, allowAuthorizer{})
			pw, err := h.ResetPassword(ctxAs(u.TenantID, apiutil.RolePlatformAdmin), ref(u), "")
			if code(err) != tc.want || pw != "" {
				t.Errorf("code = %v password %q, want %v and none", code(err), pw, tc.want)
			}
		})
	}
}

// A password the server cannot generate is a failure, never a guessable
// stand-in: it used to fall back to the current time.
func TestResetPasswordRefusesWhenNoPasswordCanBeGenerated(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	repo := &fakeUserRepo{user: u}
	h, logs := observed(repo)
	h.newPassword = func() (string, error) { return "", errStoreDown }
	pw, err := h.ResetPassword(ctxAs(u.TenantID, apiutil.RolePlatformAdmin), ref(u), "")
	if code(err) != connect.CodeInternal || pw != "" {
		t.Fatalf("code = %v password %q, want Internal and none", code(err), pw)
	}
	if repo.pwdHashSets != 0 {
		t.Errorf("a password was stored %d times, want none", repo.pwdHashSets)
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Errorf("want one error line, got %v", logs.All())
	}
}

func TestAPasswordTooLongIsTheCallersToFix(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	h := NewHandler(&fakeUserRepo{user: u}, allowAuthorizer{})
	long := strings.Repeat("x", 100)
	if _, err := h.ResetPassword(ctxAs(u.TenantID, apiutil.RolePlatformAdmin), ref(u), long); code(err) != connect.CodeInvalidArgument {
		t.Errorf("reset: code = %v, want InvalidArgument", code(err))
	}
	if _, err := h.CreateUser(ctxAs(u.TenantID, apiutil.RolePlatformAdmin), CreateUserInput{Subject: "s", InitialPassword: long}); code(err) != connect.CodeInvalidArgument {
		t.Errorf("create: code = %v, want InvalidArgument", code(err))
	}
}

func TestPasswordHashAnswer(t *testing.T) {
	if got := hashAnswer(bcrypt.ErrPasswordTooLong); code(got) != connect.CodeInvalidArgument {
		t.Errorf("too long: code = %v, want InvalidArgument", code(got))
	}
	if got := hashAnswer(errStoreDown); code(got) != connect.CodeInternal {
		t.Errorf("other: code = %v, want Internal", code(got))
	}
}

func TestListUsersMapsTheStoreAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"a page token it did not mint", fmt.Errorf("%w: bad uuid", authstore.ErrInvalidPageToken), connect.CodeInvalidArgument},
		{"store failing", errStoreDown, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := uuid.New()
			h := NewHandler(&listingRepo{listErr: tc.err}, allowAuthorizer{})
			if _, _, err := h.ListUsers(ctxAs(caller, apiutil.RoleTenantAdmin), ListUsersInput{}); code(err) != tc.want {
				t.Errorf("code = %v, want %v", code(err), tc.want)
			}
		})
	}
}

// Every change is logged with whose it was and who made it, and never with a
// password or its hash.
func TestUserChangesAreLoggedWithoutSecrets(t *testing.T) {
	tenant := uuid.New()
	u := authstore.User{UserID: uuid.New(), TenantID: tenant, PasswordHash: []byte("stored-hash")}
	ctx := ctxAs(tenant, apiutil.RolePlatformAdmin)
	const secret = "s3cret-pass"
	steps := map[string]func(h *Handler) error{
		opCreate: func(h *Handler) error {
			_, err := h.CreateUser(ctx, CreateUserInput{Subject: "s", InitialPassword: secret})
			return err
		},
		opUpdate:       func(h *Handler) error { _, err := h.UpdateUser(ctx, UpdateUserInput{User: ref(u)}); return err },
		opDelete:       func(h *Handler) error { return h.DeleteUser(ctx, ref(u), 0) },
		opGrantScopes:  func(h *Handler) error { _, err := h.GrantScopes(ctx, ref(u), nil); return err },
		opRevokeScopes: func(h *Handler) error { _, err := h.RevokeScopes(ctx, ref(u), nil); return err },
		opResetPassword: func(h *Handler) error {
			_, err := h.ResetPassword(ctx, ref(u), secret)
			return err
		},
	}
	for op, step := range steps {
		t.Run(op, func(t *testing.T) {
			h, logs := observed(&fakeUserRepo{user: u})
			if err := step(h); err != nil {
				t.Fatal(err)
			}
			lines := logs.FilterLevelExact(zapcore.InfoLevel).FilterField(zap.String("op", op)).All()
			if len(lines) != 1 {
				t.Fatalf("want one info line for %s, got %v", op, logs.All())
			}
			fields := lines[0].ContextMap()
			if fields["tenant_id"] != tenant.String() || fields["actor"] != "admin" {
				t.Errorf("line = %v, want tenant_id and actor", fields)
			}
			for _, l := range logs.All() {
				for k, v := range l.ContextMap() {
					s := fmt.Sprint(v)
					if strings.Contains(s, secret) || strings.Contains(s, "stored-hash") {
						t.Errorf("field %s carries a secret: %q", k, s)
					}
				}
			}
		})
	}
}

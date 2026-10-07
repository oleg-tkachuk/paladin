package userh

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// A user's resource name carries a tenant, and the tenant freeze judges a
// call by that tenant. Naming a live tenant with a trashed tenant's user
// acted on the trashed tenant's user past the freeze, because the user was
// found by id alone. Every operation now refuses a user who is not the named
// tenant's as not found, and changes nothing.
func TestAUserNamedUnderAnotherTenantIsNotFound(t *testing.T) {
	user := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	elsewhere := UserRef{TenantID: uuid.New(), UserID: user.UserID}
	admin := ctxAs(uuid.New(), apiutil.RolePlatformAdmin)
	scopes := []auth.Scope{{Type: auth.ScopeBucket, Value: "b1"}}

	ops := map[string]func(*Handler) error{
		"GetUser": func(h *Handler) error { _, err := h.GetUser(admin, elsewhere); return err },
		"UpdateUser": func(h *Handler) error {
			_, err := h.UpdateUser(admin, UpdateUserInput{User: elsewhere, UpdateMask: []string{"disabled"}, Disabled: true})
			return err
		},
		"DeleteUser":    func(h *Handler) error { return h.DeleteUser(admin, elsewhere, 0) },
		"GrantScopes":   func(h *Handler) error { _, err := h.GrantScopes(admin, elsewhere, scopes); return err },
		"RevokeScopes":  func(h *Handler) error { _, err := h.RevokeScopes(admin, elsewhere, scopes); return err },
		"ResetPassword": func(h *Handler) error { _, err := h.ResetPassword(admin, elsewhere, ""); return err },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			repo := &fakeUserRepo{user: user}
			if err := op(NewHandler(repo, allowAuthorizer{})); code(err) != connect.CodeNotFound {
				t.Fatalf("code = %v, want NotFound", code(err))
			}
			if repo.updated.UserID != uuid.Nil || repo.deletes != 0 || repo.pwdHashSets != 0 {
				t.Error("the user was changed")
			}
		})
	}
}

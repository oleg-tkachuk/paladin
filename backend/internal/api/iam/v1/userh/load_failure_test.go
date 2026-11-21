package userh

import (
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// errUsersDown stands in for a user store that could not be read.
var errUsersDown = errors.New("conn closed")

// Loading the user an RPC names: one that is not there is NOT_FOUND as
// "user not found"; a store that fails is the server's fault, not NOT_FOUND
// with the store's error as its message.
func TestUserLoadFailures(t *testing.T) {
	tenant := uuid.New()
	target := UserRef{TenantID: tenant, UserID: uuid.New()}
	ctx := ctxAs(tenant, apiutil.RolePlatformAdmin)
	rpcs := map[string]func(h *Handler) error{
		"DeleteUser":    func(h *Handler) error { return h.DeleteUser(ctx, target, 1) },
		"GrantScopes":   func(h *Handler) error { _, err := h.GrantScopes(ctx, target, nil); return err },
		"ResetPassword": func(h *Handler) error { _, err := h.ResetPassword(ctx, target, "new-password"); return err },
	}
	for _, tc := range []struct {
		name    string
		repoErr error
		want    connect.Code
		message string
	}{
		{"missing", authstore.ErrNotFound, connect.CodeNotFound, errUserNotFound.Error()},
		{"store down", errUsersDown, connect.CodeInternal, ""},
	} {
		for rpc, call := range rpcs {
			t.Run(tc.name+"/"+rpc, func(t *testing.T) {
				err := call(NewHandler(&fakeUserRepo{getErr: tc.repoErr}, allowAuthorizer{}))
				if code(err) != tc.want {
					t.Fatalf("code = %v, want %v (err=%v)", code(err), tc.want, err)
				}
				var cerr *connect.Error
				if tc.message != "" && (!errors.As(err, &cerr) || cerr.Message() != tc.message) {
					t.Errorf("message = %q, want %q", cerr.Message(), tc.message)
				}
				if !errors.Is(err, tc.repoErr) {
					t.Error("the store's error is not kept as the cause")
				}
			})
		}
	}
}

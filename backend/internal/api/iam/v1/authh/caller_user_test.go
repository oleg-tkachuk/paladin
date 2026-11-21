package authh

import (
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// errUsersDown stands in for a user store that could not be read.
var errUsersDown = errors.New("conn closed")

// Loading the caller's own user: one that is gone is NOT_FOUND in the
// product's words; a store that fails is the server's fault, not NOT_FOUND
// carrying the store's error as its message.
func TestCallerUserLoadFailures(t *testing.T) {
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "u1"}
	rpcs := map[string]func(h *Handler) error{
		"WhoAmI": func(h *Handler) error { _, err := h.WhoAmI(whoAmICtx(u), ""); return err },
		"ListMyMemberships": func(h *Handler) error {
			_, _, err := h.ListMyMemberships(whoAmICtx(u), ListMembershipsInput{})
			return err
		},
		"ChangePassword": func(h *Handler) error { return h.ChangePassword(ctxAsUser(u.UserID), "old", "new-password") },
	}
	for _, tc := range []struct {
		name    string
		repoErr error
		want    connect.Code
		message string
	}{
		{"gone", authstore.ErrNotFound, connect.CodeNotFound, errCallerUserGone.Error()},
		{"store down", errUsersDown, connect.CodeInternal, ""},
	} {
		for rpc, call := range rpcs {
			t.Run(tc.name+"/"+rpc, func(t *testing.T) {
				err := call(newHandler(&fakeUsers{err: tc.repoErr}, &fakeRefresh{}, &stubMinter{}))
				if got := connect.CodeOf(err); got != tc.want {
					t.Fatalf("code = %v, want %v (err=%v)", got, tc.want, err)
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

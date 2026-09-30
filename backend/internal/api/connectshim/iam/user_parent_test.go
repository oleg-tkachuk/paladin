package iam

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// listingUser records the scope ListUsers reached the handler with.
type listingUser struct {
	failingUser
	got   userh.ListUsersInput
	calls int
}

func (l *listingUser) ListUsers(_ context.Context, in userh.ListUsersInput) ([]authstore.User, string, error) {
	l.got = in
	l.calls++
	return nil, "", nil
}

// A parent that does not parse used to be dropped, so a request for one
// tenant's users came back with every tenant's. It is refused now, before the
// handler runs.
func TestListUsersRefusesAnUnparseableParent(t *testing.T) {
	t.Parallel()

	h := &listingUser{}
	_, err := (&UserServer{H: h}).ListUsers(context.Background(), connect.NewRequest(&pb.ListUsersRequest{
		Parent: "tenants/acme",
	}))
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want %v (err: %v)", got, connect.CodeInvalidArgument, err)
	}
	if h.calls != 0 {
		t.Errorf("handler ran %d time(s) for a parent it could not scope", h.calls)
	}
}

func TestListUsersScopesToTheParent(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	for parent, want := range map[string]uuid.UUID{
		"tenants/" + id.String(): id,
		"":                       uuid.Nil, // cross-tenant; the handler checks the role
	} {
		h := &listingUser{}
		if _, err := (&UserServer{H: h}).ListUsers(context.Background(), connect.NewRequest(&pb.ListUsersRequest{
			Parent: parent,
		})); err != nil {
			t.Fatalf("parent %q: %v", parent, err)
		}
		if h.got.TenantID != want {
			t.Errorf("parent %q: tenant = %v, want %v", parent, h.got.TenantID, want)
		}
	}
}

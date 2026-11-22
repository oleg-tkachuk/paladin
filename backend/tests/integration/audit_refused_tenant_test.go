//go:build integration

package integration

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// verdictPolicy answers every decision with one verdict.
type verdictPolicy cedar.Decision

func (v verdictPolicy) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.Decision(v), nil
}

// decidingUsers deletes a user once policy allows it on the user's tenant,
// as the user handler authorises a deletion.
type decidingUsers struct {
	paladiniamv1connect.UnimplementedUserServiceHandler
	policy verdictPolicy
}

func (u *decidingUsers) DeleteUser(ctx context.Context, req *iamv1.DeleteUserRequest) (*iamv1.DeleteUserResponse, error) {
	tenant, _ := apiutil.TenantInResourceName(req.GetName())
	if err := apiutil.Decide(ctx, u.policy, &cedar.Principal{}, cedar.ActionManageUser,
		&cedar.Resource{TenantID: tenant}, cedar.RequestContext{}); err != nil {
		return nil, err
	}
	return &iamv1.DeleteUserResponse{}, nil
}

// trailOf is every entry in tenant's trail.
func trailOf(t *testing.T, repo *adapters.AuditRepoV2, tenant uuid.UUID) []admindomain.AuditEntry {
	t.Helper()
	var out []admindomain.AuditEntry
	args := admindomain.ListAuditArgs{TrailTenantID: tenant}
	for {
		page, next, err := repo.List(context.Background(), args)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out = append(out, page...)
		if next == "" {
			return out
		}
		args.PageToken = next
	}
}

// A caller of tenant A that names tenant B's resource and is refused leaves
// no row in B's trail: the row is in A's, naming the target it tried. A
// platform admin admitted to B leaves its row in B's trail.
func TestAuditRefusedRequestStaysInTheCallersTrail(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolApp), h.PoolApp)
	callerTenant := mustCreateTenant(t, h.PoolMigrate, "refused-caller")
	victim := mustCreateTenant(t, h.PoolMigrate, "refused-victim")
	platform := mustCreateTenant(t, h.PoolMigrate, "refused-platform")
	target := "tenants/" + victim.String() + "/users/" + uuid.New().String()

	call := func(p *auth.Principal, allow bool) {
		t.Helper()
		client := paladiniamv1connect.NewUserServiceClient(unarytest.Client(func(s *connect.Server) {
			verdict := verdictPolicy(cedar.DecisionDeny)
			if allow {
				verdict = verdictPolicy(cedar.DecisionAllow)
			}
			paladiniamv1connect.RegisterUserServiceHandler(s, &decidingUsers{policy: verdict})
		}, middleware.AuditWithMirror(repo, auth.AudienceIAM, false, nil)))
		_, err := client.DeleteUser(auth.WithPrincipal(context.Background(), p), &iamv1.DeleteUserRequest{Name: target})
		if allow != (err == nil) {
			t.Fatalf("delete (allowed %t): %v", allow, err)
		}
	}

	call(&auth.Principal{TenantID: callerTenant, Subject: "intruder"}, false)
	if rows := trailOf(t, repo, victim); len(rows) != 0 {
		t.Fatalf("the named tenant's trail holds %d rows of a refused outsider, want none: %+v", len(rows), rows)
	}
	own := trailOf(t, repo, callerTenant)
	if len(own) != 1 {
		t.Fatalf("the caller's trail holds %d rows, want 1", len(own))
	}
	if got := own[0]; got.ResourceName != target || got.ResourceTenantID != uuid.Nil() || got.ErrorMessage == "" {
		t.Errorf("the caller's row names %q under %s with error %q, want the target %q under no tenant, refused",
			got.ResourceName, got.ResourceTenantID, got.ErrorMessage, target)
	}

	call(&auth.Principal{TenantID: platform, Subject: "ops", Roles: []string{apiutil.RolePlatformAdmin}}, true)
	rows := trailOf(t, repo, victim)
	if len(rows) != 1 || rows[0].ActorTenantID != platform || rows[0].ResourceTenantID != victim {
		t.Fatalf("the tenant's trail after a platform admin acted in it: %+v, want its one row", rows)
	}
}

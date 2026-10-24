package operationh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// A platform admin reading another tenant's History saw its audit trail
// without the batches its collections ran: ListOperations listed the
// caller's own tenant alone. Naming the tenant lists its operations, to a
// platform admin only, as ListAuditLog's tenant_id does.
func TestListOperationsOfANamedTenant(t *testing.T) {
	own, other := uuid.New(), uuid.New()
	admin := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "admin", TenantID: own, Roles: []string{apiutil.RolePlatformAdmin}})
	member := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: own, Roles: []string{apiutil.RoleTenantAdmin}})

	cases := []struct {
		name     string
		ctx      context.Context
		named    uuid.UUID
		want     uuid.UUID
		wantCode connect.Code
	}{
		{"a platform admin, another tenant", admin, other, other, 0},
		{"a platform admin, none named", admin, uuid.Nil, own, 0},
		{"a tenant's own member, its own tenant", member, own, own, 0},
		{"a tenant's member, another tenant", member, other, uuid.Nil, connect.CodePermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listed := false
			fr := &fakeRepo{listFn: func(context.Context, ListArgs) ([]Operation, string, error) {
				listed = true
				return nil, "", nil
			}}
			authz := allowAuthorizer()
			_, _, err := NewHandler(fr, authz).ListOperations(tc.ctx, ListInput{PageSize: 10, TenantID: tc.named})
			if tc.wantCode != 0 {
				wantCode(t, err, tc.wantCode)
				if listed || len(authz.calls) != 0 {
					t.Error("a refused call reached the policy or the store")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if fr.lastList.tenantID != tc.want {
				t.Errorf("listed tenant %v, want %v", fr.lastList.tenantID, tc.want)
			}
			if len(authz.calls) != 1 || authz.calls[0].resource.TenantID != tc.want {
				t.Errorf("authorized %+v, want the listed tenant", authz.calls)
			}
		})
	}
}

//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/policies"
	pbdata "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// Object.placement through the assembled data plane, on the real Cedar engine
// and the policy stored for the tenant: a platform admin sees it, a member
// under the default policy reads the same object without it, and a role the
// tenant's policy grants ReadObjectPlacement sees it too.
func TestObjectPlacementIsGatedByCedar(t *testing.T) {
	f := newActingTenantFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), actingTenantCallTimeout)
	defer cancel()

	policy := strings.ReplaceAll(policies.DefaultTenantPolicy, policies.TenantPlaceholder, "acting-target") + `
permit (principal, action == Action::"ReadObjectPlacement", resource)
when { principal.roles.contains("storage.operator") };
`
	if _, err := f.h.PoolMigrate.Exec(ctx,
		`UPDATE tenants SET inherited_cedar_policy = $2 WHERE id = $1`, f.target, policy); err != nil {
		t.Fatalf("set tenant policy: %v", err)
	}

	want := &pbdata.PhysicalPlacement{
		BackendId:   "primary",
		BucketId:    "paladin-test",
		StoragePath: f.target.String() + "/" + actingCollection + "/target.txt",
	}
	cases := []struct {
		name  string
		token string
		sees  bool
	}{
		{"platform admin", f.mint(f.platform, "platform.admin"), true},
		{"tenant member", f.mint(f.target), false},
		{"tenant admin", f.mint(f.target, "tenant.admin", "collection:admin"), false},
		{"granted role", f.mint(f.target, "storage.operator"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := f.objects.GetObject(authed(ctx, c.token), &pbdata.GetObjectRequest{Name: objectOf(f.target, f.inTarget)})
			if err != nil {
				t.Fatalf("GetObject: %v", err)
			}
			list, err := f.objects.ListObjects(authed(ctx, c.token), &pbdata.ListObjectsRequest{Parent: collectionOf(f.target)})
			if err != nil {
				t.Fatalf("ListObjects: %v", err)
			}
			if len(list.GetObjects()) != 1 {
				t.Fatalf("ListObjects returned %d objects, want 1", len(list.GetObjects()))
			}
			for rpc, p := range map[string]*pbdata.PhysicalPlacement{
				"GetObject":   got.GetPlacement(),
				"ListObjects": list.GetObjects()[0].GetPlacement(),
			} {
				switch {
				case !c.sees && p != nil:
					t.Errorf("%s: placement %v leaked", rpc, p)
				case c.sees && (p.GetBackendId() != want.GetBackendId() ||
					p.GetBucketId() != want.GetBucketId() ||
					p.GetStoragePath() != want.GetStoragePath()):
					t.Errorf("%s: placement = %v, want %v", rpc, p, want)
				}
			}
		})
	}
}

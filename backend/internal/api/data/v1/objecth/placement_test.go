package objecth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/policies"
)

// Object.placement (backend, bucket, key inside the bucket) is gated by its
// own Cedar action, ReadObjectPlacement. These tests run the real engine over
// the default tenant policy, so "who sees placement by default" is the
// shipped answer, not a stub's: platform.admin only, through the built-in
// permit. A tenant policy can grant it like any other action.

// placementRepo serves one object by id and by key, and a fixed list page, all
// in a collection bound to backend-list/bucket-list (listRepo.LookupBucket).
type placementRepo struct {
	listRepo
	obj       Object
	bucketErr error
}

func (r *placementRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.obj, nil
}

func (r *placementRepo) FindByPath(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.obj, nil
}

func (r *placementRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error) {
	if r.bucketErr != nil {
		return "", "", r.bucketErr
	}
	return r.listRepo.LookupBucket(ctx, tenantID, collection, write)
}

// grantOperatorPlacement is a tenant rule handing ReadObjectPlacement to a
// role in the caller's own tenant — the way an operator delegates it.
const grantOperatorPlacement = `
permit (
    principal,
    action == Action::"ReadObjectPlacement",
    resource
) when {
    principal.roles.contains("storage.operator") &&
    resource has tenant_id &&
    principal.tenant_id == resource.tenant_id
};
`

func defaultPolicyFor(tenantID uuid.UUID) string {
	return strings.ReplaceAll(policies.DefaultTenantPolicy, policies.TenantPlaceholder, tenantID.String())
}

type placementFixture struct {
	tenantID uuid.UUID
	repo     *placementRepo
}

func newPlacementFixture() placementFixture {
	tenantID := uuid.New()
	obj := Object{
		ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "reports/q3.pdf",
		State: statemachine.StateAvailable, SizeBytes: 10, ContentType: "application/pdf",
	}
	page := []Object{
		{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "public/a.txt", Tags: map[string]string{}},
		{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "private/b.txt", Tags: map[string]string{}},
	}
	return placementFixture{tenantID: tenantID, repo: &placementRepo{listRepo: listRepo{page: page}, obj: obj}}
}

func (f placementFixture) handler(policy cedar.Authorizer) *Handler {
	return &Handler{repo: f.repo, policy: policy, filter: cel.NewEvaluator()}
}

func (f placementFixture) as(kind auth.PrincipalKind, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "caller", TenantID: f.tenantID, Kind: kind, Roles: roles,
	})
}

func (f placementFixture) wantPlacement(t *testing.T, key string) Placement {
	t.Helper()
	return Placement{
		BackendID: "backend-list", Bucket: "bucket-list",
		StoragePath: f.tenantID.String() + "/docs/" + key,
	}
}

// readAll runs the three reads that report placement and returns what each
// gave, keyed by RPC, so a test states one expectation for all of them.
func readAll(t *testing.T, h *Handler, ctx context.Context, f placementFixture) map[string][]*Placement {
	t.Helper()
	got := map[string][]*Placement{}
	o, err := h.GetObject(ctx, "docs", f.repo.obj.ObjectID.String())
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	got["GetObject"] = []*Placement{o.Placement}
	o, err = h.LookupObject(ctx, "docs", f.repo.obj.Key)
	if err != nil {
		t.Fatalf("LookupObject: %v", err)
	}
	got["LookupObject"] = []*Placement{o.Placement}
	objs, _, err := h.ListObjects(ctx, ListObjectsInput{Collection: "docs", PageSize: 10})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != len(f.repo.page) {
		t.Fatalf("ListObjects returned %d rows, want %d — placement must never drop a row", len(objs), len(f.repo.page))
	}
	for i := range objs {
		got["ListObjects"] = append(got["ListObjects"], objs[i].Placement)
	}
	return got
}

func TestPlacementGate(t *testing.T) {
	f := newPlacementFixture()

	cases := []struct {
		name   string
		policy string
		ctx    context.Context
		want   bool
	}{
		{"platform admin, default policy", defaultPolicyFor(f.tenantID), f.as(auth.PrincipalKindUser, "platform.admin"), true},
		{"tenant member, default policy", defaultPolicyFor(f.tenantID), f.as(auth.PrincipalKindUser), false},
		{"collection admin, default policy", defaultPolicyFor(f.tenantID), f.as(auth.PrincipalKindUser, "collection:admin", "tenant.admin"), false},
		{"capability, default policy", defaultPolicyFor(f.tenantID), f.as(auth.PrincipalKindCapability), false},
		{"api key, default policy", defaultPolicyFor(f.tenantID), f.as(auth.PrincipalKindApiKey), false},
		{"operator, policy grants the action", defaultPolicyFor(f.tenantID) + grantOperatorPlacement, f.as(auth.PrincipalKindUser, "storage.operator"), true},
		{"member, policy grants the action to another role", defaultPolicyFor(f.tenantID) + grantOperatorPlacement, f.as(auth.PrincipalKindUser), false},
		{
			"platform admin, tenant forbids the action",
			defaultPolicyFor(f.tenantID) + `forbid (principal, action == Action::"ReadObjectPlacement", resource);`,
			f.as(auth.PrincipalKindUser, "platform.admin"), false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := f.handler(cedartest.Engine(c.policy))
			for rpc, ps := range readAll(t, h, c.ctx, f) {
				for i, p := range ps {
					if !c.want {
						if p != nil {
							t.Errorf("%s[%d]: placement %+v leaked to a caller without ReadObjectPlacement", rpc, i, *p)
						}
						continue
					}
					if p == nil {
						t.Errorf("%s[%d]: no placement for a caller allowed ReadObjectPlacement", rpc, i)
						continue
					}
					key := f.repo.obj.Key
					if rpc == "ListObjects" {
						key = f.repo.page[i].Key
					}
					if want := f.wantPlacement(t, key); *p != want {
						t.Errorf("%s[%d]: placement = %+v, want %+v", rpc, i, *p, want)
					}
				}
			}
		})
	}
}

// A policy that decides placement on the object itself (its key) turns the
// listing's per-row evaluation on, and placement then follows each row's own
// decision instead of one collection-wide answer.
func TestPlacementListDecidesPerRowWhenThePolicyReadsTheObject(t *testing.T) {
	f := newPlacementFixture()
	policy := defaultPolicyFor(f.tenantID) + `
permit (principal, action == Action::"ReadObjectPlacement", resource)
when { resource has key && resource.key like "public/*" };
`
	h := f.handler(cedartest.Engine(policy))
	objs, _, err := h.ListObjects(f.as(auth.PrincipalKindUser), ListObjectsInput{Collection: "docs", PageSize: 10})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("rows = %d, want 2", len(objs))
	}
	for _, o := range objs {
		allowed := strings.HasPrefix(o.Key, "public/")
		if allowed && (o.Placement == nil || *o.Placement != f.wantPlacement(t, o.Key)) {
			t.Errorf("%s: placement = %+v, want it", o.Key, o.Placement)
		}
		if !allowed && o.Placement != nil {
			t.Errorf("%s: placement %+v leaked past a per-object denial", o.Key, *o.Placement)
		}
	}
}

// failPlacement allows every check but ReadObjectPlacement, which fails as a
// broken engine would.
type failPlacement struct{}

func (failPlacement) IsAuthorized(_ context.Context, _ *cedar.Principal, a cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	if a == cedar.ActionReadObjectPlacement {
		return cedar.DecisionDeny, errors.New("engine down")
	}
	return cedar.DecisionAllow, nil
}

// Placement is enrichment: an engine fault on its check, or a collection whose
// bucket binding cannot be resolved, leaves the field unset and the read
// succeeds.
func TestPlacementNeverFailsTheRead(t *testing.T) {
	t.Run("engine fault on the placement check", func(t *testing.T) {
		f := newPlacementFixture()
		for rpc, ps := range readAll(t, f.handler(failPlacement{}), f.as(auth.PrincipalKindUser, "platform.admin"), f) {
			for i, p := range ps {
				if p != nil {
					t.Errorf("%s[%d]: placement %+v set although its check failed", rpc, i, *p)
				}
			}
		}
	})
	t.Run("unresolved bucket binding", func(t *testing.T) {
		f := newPlacementFixture()
		f.repo.bucketErr = errors.New("lookup failed")
		h := f.handler(cedartest.Engine(defaultPolicyFor(f.tenantID)))
		for rpc, ps := range readAll(t, h, f.as(auth.PrincipalKindUser, "platform.admin"), f) {
			for i, p := range ps {
				if p != nil {
					t.Errorf("%s[%d]: placement %+v reported without a bucket binding", rpc, i, *p)
				}
			}
		}
	})
}

package admin

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The parent of CreateSubscription and ListSubscriptions is documented as
// "tenants/{tenant_id_or_slug}". Both parsed it as a UUID only: a slug was
// InvalidArgument on Create, and on List it was dropped without a word, so the
// listing came back unscoped instead of narrowed to the tenant named.

const (
	refSlug        = "acme"
	refUnknownSlug = "nobody"
	refMalformed   = "tenants/Not_A_Slug"
)

// creatingSubs records the subscription Create received.
type creatingSubs struct {
	recordingSubs
	created admindomain.EventSubscription
}

func (c *creatingSubs) Create(_ context.Context, s admindomain.EventSubscription) (*admindomain.EventSubscription, error) {
	c.created = s
	return &s, nil
}

func TestSubscriptionParentAcceptsIDOrSlug(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	cases := []struct {
		label    string
		parent   string
		want     uuid.UUID
		wantCode connect.Code
	}{
		{"uuid", "tenants/" + tenantID.String(), tenantID, 0},
		{"slug", "tenants/" + refSlug, tenantID, 0},
		{"unknown slug", "tenants/" + refUnknownSlug, uuid.Nil, connect.CodeNotFound},
		{"malformed", refMalformed, uuid.Nil, connect.CodeInvalidArgument},
		{"not a tenant", "orgs/" + refSlug, uuid.Nil, connect.CodeInvalidArgument},
	}
	for _, tc := range cases {
		t.Run("Create/"+tc.label, func(t *testing.T) {
			t.Parallel()
			h := &creatingSubs{}
			srv := &EventSubscriptionServer{H: h, Tenants: &slugTenant{slug: refSlug, id: tenantID}}
			_, err := srv.CreateSubscription(context.Background(), connect.NewRequest(&pb.CreateSubscriptionRequest{
				Parent:       tc.parent,
				Subscription: &pb.EventSubscription{},
			}))
			assertTenantResolution(t, err, tc.wantCode, h.created.TenantID, tc.want)
		})
		t.Run("List/"+tc.label, func(t *testing.T) {
			t.Parallel()
			h := &recordingSubs{}
			srv := &EventSubscriptionServer{H: h, Tenants: &slugTenant{slug: refSlug, id: tenantID}}
			_, err := srv.ListSubscriptions(context.Background(), connect.NewRequest(&pb.ListSubscriptionsRequest{
				Parent: tc.parent,
			}))
			assertTenantResolution(t, err, tc.wantCode, h.args.TenantID, tc.want)
		})
	}
}

// A slug cannot be resolved without a tenant lookup; say so rather than
// guessing a scope.
func TestSubscriptionParentSlugWithoutResolver(t *testing.T) {
	t.Parallel()

	srv := &EventSubscriptionServer{H: &creatingSubs{}}
	_, err := srv.CreateSubscription(context.Background(), connect.NewRequest(&pb.CreateSubscriptionRequest{
		Parent:       "tenants/" + refSlug,
		Subscription: &pb.EventSubscription{},
	}))
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want %v (err %v)", got, connect.CodeInvalidArgument, err)
	}
}

// accessibleBuckets records the tenant ListAccessibleBuckets was scoped to.
type accessibleBuckets struct {
	failingBucket
	tenant uuid.UUID
}

func (a *accessibleBuckets) ListAccessibleBuckets(_ context.Context, tenantID uuid.UUID, _ int32, _, _ string) ([]admindomain.Bucket, string, error) {
	a.tenant = tenantID
	return nil, "", nil
}

func TestListAccessibleBucketsAcceptsIDOrSlug(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	cases := []struct {
		label    string
		tenant   string
		want     uuid.UUID
		wantCode connect.Code
	}{
		{"uuid", "tenants/" + tenantID.String(), tenantID, 0},
		{"slug", "tenants/" + refSlug, tenantID, 0},
		{"unknown slug", "tenants/" + refUnknownSlug, uuid.Nil, connect.CodeNotFound},
		{"malformed", refMalformed, uuid.Nil, connect.CodeInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			h := &accessibleBuckets{}
			srv := &BucketServer{H: h, Tenants: &slugTenant{slug: refSlug, id: tenantID}}
			_, err := srv.ListAccessibleBuckets(context.Background(), connect.NewRequest(&pb.ListAccessibleBucketsRequest{
				Tenant: tc.tenant,
			}))
			assertTenantResolution(t, err, tc.wantCode, h.tenant, tc.want)
		})
	}
}

// A resolver failure that is not a Connect error passes through unchanged, so
// the handler's own mapping decides the code.
func TestResolveTenantNamePassesResolverErrors(t *testing.T) {
	t.Parallel()

	_, err := resolveTenantName(context.Background(), slugTenants{}, "tenants/"+refSlug)
	if !errors.Is(err, errBoom) {
		t.Errorf("err = %v, want %v", err, errBoom)
	}
}

func assertTenantResolution(t *testing.T, err error, wantCode connect.Code, got, want uuid.UUID) {
	t.Helper()
	if wantCode != 0 {
		if code := connect.CodeOf(err); code != wantCode {
			t.Fatalf("code = %v, want %v (err %v)", code, wantCode, err)
		}
		if got != uuid.Nil {
			t.Errorf("the handler was reached with tenant %v after a failed resolution", got)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("scoped to tenant %v, want %v", got, want)
	}
}

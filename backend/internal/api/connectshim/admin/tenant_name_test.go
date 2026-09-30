package admin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// slugTenant resolves one slug and records the id each mutating call received.
type slugTenant struct {
	failingTenant
	slug string
	id   uuid.UUID
	got  uuid.UUID
}

func (f *slugTenant) GetTenantBySlug(_ context.Context, slug string) (*tenanth.Tenant, error) {
	if slug != f.slug {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}
	return &tenanth.Tenant{TenantID: f.id, Slug: slug}, nil
}

func (f *slugTenant) DeleteTenant(_ context.Context, id uuid.UUID, _ int64) error {
	f.got = id
	return nil
}

func (f *slugTenant) PurgeTenant(_ context.Context, id uuid.UUID) error {
	f.got = id
	return nil
}

func (f *slugTenant) RestoreTenant(_ context.Context, id uuid.UUID) (*tenanth.Tenant, error) {
	f.got = id
	return &tenanth.Tenant{TenantID: id}, nil
}

func (f *slugTenant) UpdateTenant(_ context.Context, args tenanth.UpdateTenantArgs) (*tenanth.Tenant, error) {
	f.got = args.TenantID
	return &tenanth.Tenant{TenantID: args.TenantID}, nil
}

func (f *slugTenant) RenameTenantSlug(_ context.Context, args tenanth.RenameTenantSlugArgs) (*tenanth.Tenant, error) {
	f.got = args.TenantID
	return &tenanth.Tenant{TenantID: args.TenantID}, nil
}

// Every RPC that names one tenant takes `tenants/{tenant_id_or_slug}`, as the
// proto documents. DeleteTenant, UpdateTenant, SetInheritedPolicy and
// RenameTenantSlug used to parse the name as a UUID only, so a slug that
// GetTenant and PurgeTenant accepted was InvalidArgument on the call between
// them.
func TestTenantRPCsAcceptASlug(t *testing.T) {
	t.Parallel()

	const (
		slug    = "acme"
		name    = "tenants/" + slug
		version = "3"
	)
	cases := map[string]func(*TenantServer) error{
		"DeleteTenant": func(s *TenantServer) error {
			_, err := s.DeleteTenant(context.Background(), connect.NewRequest(&pb.DeleteTenantRequest{
				Name: name, ResourceVersion: version,
			}))
			return err
		},
		"PurgeTenant": func(s *TenantServer) error {
			_, err := s.PurgeTenant(context.Background(), connect.NewRequest(&pb.PurgeTenantRequest{Name: name}))
			return err
		},
		"RestoreTenant": func(s *TenantServer) error {
			_, err := s.RestoreTenant(context.Background(), connect.NewRequest(&pb.RestoreTenantRequest{Name: name}))
			return err
		},
		"UpdateTenant": func(s *TenantServer) error {
			_, err := s.UpdateTenant(context.Background(), connect.NewRequest(&pb.UpdateTenantRequest{
				Name: name, ResourceVersion: version,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
				Tenant:     &pb.Tenant{DisplayName: "Acme"},
			}))
			return err
		},
		"SetInheritedPolicy": func(s *TenantServer) error {
			_, err := s.SetInheritedPolicy(context.Background(), connect.NewRequest(&pb.SetInheritedPolicyRequest{
				Name: name, ResourceVersion: version,
			}))
			return err
		},
		"RenameTenantSlug": func(s *TenantServer) error {
			_, err := s.RenameTenantSlug(context.Background(), connect.NewRequest(&pb.RenameTenantSlugRequest{
				Name: name, ResourceVersion: version, NewSlug: "acme-2",
			}))
			return err
		},
	}
	for rpc, call := range cases {
		t.Run(rpc, func(t *testing.T) {
			t.Parallel()
			h := &slugTenant{slug: slug, id: uuid.New()}
			if err := call(&TenantServer{H: h}); err != nil {
				t.Fatalf("%s(%q): %v", rpc, name, err)
			}
			if h.got != h.id {
				t.Errorf("%s reached the handler with tenant %v, want %v resolved from the slug", rpc, h.got, h.id)
			}
		})
	}
}

// An unknown slug is the lookup's answer, not a parse failure.
func TestTenantRPCsReportAnUnknownSlugAsNotFound(t *testing.T) {
	t.Parallel()

	s := &TenantServer{H: &slugTenant{slug: "acme", id: uuid.New()}}
	_, err := s.DeleteTenant(context.Background(), connect.NewRequest(&pb.DeleteTenantRequest{
		Name: "tenants/other", ResourceVersion: "1",
	}))
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("code = %v, want %v (err: %v)", got, connect.CodeNotFound, err)
	}
}

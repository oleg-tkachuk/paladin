package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	admin "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	data "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	iam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// frozenPlanes are the packages whose RPCs the freeze classifies.
var frozenPlanes = []protoreflect.FullName{
	datav1.File_paladin_data_v1_types_proto.Package(),
	adminv1.File_paladin_admin_v1_types_proto.Package(),
	iamv1.File_paladin_iam_v1_types_proto.Package(),
}

// eachMethod calls fn with every RPC of the frozen planes.
func eachMethod(fn func(procedure string, md protoreflect.MethodDescriptor)) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for _, pkg := range frozenPlanes {
			if fd.Package() != pkg {
				continue
			}
			for i := 0; i < fd.Services().Len(); i++ {
				svc := fd.Services().Get(i)
				for j := 0; j < svc.Methods().Len(); j++ {
					md := svc.Methods().Get(j)
					fn("/"+string(svc.FullName())+"/"+string(md.Name()), md)
				}
			}
		}
		return true
	})
}

// An RPC added without a kind would be refused on every trashed tenant it
// names, read or not; one listed that no longer exists hides a rename.
func TestEveryRPCIsClassified(t *testing.T) {
	seen := map[string]bool{}
	eachMethod(func(procedure string, _ protoreflect.MethodDescriptor) {
		seen[procedure] = true
		if _, ok := procedureKinds[procedure]; !ok {
			t.Errorf("%s has no kind in procedureKinds", procedure)
		}
	})
	for procedure := range procedureKinds {
		if !seen[procedure] {
			t.Errorf("%s is classified but is not an RPC", procedure)
		}
	}
}

// The freeze reads unary requests only, so a streaming change would pass it.
func TestNoChangeIsStreaming(t *testing.T) {
	eachMethod(func(procedure string, md protoreflect.MethodDescriptor) {
		if (md.IsStreamingClient() || md.IsStreamingServer()) && procedureKinds[procedure] == Changes {
			t.Errorf("%s is a streaming change, which TenantFreeze does not see", procedure)
		}
	})
}

// notTenantFields are string fields of the frozen planes' requests that look
// as if they could name a tenant and do not.
var notTenantFields = map[protoreflect.Name]bool{
	"display_name":  true, // a label
	"sasl_username": true, // a Kafka sink's login
	// What SimulateAuthz and GetEffectivePolicy evaluate; both only read.
	"resource_name": true,
}

// A request field naming a tenant that the walk does not read would let a
// change reach a trashed tenant through it.
func TestEveryTenantFieldIsKnown(t *testing.T) {
	known := func(n protoreflect.Name) bool {
		return nameFields[n] || tenantNameFields[n] || tenantIDFields[n] || ignoredTenantFields[n] || notTenantFields[n]
	}
	suspect := func(n protoreflect.Name) bool {
		s := string(n)
		return strings.Contains(s, "tenant") || strings.HasSuffix(s, "name") ||
			strings.HasSuffix(s, "names") || strings.HasSuffix(s, "parent")
	}
	visited := map[protoreflect.FullName]bool{}
	var walk func(m protoreflect.MessageDescriptor)
	walk = func(m protoreflect.MessageDescriptor) {
		if visited[m.FullName()] {
			return
		}
		visited[m.FullName()] = true
		for i := 0; i < m.Fields().Len(); i++ {
			f := m.Fields().Get(i)
			switch {
			case f.Kind() == protoreflect.StringKind && suspect(f.Name()) && !known(f.Name()):
				t.Errorf("%s.%s may name a tenant and the freeze neither reads nor rules it out", m.FullName(), f.Name())
			case f.Kind() == protoreflect.MessageKind && !f.IsMap():
				walk(f.Message())
			}
		}
	}
	eachMethod(func(_ string, md protoreflect.MethodDescriptor) { walk(md.Input()) })
}

// fakeSlugs answers from a map.
type fakeSlugs struct {
	ids map[string]uuid.UUID
	err error
}

func (f fakeSlugs) TenantIDBySlug(_ context.Context, slug string) (uuid.UUID, bool, error) {
	id, ok := f.ids[slug]
	return id, ok, f.err
}

func TestTenantFreeze(t *testing.T) {
	trashed, live, gone := uuid.New(), uuid.New(), uuid.New()
	states := &fakeStates{states: map[uuid.UUID]auth.TenantState{
		trashed: auth.TenantTrashed, live: auth.TenantLive, gone: auth.TenantMissing,
	}}
	slugs := fakeSlugs{ids: map[string]uuid.UUID{"acme": trashed, "beta": live}}
	collection := func(tenant string) string { return "tenants/" + tenant + "/collections/c" }

	for name, tc := range map[string]struct {
		procedure string
		msg       proto.Message
		frozen    bool
	}{
		"an upload into a trashed tenant": {data.ObjectServiceUploadObjectProcedure,
			&datav1.UploadObjectRequest{Parent: collection(trashed.String())}, true},
		"a change naming the tenant by slug": {admin.CollectionServiceCreateCollectionProcedure,
			&adminv1.CreateCollectionRequest{Parent: "tenants/acme"}, true},
		"a nested owner": {admin.BucketServiceCreateBucketProcedure,
			&adminv1.CreateBucketRequest{Bucket: &adminv1.Bucket{OwnerTenantId: trashed.String()}}, true},
		"a bare tenant id": {admin.TenantBudgetServiceSetProcedure,
			&adminv1.TenantBudgetServiceSetRequest{TenantId: trashed.String()}, true},
		"an update of the tenant": {admin.TenantServiceUpdateTenantProcedure,
			&adminv1.UpdateTenantRequest{Tenant: &adminv1.Tenant{Name: "tenants/" + trashed.String()}}, true},
		"an unclassified RPC is a change": {"/paladin.data.v1.ObjectService/NotYetClassified",
			&datav1.UploadObjectRequest{Parent: collection(trashed.String())}, true},
		"a user created in a trashed tenant": {iam.UserServiceCreateUserProcedure,
			&iamv1.CreateUserRequest{Parent: "tenants/" + trashed.String()}, true},
		"a user of a trashed tenant updated": {iam.UserServiceUpdateUserProcedure,
			&iamv1.UpdateUserRequest{Name: "tenants/" + trashed.String() + "/users/" + uuid.NewString()}, true},
		"switching into a trashed tenant": {iam.AuthServiceSwitchTenantProcedure,
			&iamv1.SwitchTenantRequest{TargetTenantId: trashed.String()}, true},
		"revoking a user's scopes": {iam.UserServiceRevokeScopesProcedure,
			&iamv1.RevokeScopesRequest{Name: "tenants/" + trashed.String() + "/users/u"}, false},
		"a download":        {data.ObjectServiceDownloadObjectProcedure, &datav1.DownloadObjectRequest{Name: collection(trashed.String()) + "/objects/o"}, false},
		"a presigned read":  {data.PresignServicePresignDownloadProcedure, &datav1.PresignDownloadRequest{Name: collection(trashed.String()) + "/objects/o"}, false},
		"revoking a token":  {admin.APITokenServiceRevokeProcedure, &adminv1.APITokenServiceRevokeRequest{Name: "tenants/" + trashed.String() + "/apiTokens/x"}, false},
		"restoring":         {admin.TenantServiceRestoreTenantProcedure, &adminv1.RestoreTenantRequest{Name: "tenants/" + trashed.String()}, false},
		"purging":           {admin.TenantServicePurgeTenantProcedure, &adminv1.PurgeTenantRequest{Name: "tenants/" + trashed.String()}, false},
		"a live tenant":     {data.ObjectServiceUploadObjectProcedure, &datav1.UploadObjectRequest{Parent: collection(live.String())}, false},
		"a live slug":       {admin.CollectionServiceCreateCollectionProcedure, &adminv1.CreateCollectionRequest{Parent: "tenants/beta"}, false},
		"an unknown slug":   {admin.CollectionServiceCreateCollectionProcedure, &adminv1.CreateCollectionRequest{Parent: "tenants/nobody"}, false},
		"a tenant gone":     {data.ObjectServiceUploadObjectProcedure, &datav1.UploadObjectRequest{Parent: collection(gone.String())}, false},
		"a platform change": {admin.BackendServiceCreateBackendProcedure, &adminv1.CreateBackendRequest{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkFrozen(platformCtx(), states, slugs, tc.procedure, tc.msg)
			if !tc.frozen {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || reasonOf(err) != "ERROR_REASON_TENANT_ALREADY_DELETED" {
				t.Fatalf("err = %v (reason %q), want FailedPrecondition with TENANT_ALREADY_DELETED", err, reasonOf(err))
			}
		})
	}
}

// A read needs no state at all: the freeze only looks a tenant up for a
// change.
func TestTenantFreezeReadsNothingForARead(t *testing.T) {
	states := &fakeStates{err: errors.New("down")}
	err := checkFrozen(platformCtx(), states, fakeSlugs{err: errors.New("down")},
		data.ObjectServiceGetObjectProcedure, &datav1.GetObjectRequest{Name: "tenants/" + uuid.NewString() + "/collections/c/objects/o"})
	if err != nil || states.reads != 0 {
		t.Errorf("err = %v after %d reads, want a read let through unread", err, states.reads)
	}
}

// A state or slug that cannot be read refuses the change: the freeze fails
// closed.
func TestTenantFreezeFailsClosed(t *testing.T) {
	down := errors.New("database down")
	for name, tc := range map[string]struct {
		states auth.TenantStateReader
		slugs  TenantSlugs
		parent string
	}{
		"state unreadable": {&fakeStates{err: down}, fakeSlugs{}, "tenants/" + uuid.NewString()},
		"slug unreadable":  {&fakeStates{}, fakeSlugs{err: down}, "tenants/acme"},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkFrozen(platformCtx(), tc.states, tc.slugs,
				admin.CollectionServiceCreateCollectionProcedure, &adminv1.CreateCollectionRequest{Parent: tc.parent})
			if connect.CodeOf(err) != connect.CodeUnavailable {
				t.Errorf("err = %v, want Unavailable", err)
			}
		})
	}
}

// The walk reads every kind of field that names a tenant: a resource name, a
// tenant name on its own, a bare id or slug — and skips a simulated
// principal's tenant.
func TestRequestTenants(t *testing.T) {
	byName, byTenant, byID, simulated := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for name, tc := range map[string]struct {
		msg   proto.Message
		ids   []uuid.UUID
		slugs []string
	}{
		"a resource name":       {&datav1.GetObjectRequest{Name: "tenants/" + byName.String() + "/collections/c/objects/o"}, []uuid.UUID{byName}, nil},
		"a tenant name":         {&adminv1.ListAccessibleBucketsRequest{Tenant: "tenants/" + byTenant.String()}, []uuid.UUID{byTenant}, nil},
		"a tenant name by slug": {&adminv1.ListAccessibleBucketsRequest{Tenant: "tenants/acme"}, nil, []string{"acme"}},
		"a bare id":             {&adminv1.TenantBudgetServiceSetRequest{TenantId: byID.String()}, []uuid.UUID{byID}, nil},
		"a simulated principal": {&adminv1.SimulateAuthzRequest{PrincipalTenantId: simulated.String()}, nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := requestTenants(tc.msg.ProtoReflect())
			if len(got.ids) != len(tc.ids) || len(got.slugs) != len(tc.slugs) {
				t.Fatalf("got ids %v slugs %v, want %v %v", got.ids, got.slugs, tc.ids, tc.slugs)
			}
			for _, id := range tc.ids {
				if !got.ids[id] {
					t.Errorf("missing %s", id)
				}
			}
			for _, s := range tc.slugs {
				if !got.slugs[s] {
					t.Errorf("missing slug %s", s)
				}
			}
		})
	}
}

// platformCtx carries a platform admin of a tenant of its own.
func platformCtx() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "admin", TenantID: uuid.New(), Roles: []string{apiutil.RolePlatformAdmin},
	})
}

// A principal without a platform role is confined to its own tenant, so the
// freeze tells it nothing about another tenant it names — not even by
// looking it up. The handler refuses it as it always did.
func TestTenantFreezeSaysNothingToATenantPrincipal(t *testing.T) {
	trashed := uuid.New()
	states := &fakeStates{states: map[uuid.UUID]auth.TenantState{trashed: auth.TenantTrashed}}
	slugs := &countingSlugs{}
	for name, ctx := range map[string]context.Context{
		"no principal": context.Background(),
		"a tenant admin": auth.WithPrincipal(context.Background(), &auth.Principal{
			Subject: "u", TenantID: uuid.New(), Roles: []string{apiutil.RoleTenantAdmin},
		}),
	} {
		t.Run(name, func(t *testing.T) {
			for _, msg := range []proto.Message{
				&datav1.UploadObjectRequest{Parent: "tenants/" + trashed.String() + "/collections/c"},
				&adminv1.CreateCollectionRequest{Parent: "tenants/acme"},
			} {
				if err := checkFrozen(ctx, states, slugs, data.ObjectServiceUploadObjectProcedure, msg); err != nil {
					t.Errorf("told %v", err)
				}
			}
			if states.reads != 0 || slugs.calls != 0 {
				t.Errorf("looked up %d states and %d slugs for a tenant principal", states.reads, slugs.calls)
			}
		})
	}
}

type countingSlugs struct{ calls int }

func (c *countingSlugs) TenantIDBySlug(context.Context, string) (uuid.UUID, bool, error) {
	c.calls++
	return uuid.Nil, false, nil
}

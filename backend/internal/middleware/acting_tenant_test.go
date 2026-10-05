package middleware

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

func collectionIn(tenant uuid.UUID) string {
	return apiutil.TenantNamePrefix + tenant.String() + "/collections/docs"
}

func objectIn(tenant uuid.UUID) string {
	return collectionIn(tenant) + "/objects/" + uuid.NewString()
}

// The tenant a request acts on is decided before the rate limiter and the
// idempotency store key on it. Only a platform admin is moved, only onto one
// tenant its names agree on; the shim refuses everyone and everything else.
func TestActOnNamedTenant(t *testing.T) {
	own, target, other := uuid.New(), uuid.New(), uuid.New()
	admin := auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: own, Subject: "ops", Roles: []string{apiutil.RolePlatformAdmin},
	})
	member := auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: own, Subject: "alice", Roles: []string{"tenant.admin"},
	})
	cases := []struct {
		name string
		ctx  context.Context
		msg  proto.Message
		want uuid.UUID // uuid.Nil: no acting tenant set
	}{
		{"admin, parent in another tenant", admin, &datav1.ListObjectsRequest{Parent: collectionIn(target)}, target},
		{"admin, object_name", admin, &datav1.CompleteMultipartUploadRequest{ObjectName: objectIn(target)}, target},
		{"admin, names only inside a selector", admin, &datav1.BatchDeleteObjectsRequest{
			Selector: &datav1.ObjectSelector{Names: []string{objectIn(target), objectIn(target)}},
		}, target},
		{"admin, its own tenant", admin, &datav1.ListObjectsRequest{Parent: collectionIn(own)}, uuid.Nil},
		{"admin, names spanning tenants", admin, &datav1.CopyObjectRequest{
			SourceName: objectIn(target), DestinationCollection: collectionIn(other),
		}, uuid.Nil},
		{"admin, no name", admin, &datav1.EnsureTenantStorageRequest{BackendId: "primary", Bucket: "b"}, uuid.Nil},
		{"admin, another tenant in a field that is not a name", admin, &datav1.UploadObjectRequest{
			Parent: collectionIn(target), Key: apiutil.TenantNamePrefix + other.String() + "/report.pdf",
		}, target},
		{"member, another tenant", member, &datav1.ListObjectsRequest{Parent: collectionIn(target)}, uuid.Nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, set := auth.ActingTenant(actingOnNamedTenant(tc.ctx, tc.msg))
			if tc.want == uuid.Nil {
				if set {
					t.Errorf("acting tenant set to %s, want none", got)
				}
				return
			}
			if !set || got != tc.want {
				t.Errorf("acting tenant = %s (set=%v), want %s", got, set, tc.want)
			}
		})
	}
}

// Every string field of a data-plane request is either a name the shim
// scopes by, or not a name. A new name-bearing field that is not added to
// nameFields would act on the admin's own tenant again, silently.
var notNameFields = map[protoreflect.Name]bool{
	"backend_id": true, "bucket": true, "checksum_value": true, "collections": true,
	"content_disposition": true, "content_type": true, "destination_key": true,
	"destination_key_template": true, "etag": true, "external_ref": true, "filter": true,
	"idempotency_key": true, "key": true, "keys": true, "mode": true, "order_by": true,
	"resource_version": true, "upload_id": true,
}

func TestDataPlaneNameFieldsAreKnown(t *testing.T) {
	seen := map[protoreflect.Name]bool{}
	var walk func(m protoreflect.MessageDescriptor, depth int)
	walk = func(m protoreflect.MessageDescriptor, depth int) {
		for i := 0; i < m.Fields().Len(); i++ {
			f := m.Fields().Get(i)
			switch {
			case f.Kind() == protoreflect.StringKind:
				seen[f.Name()] = true
				if !nameFields[f.Name()] && !notNameFields[f.Name()] {
					t.Errorf("%s.%s is a string the acting-tenant walk neither reads nor rules out", m.FullName(), f.Name())
				}
			case f.Kind() == protoreflect.MessageKind && !f.IsMap() &&
				f.Message().FullName().Parent() == datav1.File_paladin_data_v1_types_proto.Package() && depth < 3:
				walk(f.Message(), depth+1)
			}
		}
	}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != datav1.File_paladin_data_v1_types_proto.Package() {
			return true
		}
		for i := 0; i < fd.Services().Len(); i++ {
			svc := fd.Services().Get(i)
			for j := 0; j < svc.Methods().Len(); j++ {
				walk(svc.Methods().Get(j).Input(), 0)
			}
		}
		return true
	})
	for _, list := range []map[protoreflect.Name]bool{nameFields, notNameFields} {
		for name := range list {
			if !seen[name] {
				t.Errorf("%s is listed but no data-plane request has it — drop it", name)
			}
		}
	}
}

package resolve

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// fuzzCallerTenant is the tenant a bare name resolves under in the fuzz target.
var fuzzCallerTenant = uuid.MustParse("3a823fd4-0b3d-4ce2-a280-93b8d75cc07b")

// nameOf prints ref in the shape it was parsed from.
func nameOf(ref CanonicalRef) string {
	tenantPath := prefixTenant + ref.TenantID.String() + collectionsSep + ref.Collection
	switch ref.Shape {
	case ShapeCanonical:
		return prefixCanonical + ref.BackendID + bucketsSep + ref.BucketName + "/" + tenantPath
	case ShapeTenant:
		return tenantPath
	default:
		return ref.Collection
	}
}

// Any collection name a caller sends: refused, or resolved to fields that
// print back to a name resolving to the same, with the tenant every reader of
// the name agrees on — the audit trail and the acting-tenant interceptor read
// it with apiutil.TenantInResourceName, and a name whose text puts it under
// one tenant must not act on another.
func FuzzParseCollectionName(f *testing.F) {
	tid := fuzzCallerTenant.String()
	for _, s := range []string{
		"tenants/" + tid + "/collections/inv/2026",
		"storageBackends/primary/buckets/b1/tenants/" + tid + "/collections/inv/2026",
		"just-a-key",
		"tenants/not-a-uuid/collections/x",
		"tenants/" + tid + "/collections/",
		"storageBackends//buckets/b/tenants/" + tid + "/collections/c",
		"tenants/{" + tid + "}/collections/c",
		"",
	} {
		f.Add(s)
	}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u", TenantID: fuzzCallerTenant})
	f.Fuzz(func(t *testing.T, name string) {
		ref, err := ParseCollectionName(ctx, name)
		if err != nil {
			return
		}
		if ref.Collection == "" {
			t.Fatalf("%q: empty collection", name)
		}
		if ref.Shape == ShapeBare {
			if ref.TenantID != fuzzCallerTenant || ref.Collection != name {
				t.Fatalf("%q: a bare name resolved to %+v, want the caller's tenant and the name", name, ref)
			}
		} else if named, ok := apiutil.TenantInResourceName(name); ok && named != ref.TenantID {
			t.Fatalf("%q acts on tenant %s while its text names tenant %s", name, ref.TenantID, named)
		}
		if ref.Shape == ShapeCanonical {
			for _, seg := range []string{ref.BackendID, ref.BucketName} {
				if seg == "" || strings.Contains(seg, "/") {
					t.Fatalf("%q: backend or bucket %q is not one segment", name, seg)
				}
			}
		}
		again, err := ParseCollectionName(ctx, nameOf(ref))
		if err != nil || again != ref {
			t.Fatalf("%q: %q resolves back to (%+v, %v), want %+v", name, nameOf(ref), again, err, ref)
		}
	})
}

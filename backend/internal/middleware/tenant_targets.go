package middleware

import (
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// tenantNameFields hold "tenants/{tenant_id_or_slug}" on their own.
var tenantNameFields = map[protoreflect.Name]bool{
	"tenant": true,
}

// tenantIDFields hold a tenant by its bare id, or — owner_tenant_id — by id
// or slug.
var tenantIDFields = map[protoreflect.Name]bool{
	"tenant_id":       true,
	"owner_tenant_id": true,
	// The tenant a session switches into.
	"target_tenant_id": true,
}

// ignoredTenantFields name a tenant without acting on it: a simulated
// principal's tenant is an input to a what-if, not a target.
var ignoredTenantFields = map[protoreflect.Name]bool{
	"principal_tenant_id": true,
}

// tenantRefs are the tenants a request names: by id, and by slug where a
// name spells the tenant that way.
type tenantRefs struct {
	ids   map[uuid.UUID]bool
	slugs map[string]bool
}

// requestTenants collects every tenant a request names in a resource name or
// a tenant field, at any depth.
func requestTenants(m protoreflect.Message) tenantRefs {
	refs := tenantRefs{ids: map[uuid.UUID]bool{}, slugs: map[string]bool{}}
	walkStrings(m, func(field protoreflect.Name, value string) {
		switch {
		case value == "":
		case nameFields[field] || tenantNameFields[field]:
			refs.addName(value)
		case tenantIDFields[field]:
			refs.addRef(value)
		}
	})
	return refs
}

// addName adds the tenant of a resource name, if it names one.
func (r tenantRefs) addName(name string) {
	tenantsSegment := strings.TrimSuffix(apiutil.TenantNamePrefix, "/")
	parts := strings.Split(name, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == tenantsSegment {
			r.addRef(parts[i+1])
			return
		}
	}
}

// addRef adds a tenant spelt by id or by slug.
func (r tenantRefs) addRef(ref string) {
	if ref == "" {
		return
	}
	if id, err := uuid.Parse(ref); err == nil {
		r.ids[id] = true
		return
	}
	r.slugs[ref] = true
}

// walkStrings calls fn with every set string field of m, at any depth, list
// elements included.
func walkStrings(m protoreflect.Message, fn func(field protoreflect.Name, value string)) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
		case fd.Kind() == protoreflect.MessageKind && fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				walkStrings(list.Get(i).Message(), fn)
			}
		case fd.Kind() == protoreflect.MessageKind:
			walkStrings(v.Message(), fn)
		case fd.Kind() == protoreflect.StringKind && fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				fn(fd.Name(), list.Get(i).String())
			}
		case fd.Kind() == protoreflect.StringKind:
			fn(fd.Name(), v.String())
		}
		return true
	})
}

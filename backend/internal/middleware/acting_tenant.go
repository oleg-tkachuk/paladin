package middleware

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// nameFields are the data-plane request fields that hold resource names, at
// any depth (an ObjectSelector's `names` sits inside a batch request). They
// are the fields the data shim parses and scopes by (connectshim/data
// scopeToTenant); TestDataPlaneNameFieldsAreKnown holds the two lists
// together.
var nameFields = map[protoreflect.Name]bool{
	"name":                   true,
	"parent":                 true,
	"object_name":            true,
	"source_name":            true,
	"source_parent":          true,
	"destination_collection": true,
	"names":                  true,
}

// ActOnNamedTenant makes the tenant a platform admin's request names the
// tenant the request acts on, before anything that keys on it runs.
//
// The data shim does the same once the handler parses the name (ADR-0022),
// but the rate limiter and the idempotency store run before that and keyed
// on the admin's own tenant: an admin's uploads did not draw on the target's
// rate budget, and one Idempotency-Key used against two tenants collided.
// Placed after authentication and before both.
//
// Only a platform admin, and only when the names agree on one tenant other
// than its own. Anyone else naming another tenant is refused by the shim;
// names that span tenants are refused there too, so neither is decided here.
// Unary only: the data plane has no streaming RPC.
func ActOnNamedTenant() connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			return next(actingOnNamedTenant(ctx, req), spec, req)
		}
	}, nil)
}

func actingOnNamedTenant(ctx context.Context, msg any) context.Context {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || !p.HasRole(apiutil.RolePlatformAdmin) {
		return ctx
	}
	m, ok := msg.(proto.Message)
	if !ok {
		return ctx
	}
	tenants := map[uuid.UUID]bool{}
	namedTenants(m.ProtoReflect(), tenants)
	if len(tenants) != 1 {
		return ctx
	}
	for tenant := range tenants {
		if tenant != p.TenantID {
			return auth.WithActingTenant(ctx, tenant)
		}
	}
	return ctx
}

// namedTenants adds the tenant of every resource name in m to out — by id
// only: a slug is the shim's to resolve.
func namedTenants(m protoreflect.Message, out map[uuid.UUID]bool) {
	walkStrings(m, func(field protoreflect.Name, value string) {
		if !nameFields[field] {
			return
		}
		if tenant, ok := apiutil.TenantInResourceName(value); ok {
			out[tenant] = true
		}
	})
}

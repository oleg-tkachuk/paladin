package middleware

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// decidingPolicy answers every decision with one verdict.
type decidingPolicy cedar.Decision

func (d decidingPolicy) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.Decision(d), nil
}

// auditedCall runs one recorded call as p naming a user of tenant named,
// whose handler runs authorize first and answers err after it, and returns
// the row the interceptor wrote.
func auditedCall(t *testing.T, p *auth.Principal, named uuid.UUID,
	authorize func(context.Context) error, err error) (row struct {
	tenant uuid.UUID
	name   string
}) {
	t.Helper()
	ctx := context.Background()
	if p != nil {
		ctx = auth.WithPrincipal(ctx, p)
	}
	w := &recordingAuditWriter{}
	a := &auditInterceptor{w: w, audience: auth.AudienceAdmin}
	next := func(ctx context.Context, _ connect.Spec, _ proto.Message) (proto.Message, error) {
		if authorize != nil {
			if aerr := authorize(ctx); aerr != nil {
				return nil, aerr
			}
		}
		return nil, err
	}
	name := "tenants/" + named.String() + "/users/u"
	_, _ = a.unary(next)(ctx, auditedSpec, &iamv1.GetUserRequest{Name: name})
	if len(w.rows) != 1 {
		t.Fatalf("%d rows written, want 1", len(w.rows))
	}
	row.tenant, row.name = w.rows[0].ResourceTenant(), w.rows[0].ResourceName
	if row.name != name {
		t.Errorf("row names %q, want the target %q", row.name, name)
	}
	return row
}

// decideOn is a handler's policy check on a resource of tenant.
func decideOn(tenant uuid.UUID, verdict cedar.Decision) func(context.Context) error {
	return func(ctx context.Context) error {
		return apiutil.Decide(ctx, decidingPolicy(verdict), &cedar.Principal{}, cedar.ActionManageUser,
			&cedar.Resource{TenantID: tenant}, cedar.RequestContext{})
	}
}

// A row is filed in the tenant its request names only when the call's
// authorization admitted it there. A call refused before any admission — by
// authentication, by policy, by validation, for a resource that is not there
// — and a call that succeeded with no admission into the named tenant stay in
// the caller's own trail, the name recording the target it tried.
func TestAuditFilesARowOnlyInATenantTheCallerWasAdmittedTo(t *testing.T) {
	own, target := uuid.New(), uuid.New()
	agent := &auth.Principal{TenantID: own, Subject: "agent-7"}
	refused := func(code connect.Code) error { return connect.NewError(code, "refused") }
	cases := []struct {
		name      string
		principal *auth.Principal
		named     uuid.UUID
		authorize func(context.Context) error
		err       error
		want      uuid.UUID
	}{
		{"refused by authentication", nil, target, nil, refused(connect.CodeUnauthenticated), uuid.Nil()},
		{"refused by policy", agent, target, decideOn(target, cedar.DecisionDeny), nil, uuid.Nil()},
		{"refused by validation", agent, target, nil, refused(connect.CodeInvalidArgument), uuid.Nil()},
		{"refused as not found", agent, target, nil, refused(connect.CodeNotFound), uuid.Nil()},
		{"admitted to its own tenant, another named", agent, target, decideOn(own, cedar.DecisionAllow), nil, uuid.Nil()},
		{"succeeded with no admission, another named", agent, target, nil, nil, uuid.Nil()},
		{"succeeded with no admission, its own named", agent, own, nil, nil, own},
		{"refused in its own tenant", agent, own, decideOn(own, cedar.DecisionDeny), nil, own},
		{"admitted to the named tenant", agent, target, decideOn(target, cedar.DecisionAllow), nil, target},
		{"admitted, then failed", agent, target, decideOn(target, cedar.DecisionAllow), refused(connect.CodeFailedPrecondition), target},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := auditedCall(t, c.principal, c.named, c.authorize, c.err).tenant; got != c.want {
				t.Errorf("row filed under %s, want %s", got, c.want)
			}
		})
	}
}

// Every role whose authorization path lets it act in another tenant has its
// work there filed in that tenant's trail — by what admitted it, not by the
// role: the attribution holds no list of roles to drift from the gates. The
// same principal is filed in its own trail alone when nothing admitted it.
func TestAuditFilesCrossTenantWorkByAdmissionWhateverTheRole(t *testing.T) {
	own, target := uuid.New(), uuid.New()
	gate := func(ctx context.Context) error { apiutil.AdmitTenant(ctx, target); return nil }
	for _, role := range []string{
		apiutil.RolePlatformAdmin,
		apiutil.RoleTenantProvisioner,
		apiutil.RoleCapabilityIssuer,
		apiutil.RoleBucketAdmin,
	} {
		p := &auth.Principal{TenantID: own, Subject: "svc", Roles: []string{role}}
		t.Run(role, func(t *testing.T) {
			for name, authorize := range map[string]func(context.Context) error{
				"by policy": decideOn(target, cedar.DecisionAllow),
				"by a gate": gate,
			} {
				if got := auditedCall(t, p, target, authorize, nil).tenant; got != target {
					t.Errorf("admitted %s: filed under %s, want %s", name, got, target)
				}
			}
			if got := auditedCall(t, p, target, nil, nil).tenant; got != uuid.Nil() {
				t.Errorf("not admitted: filed under %s, want only its own trail", got)
			}
		})
	}
}

// A state reader runs as the caller before the change; a read it is allowed
// admits nothing to a change it is refused.
func TestAuditStateReadAdmitsNothing(t *testing.T) {
	own, target := uuid.New(), uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: own, Subject: "agent-7"})
	w := &recordingAuditWriter{}
	read := func(ctx context.Context, _ proto.Message) (proto.Message, error) {
		return nil, decideOn(target, cedar.DecisionAllow)(ctx)
	}
	a := &auditInterceptor{w: w, audience: auth.AudienceAdmin, readers: StateReaders{auditedSpec.Procedure: read}}
	next := func(ctx context.Context, _ connect.Spec, _ proto.Message) (proto.Message, error) {
		return nil, decideOn(target, cedar.DecisionDeny)(ctx)
	}
	_, _ = a.unary(next)(ctx, auditedSpec, &iamv1.GetUserRequest{Name: "tenants/" + target.String() + "/users/u"})
	if len(w.rows) != 1 {
		t.Fatalf("%d rows written, want 1", len(w.rows))
	}
	if got := w.rows[0].ResourceTenant(); got != uuid.Nil() {
		t.Errorf("row filed under %s, want only the caller's trail", got)
	}
}

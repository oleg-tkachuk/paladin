package middleware

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// The audit row takes its resource from the request's `name` or `parent`
// (resourceFromMessage) unless the handler stashes one. A recorded RPC whose
// request has neither leaves a row naming nothing, and such a row is in no
// tenant's trail but its actor's — so a platform admin's work inside a tenant
// does not show on that tenant's page. Every such RPC has to be one of two
// kinds, and this list is where a new one is decided.
var (
	// The handler stashes the resource it acted on (apiutil.StashResource).
	namesItsResource = map[string]bool{
		"/paladin.admin.v1.BackendService/CreateBackend":    true,
		"/paladin.admin.v1.TenantService/CreateTenant":      true,
		"/paladin.admin.v1.TenantBudgetService/Set":         true,
		"/paladin.admin.v1.CapabilityService/Issue":         true,
		"/paladin.admin.v1.CapabilityService/Delegate":      true,
		"/paladin.admin.v1.CapabilityService/Revoke":        true,
		"/paladin.admin.v1.CapabilityService/RevokeBiscuit": true,
	}
	// The caller acts on itself — its session, password or settings — so the
	// row belongs in its own tenant's trail, which the actor tenant gives it.
	actsOnTheCaller = map[string]bool{
		"/paladin.iam.v1.UserSettingsService/UpdateMine": true,
		"/paladin.iam.v1.AuthService/Revoke":             true,
		"/paladin.iam.v1.AuthService/ChangePassword":     true,
		"/paladin.iam.v1.AuthService/SwitchTenant":       true,
	}
)

// auditedPlanes name, by one file each, the proto packages whose services run
// behind the audit interceptor. A walk that found nothing would pass the loop
// below; the check that every listed RPC was seen is what catches it.
var auditedPlanes = []protoreflect.FileDescriptor{
	adminv1.File_paladin_admin_v1_audit_service_proto,
	iamv1.File_paladin_iam_v1_auth_service_proto,
}

func TestEveryRecordedRPCNamesItsResource(t *testing.T) {
	a := &auditInterceptor{}
	seen := map[string]bool{}
	packages := map[protoreflect.FullName]bool{}
	for _, fd := range auditedPlanes {
		packages[fd.Package()] = true
	}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !packages[fd.Package()] {
			return true
		}
		for i := 0; i < fd.Services().Len(); i++ {
			svc := fd.Services().Get(i)
			for j := 0; j < svc.Methods().Len(); j++ {
				m := svc.Methods().Get(j)
				procedure := "/" + string(svc.FullName()) + "/" + string(m.Name())
				if a.shouldSkip(connect.Spec{Procedure: procedure, IdempotencyLevel: idempotencyOf(m)}) {
					continue
				}
				in := m.Input().Fields()
				if in.ByName("name") != nil || in.ByName("parent") != nil {
					continue
				}
				seen[procedure] = true
				if !namesItsResource[procedure] && !actsOnTheCaller[procedure] {
					t.Errorf("%s is recorded, its request has no name or parent, and it is "+
						"in neither list: stash its resource in the handler, or say it acts on the caller", procedure)
				}
			}
		}
		return true
	})
	for _, list := range []map[string]bool{namesItsResource, actsOnTheCaller} {
		for procedure := range list {
			if !seen[procedure] {
				t.Errorf("%s is listed but is not a recorded RPC without a name — drop it", procedure)
			}
		}
	}
}

// idempotencyOf reads the contract's idempotency option as connect reports it
// on a handler's Spec.
func idempotencyOf(m protoreflect.MethodDescriptor) connect.IdempotencyLevel {
	opts, _ := m.Options().(*descriptorpb.MethodOptions)
	switch opts.GetIdempotencyLevel() {
	case descriptorpb.MethodOptions_NO_SIDE_EFFECTS:
		return connect.IdempotencyNoSideEffects
	case descriptorpb.MethodOptions_IDEMPOTENT:
		return connect.IdempotencyIdempotent
	}
	return connect.IdempotencyUnknown
}

// recordingAuditWriter keeps the rows the interceptor writes.
type recordingAuditWriter struct {
	rows []admindomain.AuditEntry
	ctxs []context.Context
}

func (w *recordingAuditWriter) Insert(ctx context.Context, e admindomain.AuditEntry) error {
	w.rows = append(w.rows, e)
	w.ctxs = append(w.ctxs, ctx)
	return nil
}

func (w *recordingAuditWriter) InsertWithOutbox(ctx context.Context, e admindomain.AuditEntry,
	_ func(context.Context, pgx.Tx) error) error {
	return w.Insert(ctx, e)
}

// What a handler stashes is what the row names — before the request's own
// name, which for these RPCs is absent or names something else.
func TestAuditRowNamesTheResourceTheHandlerStashed(t *testing.T) {
	const stashed = "tenants/01a0fcfc-281e-7c59-963c-8a946fe82a47/budget"
	cases := map[string]struct {
		stash string
		want  string
	}{
		"stashed":         {stash: stashed, want: stashed},
		"nothing stashed": {want: "tenants/from-the-request"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := &recordingAuditWriter{}
			a := &auditInterceptor{w: w, audience: "paladin-admin"}
			next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
				apiutil.StashResource(ctx, tc.stash)
				return nil, nil
			}
			req := connect.NewRequest(&iamv1.GetUserRequest{Name: "tenants/from-the-request"})
			if _, err := a.WrapUnary(next)(context.Background(), req); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(w.rows) != 1 {
				t.Fatalf("%d rows written, want 1", len(w.rows))
			}
			if got := w.rows[0].ResourceName; got != tc.want {
				t.Errorf("row names %q, want %q", got, tc.want)
			}
		})
	}
}

// On the data plane only a principal's calls inside another tenant are
// recorded: those are what the tenant's trail would miss. Its own
// principals' writes, and an admin's in its own tenant, are not.
func TestAuditActingElsewhere(t *testing.T) {
	own, target := uuid.New(), uuid.New()
	admin := auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: own, Subject: "ops", Roles: []string{apiutil.RolePlatformAdmin},
	})
	cases := map[string]struct {
		ctx  context.Context
		rows int
	}{
		"inside another tenant":      {auth.WithActingTenant(admin, target), 1},
		"inside its own tenant":      {admin, 0},
		"acting on its own, by name": {auth.WithActingTenant(admin, own), 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := &recordingAuditWriter{}
			call := AuditActingElsewhere(w, auth.AudienceData).WrapUnary(
				func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) { return nil, nil })
			if _, err := call(tc.ctx, connect.NewRequest(&adminv1.ListAuditLogRequest{})); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(w.rows) != tc.rows {
				t.Fatalf("%d rows written, want %d", len(w.rows), tc.rows)
			}
			// audit_log admits a row only under its actor's tenant, so the
			// insert must not run in the tenant the call acted on.
			for _, ctx := range w.ctxs {
				if scope, _ := auth.EffectiveTenant(ctx); scope != own {
					t.Errorf("row inserted under %s, want the actor's tenant %s", scope, own)
				}
			}
		})
	}
}

//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	pbdata "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// actingTenantCallTimeout bounds each call through the assembled data mux.
const actingTenantCallTimeout = 20 * time.Second

// actingTenantFixture is the assembled data plane on the RLS-enforced app role,
// with three tenants: the platform admin's own, a target the admin acts on, and
// an unrelated one whose member must not reach the target. The admin's tenant
// and the target each hold a collection named alike, so an answer from the
// wrong tenant is visible rather than empty.
type actingTenantFixture struct {
	h                       *pgharness.Harness
	objects                 paladindatav1connect.ObjectServiceClient
	mint                    func(tenant uuid.UUID, roles ...string) string
	platform, target, other uuid.UUID
	inTarget, inPlatform    uuid.UUID
}

const actingCollection = "docs"

func newActingTenantFixture(t *testing.T) actingTenantFixture {
	t.Helper()
	ctx := context.Background()
	h := pgharness.Setup(t)
	f := actingTenantFixture{
		h:        h,
		platform: mustCreateTenant(t, h.PoolMigrate, "acting-platform"),
		target:   mustCreateTenant(t, h.PoolMigrate, "acting-target"),
		other:    mustCreateTenant(t, h.PoolMigrate, "acting-other"),
	}
	for _, tenant := range []uuid.UUID{f.platform, f.target} {
		mustCreateCollection(t, h.PoolMigrate, tenant, actingCollection)
	}
	f.inTarget = mustInsertAvailableObject(t, h.PoolMigrate, f.target, actingCollection, "target.txt")
	f.inPlatform = mustInsertAvailableObject(t, h.PoolMigrate, f.platform, actingCollection, "platform.txt")

	cfg := wiringConfig(t, h.MigrateDSN)
	cfg.Datastores.Postgres.DSN = h.AppDSN // the runtime role, NOBYPASSRLS
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop(), postgres.WithRLS())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)
	deps, err := app.BuildSharedDeps(ctx, cfg, db, zap.NewNop())
	if err != nil {
		t.Fatalf("BuildSharedDeps: %v", err)
	}
	t.Cleanup(deps.StopWatchers)
	dataMux, _, _, err := app.AssembleAPIMuxes(ctx, deps, app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleAPIMuxes: %v", err)
	}
	srv := httptest.NewServer(dataMux)
	t.Cleanup(srv.Close)
	f.objects = paladindatav1connect.NewObjectServiceClient(http.DefaultClient, srv.URL)

	iss, err := issuer.New(issuer.Config{
		Issuer: cfg.Auth.Issuer, SigningKey: []byte(wiringSigningKey), AccessTokenTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	f.mint = func(tenant uuid.UUID, roles ...string) string {
		token, _, err := iss.MintAccess(issuer.AccessClaims{
			Subject: "acting@test", TenantID: tenant, Audience: auth.AudienceData,
			Roles: roles, Kind: auth.PrincipalKindUser,
		})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		return token
	}
	return f
}

func authed[T any](token string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)
	return req
}

func collectionOf(tenant uuid.UUID) string {
	return "tenants/" + tenant.String() + "/collections/" + actingCollection
}

func objectOf(tenant, object uuid.UUID) string {
	return collectionOf(tenant) + "/objects/" + object.String()
}

// A platform admin naming another tenant acts on that tenant: it lists the
// target's objects — not its own same-named collection's — and its writes land
// in the target's rows.
func TestDataPlaneAdminActsOnTheNamedTenant(t *testing.T) {
	f := newActingTenantFixture(t)
	admin := f.mint(f.platform, "platform.admin")
	ctx, cancel := context.WithTimeout(context.Background(), actingTenantCallTimeout)
	defer cancel()

	list, err := f.objects.ListObjects(ctx, authed(admin, &pbdata.ListObjectsRequest{Parent: collectionOf(f.target)}))
	if err != nil {
		t.Fatalf("ListObjects on the target: %v", err)
	}
	var names []string
	for _, o := range list.Msg.GetObjects() {
		names = append(names, o.GetName())
	}
	if len(names) != 1 || names[0] != objectOf(f.target, f.inTarget) {
		t.Fatalf("admin listed %v, want only the target's object %s", names, objectOf(f.target, f.inTarget))
	}

	if _, err := f.objects.UpdateObject(ctx, authed(admin, &pbdata.UpdateObjectRequest{
		Name:            objectOf(f.target, f.inTarget),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		Tags:            map[string]string{"set-by": "platform-admin"},
	})); err != nil {
		t.Fatalf("UpdateObject on the target: %v", err)
	}
	if got := mustObjectTags(t, f.h.PoolMigrate, f.inTarget); got["set-by"] != "platform-admin" {
		t.Errorf("target object tags = %v, want the admin's write", got)
	}
	if got := mustObjectTags(t, f.h.PoolMigrate, f.inPlatform); len(got) != 0 {
		t.Errorf("the admin's own same-named object was written: %v", got)
	}

	// On its own tenant nothing changes.
	own, err := f.objects.ListObjects(ctx, authed(admin, &pbdata.ListObjectsRequest{Parent: collectionOf(f.platform)}))
	if err != nil || len(own.Msg.GetObjects()) != 1 || own.Msg.GetObjects()[0].GetName() != objectOf(f.platform, f.inPlatform) {
		t.Fatalf("admin on its own tenant: %v, %v", own, err)
	}
}

// Everyone else stays on its own tenant, and no request may name two.
func TestDataPlaneTenantBoundaries(t *testing.T) {
	f := newActingTenantFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), actingTenantCallTimeout)
	defer cancel()
	member := f.mint(f.other, "tenant.admin")
	admin := f.mint(f.platform, "platform.admin")

	cases := map[string]error{}
	_, cases["a member listing another tenant"] = f.objects.ListObjects(ctx,
		authed(member, &pbdata.ListObjectsRequest{Parent: collectionOf(f.target)}))
	_, cases["a member reading another tenant's version"] = f.objects.GetObjectVersion(ctx,
		authed(member, &pbdata.GetObjectVersionRequest{Name: objectOf(f.target, f.inTarget) + "/versions/" + uuid.NewString()}))
	_, cases["an admin copying across tenants"] = f.objects.CopyObject(ctx,
		authed(admin, &pbdata.CopyObjectRequest{
			SourceName: objectOf(f.target, f.inTarget), DestinationCollection: collectionOf(f.platform), DestinationKey: "stolen.txt",
		}))
	for name, err := range cases {
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: code = %v (%v), want PermissionDenied", name, connect.CodeOf(err), err)
		}
	}
}

// The idempotency store keys a response on the tenant the request acts on.
// It keyed on the caller's own, so a platform admin who reused one
// Idempotency-Key in two tenants had the second call answered with the
// first one's response, and the second write never happened.
func TestDataPlaneAdminIdempotencyKeyIsPerTenant(t *testing.T) {
	f := newActingTenantFixture(t)
	admin := f.mint(f.platform, "platform.admin")
	ctx, cancel := context.WithTimeout(context.Background(), actingTenantCallTimeout)
	defer cancel()

	const key = "admin-key-reused-across-tenants"
	for _, c := range []struct {
		tenant, object uuid.UUID
	}{{f.target, f.inTarget}, {f.platform, f.inPlatform}} {
		req := authed(admin, &pbdata.UpdateObjectRequest{
			Name:            objectOf(c.tenant, c.object),
			ResourceVersion: "1",
			UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			Tags:            map[string]string{"set-by": "platform-admin"},
		})
		req.Header().Set("Idempotency-Key", key)
		resp, err := f.objects.UpdateObject(ctx, req)
		if err != nil {
			t.Fatalf("UpdateObject in %s: %v", c.tenant, err)
		}
		if got, want := resp.Msg.GetName(), objectOf(c.tenant, c.object); got != want {
			t.Errorf("answered with %s, want %s — the key replayed another tenant's response", got, want)
		}
		if got := mustObjectTags(t, f.h.PoolMigrate, c.object); got["set-by"] != "platform-admin" {
			t.Errorf("object %s was not written: tags %v", c.object, got)
		}
	}

	// And within the tenant the key still does its job: a retry — the same
	// request — is answered from the first call, not run again. Keyed on the
	// caller while the connection is scoped to the target, the row was
	// refused by RLS and a retry ran the write twice.
	retry := authed(admin, &pbdata.UpdateObjectRequest{
		Name:            objectOf(f.target, f.inTarget),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		Tags:            map[string]string{"set-by": "platform-admin"},
	})
	retry.Header().Set("Idempotency-Key", key)
	if _, err := f.objects.UpdateObject(ctx, retry); err != nil {
		t.Fatalf("retry: %v", err)
	}

	// The key sent with a different request is refused, and writes nothing.
	other := authed(admin, &pbdata.UpdateObjectRequest{
		Name:            objectOf(f.target, f.inTarget),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		Tags:            map[string]string{"set-by": "a-different-request-that-must-not-run"},
	})
	other.Header().Set("Idempotency-Key", key)
	if _, err := f.objects.UpdateObject(ctx, other); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a different request under the key: %v, want InvalidArgument", err)
	}
	if got := mustObjectTags(t, f.h.PoolMigrate, f.inTarget); got["set-by"] != "platform-admin" {
		t.Errorf("a request under a reused key ran: tags %v", got)
	}
}

// A platform admin's data-plane work inside another tenant is in that
// tenant's trail; its work in its own tenant, like any tenant's own
// data-plane writes, is not audited.
func TestDataPlaneAdminWorkIsInTheTenantsTrail(t *testing.T) {
	f := newActingTenantFixture(t)
	admin := f.mint(f.platform, "platform.admin")
	ctx, cancel := context.WithTimeout(context.Background(), actingTenantCallTimeout)
	defer cancel()

	for _, c := range []struct {
		tenant, object uuid.UUID
	}{{f.target, f.inTarget}, {f.platform, f.inPlatform}} {
		if _, err := f.objects.UpdateObject(ctx, authed(admin, &pbdata.UpdateObjectRequest{
			Name:            objectOf(c.tenant, c.object),
			ResourceVersion: "1",
			UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			Tags:            map[string]string{"set-by": "platform-admin"},
		})); err != nil {
			t.Fatalf("UpdateObject in %s: %v", c.tenant, err)
		}
	}

	type row struct {
		action, resource string
		actor            uuid.UUID
	}
	rowsUnder := func(tenant uuid.UUID) []row {
		rows, err := f.h.PoolMigrate.Query(ctx,
			`SELECT action, resource_name, actor_tenant_id FROM audit_log
			 WHERE resource_tenant_id = $1 AND action LIKE '%ObjectService/UpdateObject'`, tenant)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.action, &r.resource, &r.actor); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, r)
		}
		return out
	}

	got := rowsUnder(f.target)
	if len(got) != 1 {
		t.Fatalf("%d rows in the target's trail, want 1", len(got))
	}
	if got[0].actor != f.platform || got[0].resource != objectOf(f.target, f.inTarget) {
		t.Errorf("row = %+v, want the admin's tenant as actor and the object as resource", got[0])
	}
	if own := rowsUnder(f.platform); len(own) != 0 {
		t.Errorf("the admin's work in its own tenant was audited: %+v", own)
	}
}

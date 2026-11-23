//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/protonul"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	pbadmin "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	pbdata "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	pbiam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// nulCallTimeout bounds each call of the NUL refusal tests.
const nulCallTimeout = 20 * time.Second

// nulText is request text holding the NUL Postgres cannot store.
const nulText = "a" + string(protonul.NUL) + "b"

// nulPlatformAdmin is the role the admin-plane case calls with, so nothing
// short of the NUL refusal would refuse the call before its handler.
const nulPlatformAdmin = "platform.admin"

// wantNULRefusal fails t unless err is the InvalidArgument naming path.
func wantNULRefusal(t *testing.T, err error, path string) {
	t.Helper()
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok || ce.Code() != connect.CodeInvalidArgument {
		t.Fatalf("err = %v (code %v), want InvalidArgument", err, connect.CodeOf(err))
	}
	if want := fmt.Sprintf(protonul.RefusalFormat, path); ce.Message() != want {
		t.Fatalf("message = %q, want %q", ce.Message(), want)
	}
}

// Every plane, as the process assembles it, refuses request text holding NUL
// as InvalidArgument naming the field, instead of handing it to Postgres.

func TestDataPlaneRefusesNUL(t *testing.T) {
	f := newActingTenantFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), nulCallTimeout)
	defer cancel()
	member := f.mint(f.target, collectionAdminRole)

	_, err := f.objects.UploadObject(authed(ctx, member), &pbdata.UploadObjectRequest{
		Parent: collectionOf(f.target), Key: nulText, ContentType: "text/plain", ChecksumValue: "AAAAAA==",
	})
	wantNULRefusal(t, err, "key")

	_, err = f.objects.UpdateObject(authed(ctx, member), &pbdata.UpdateObjectRequest{
		Name:            objectOf(f.target, f.inTarget),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		Tags:            map[string]string{"owner": nulText},
	})
	wantNULRefusal(t, err, "tags.value")
	if got := mustObjectTags(t, f.h.PoolMigrate, f.inTarget); len(got) != 0 {
		t.Errorf("the refused write reached the store: tags %v", got)
	}
}

// Login is permissive: the refusal reaches a caller with no principal, ahead
// of the audit that would store the subject in jsonb.
func TestIAMPlaneRefusesNUL(t *testing.T) {
	h := pgharness.Setup(t)
	_, iamMux := assembleAPIMuxes(t, h, nil)
	srv := httptest.NewServer(iamMux)
	t.Cleanup(srv.Close)
	client := paladiniamv1connect.NewAuthServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, srv.URL)))
	ctx, cancel := context.WithTimeout(context.Background(), nulCallTimeout)
	defer cancel()

	_, err := client.Login(ctx, &pbiam.LoginRequest{Subject: nulText, Password: "irrelevant"})
	wantNULRefusal(t, err, "subject")
}

func TestAdminPlaneRefusesNUL(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "nul-admin")
	mux, keys := assembleAdminMux(t, h, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	iss, err := issuer.New(issuer.Config{Issuer: wiringConfig(t, h.MigrateDSN).Auth.Issuer, Keys: keys, AccessTokenTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: "nul@test", TenantID: tenant, Audience: auth.AudienceAdmin,
		Roles: []string{nulPlatformAdmin}, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	client := paladinadminv1connect.NewTenantServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, srv.URL)))
	ctx, cancel := context.WithTimeout(context.Background(), nulCallTimeout)
	defer cancel()

	_, err = client.UpdateTenant(authed(ctx, token), &pbadmin.UpdateTenantRequest{
		Name:            "tenants/" + tenant.String(),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
		Tenant:          &pbadmin.Tenant{Labels: map[string]string{"team": nulText}},
	})
	wantNULRefusal(t, err, "tenant.labels.value")
}

//go:build integration

package integration

// The data and iam muxes, asked the same question as the admin one: which
// services does the process actually serve?
//
// The data plane is where every client lives, and the failure is the quietest
// in the system — a service that was never mounted answers 404 from the
// default mux, so it looks like a bad URL rather than a missing feature.
//
// The OAuth endpoints are the conditional half. They are raw HTTP rather than
// Connect, mounted only when auth.oauth.enabled, and /oauth/register is
// mounted only when dynamic registration is on within that. Three states, and
// the middle one — OAuth on, registration off — is the one a single boolean
// would collapse.

import (
	"context"
	"net/http"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

func servicesInProtoPackage(t *testing.T, pkg string) []string {
	t.Helper()
	var out []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if string(fd.Package()) != pkg {
			return true
		}
		svcs := fd.Services()
		for i := range svcs.Len() {
			out = append(out, string(svcs.Get(i).FullName()))
		}
		return true
	})
	if len(out) == 0 {
		t.Fatalf("no %s services in the proto registry — this test would assert nothing", pkg)
	}
	return out
}

func assembleAPIMuxes(t *testing.T, h *pgharness.Harness, mutate func(*config.Config)) (dataMux, iamMux *http.ServeMux) {
	t.Helper()
	ctx := context.Background()

	cfg := wiringConfig(t, h.MigrateDSN)
	if mutate != nil {
		mutate(&cfg)
	}
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)

	deps, err := app.BuildSharedDeps(ctx, cfg, db, zap.NewNop())
	if err != nil {
		t.Fatalf("BuildSharedDeps: %v", err)
	}
	t.Cleanup(deps.StopWatchers)

	dataMux, iamMux, _, err = app.AssembleAPIMuxes(ctx, deps, app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleAPIMuxes: %v", err)
	}
	return dataMux, iamMux
}

func TestAPIMuxes_MountEveryService(t *testing.T) {
	h := pgharness.Setup(t)
	dataMux, iamMux := assembleAPIMuxes(t, h, nil)

	for _, tc := range []struct {
		plane string
		pkg   string
		mux   *http.ServeMux
	}{
		{"data", "paladin.data.v1", dataMux},
		{"iam", "paladin.iam.v1", iamMux},
	} {
		t.Run(tc.plane, func(t *testing.T) {
			for _, svc := range servicesInProtoPackage(t, tc.pkg) {
				if !routed(t, tc.mux, "/"+svc+"/AnyMethod") {
					t.Errorf("%s is not mounted on the %s mux — callers get a default-mux 404",
						svc, tc.plane)
				}
			}
		})
	}

	// Each plane serves only its own package. A data service reachable on the
	// iam listener would be reachable with an iam-audience token, which is a
	// different authorisation decision than the one its handler expects.
	for _, svc := range servicesInProtoPackage(t, "paladin.data.v1") {
		if routed(t, iamMux, "/"+svc+"/AnyMethod") {
			t.Errorf("%s is reachable on the iam mux; the planes carry different audiences", svc)
		}
	}
	for _, svc := range servicesInProtoPackage(t, "paladin.iam.v1") {
		if routed(t, dataMux, "/"+svc+"/AnyMethod") {
			t.Errorf("%s is reachable on the data mux; the planes carry different audiences", svc)
		}
	}
}

func TestAPIMuxes_OAuthEndpointsFollowTheirFlags(t *testing.T) {
	h := pgharness.Setup(t)

	const (
		authorize = "/oauth/authorize"
		token     = "/oauth/token"
		register  = "/oauth/register"
	)

	cases := []struct {
		name         string
		set          func(*config.Config)
		wantCore     bool
		wantRegister bool
	}{
		{
			name:     "off",
			set:      func(c *config.Config) { c.Auth.OAuth.Enabled = false },
			wantCore: false, wantRegister: false,
		},
		{
			// The state a single flag would collapse: an authorization server
			// that does not let anyone register a client at runtime.
			name: "on without dynamic registration",
			set: func(c *config.Config) {
				c.Auth.OAuth.Enabled = true
				c.Auth.OAuth.DynamicRegistration = false
			},
			wantCore: true, wantRegister: false,
		},
		{
			name: "on with dynamic registration",
			set: func(c *config.Config) {
				c.Auth.OAuth.Enabled = true
				c.Auth.OAuth.DynamicRegistration = true
			},
			wantCore: true, wantRegister: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, iamMux := assembleAPIMuxes(t, h, tc.set)
			for _, path := range []string{authorize, token} {
				if got := routed(t, iamMux, path); got != tc.wantCore {
					t.Errorf("%s routed = %v, want %v", path, got, tc.wantCore)
				}
			}
			if got := routed(t, iamMux, register); got != tc.wantRegister {
				t.Errorf("%s routed = %v, want %v", register, got, tc.wantRegister)
			}
		})
	}
}

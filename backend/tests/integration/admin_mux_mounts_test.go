//go:build integration

package integration

// Which services does the admin plane actually serve?
//
// Every mount is one `mux.Handle` line, and a missing one is invisible: the
// route falls through to the default mux and answers 404. Nothing fails to
// build, no test notices, and the first report is an operator saying a page
// in the console is broken.
//
// Two mounts carry more than availability.
//
//   - JWKS is deliberately absent when the capability subsystem is off. The
//     comment on it says why: an empty {"keys": []} tells a scanner the
//     endpoint exists and the deployment has no keys yet. Mounting it
//     unconditionally would be a one-line "simplification".
//   - CapabilityService and APITokenService are mounted in BOTH states — the
//     real handler when the subsystem is on, the disabled stub when it is
//     off — so a caller gets Unimplemented plus X-Paladin-Reason instead of a
//     404 that cannot distinguish "turned off" from "not in this build".
//     internal/app holds what the stub returns; only here can we see that the
//     stub is what the mux was given.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	adminv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// adminServiceNames enumerates the admin plane from the proto registry rather
// than a list kept by hand, so a service added to paladin.admin.v1 is covered
// the moment it is generated.
func adminServiceNames(t *testing.T) []string {
	t.Helper()
	var out []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if string(fd.Package()) != "paladin.admin.v1" {
			return true
		}
		svcs := fd.Services()
		for i := range svcs.Len() {
			out = append(out, string(svcs.Get(i).FullName()))
		}
		return true
	})
	if len(out) == 0 {
		t.Fatal("no paladin.admin.v1 services in the proto registry — this test would assert nothing")
	}
	return out
}

func assembleAdminMux(t *testing.T, h *pgharness.Harness, mutate func(*config.Config)) *http.ServeMux {
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
	// Registered after db.Close so it runs before it — the policy engine and
	// the audit hub both hold pooled connections until cancelled, and
	// db.Close blocks on them. See app_wiring_test.go for the afternoon this
	// cost the first time.
	t.Cleanup(deps.StopWatchers)

	mux, _, err := app.AssembleAdminMux(ctx, deps, app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleAdminMux: %v", err)
	}
	return mux
}

func routed(t *testing.T, mux *http.ServeMux, path string) bool {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
	_, pattern := mux.Handler(req)
	return pattern != ""
}

func TestAdminMux_MountsEveryAdminService(t *testing.T) {
	h := pgharness.Setup(t)
	services := adminServiceNames(t)

	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{"subsystems disabled", false},
		{"subsystems enabled", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := assembleAdminMux(t, h, func(c *config.Config) {
				c.Capability.Enabled = tc.enabled
				c.Capability.IssuerName = "test-issuer"
				c.APIToken.Enabled = tc.enabled
			})

			for _, svc := range services {
				if !routed(t, mux, "/"+svc+"/AnyMethod") {
					t.Errorf("%s is not mounted — callers get a default-mux 404", svc)
				}
			}

			// The JWKS route is the one mount that is meant to disappear.
			const jwks = "/.well-known/jwks.json"
			switch got := routed(t, mux, jwks); {
			case tc.enabled && !got:
				t.Error("JWKS is not served with the capability subsystem on; verifiers in other pods cannot fetch the key set")
			case !tc.enabled && got:
				t.Error("JWKS is served with the capability subsystem off; an empty key set tells a scanner the endpoint exists")
			}
		})
	}
}

// With the subsystem off the route exists — the test above proves that much —
// but route presence cannot tell which handler is behind it. This asks.
func TestAdminMux_DisabledSubsystemAnswersThroughTheMux(t *testing.T) {
	h := pgharness.Setup(t)
	mux := assembleAdminMux(t, h, func(c *config.Config) {
		c.Capability.Enabled = false
		c.APIToken.Enabled = false
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := wiringConfig(t, h.MigrateDSN)
	iss, err := issuer.New(issuer.Config{
		Issuer: cfg.Auth.Issuer, SigningKey: []byte(wiringSigningKey),
		AccessTokenTTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: "mount@test", Audience: auth.AudienceAdmin,
		Roles: []string{"platform.admin"}, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client := paladinadminv1connect.NewCapabilityServiceClient(http.DefaultClient, srv.URL)
	// The validating interceptor runs before the handler, so the request has
	// to be well-formed or the answer is InvalidArgument and says nothing
	// about which handler is mounted.
	req := connect.NewRequest(&adminv1.CapabilityServiceListRequest{
		TenantId:      uuid.NewString(),
		PrincipalKind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER,
		Subject:       "agent-1",
	})
	req.Header().Set("Authorization", "Bearer "+token)

	_, err = client.List(ctx, req)
	if err == nil {
		t.Fatal("CapabilityService.List succeeded with the subsystem disabled")
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *connect.Error", err)
	}
	if ce.Code() != connect.CodeUnimplemented {
		t.Fatalf("code = %v, want Unimplemented — a 404 or Unauthenticated here means the stub is not what the mux was given", ce.Code())
	}
	if got := ce.Meta().Get(app.HeaderReason); got != app.ReasonDisabled {
		t.Errorf("%s = %q, want %q", app.HeaderReason, got, app.ReasonDisabled)
	}
	if got := ce.Meta().Get(app.HeaderSubsystem); got != "capability" {
		t.Errorf("%s = %q, want capability", app.HeaderSubsystem, got)
	}
}

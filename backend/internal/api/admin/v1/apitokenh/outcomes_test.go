package apitokenh

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// stubPlaintext is what the stub issuer hands back as the secret, so a test
// can show it never reaches a log line.
const stubPlaintext = "paladin_pat_secret123"

// unlimited is the rate-limit choice every Create must make.
var unlimited = &adminv1.APITokenServiceCreateRequest_UnlimitedRate{UnlimitedRate: true}

func logged(iss tokenIssuer, store api_token.Store) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := newHandler(iss, store, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

// A request the issuer refuses stays invalid; an issuer that fails is an
// internal error with a line saying why — it used to read as the caller's
// mistake.
func TestCreateTellsARefusalFromAFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  connect.Code
		level zapcore.Level
	}{
		{"refused", fmt.Errorf("%w: TTL too long", api_token.ErrInvalidIssue), connect.CodeInvalidArgument, zapcore.WarnLevel},
		{"failed", errors.New("api_token: persist: connection reset"), connect.CodeInternal, zapcore.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ctxAs(apiutil.RolePlatformAdmin)
			p, _ := auth.PrincipalFromContext(ctx)
			h, logs := logged(&stubIssuer{err: tc.err}, &fakeStore{})
			_, err := h.Create(ctx, &adminv1.APITokenServiceCreateRequest{
				Parent: "tenants/" + p.TenantID.String(), DisplayName: "svc", Audience: []string{"data"}, RateLimit: unlimited,
			})
			if code(err) != tc.code {
				t.Fatalf("code = %v, want %v", code(err), tc.code)
			}
			if logs.FilterLevelExact(tc.level).Len() != 1 {
				t.Fatalf("want one %v line, got %v", tc.level, logs.All())
			}
		})
	}
}

// Minting and revoking a credential are logged with whose and which, and the
// secret is in no line.
func TestCredentialChangesAreLoggedWithoutTheSecret(t *testing.T) {
	ctx := ctxAs(apiutil.RolePlatformAdmin)
	p, _ := auth.PrincipalFromContext(ctx)
	store := &fakeStore{}
	h, logs := logged(&stubIssuer{}, store)

	res, err := h.Create(ctx, &adminv1.APITokenServiceCreateRequest{
		Parent: "tenants/" + p.TenantID.String(), DisplayName: "svc", Audience: []string{"data"}, RateLimit: unlimited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Revoke(ctx, &adminv1.APITokenServiceRevokeRequest{Name: res.GetApiToken().GetName()}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"api token issued", "api token revoked"} {
		lines := logs.FilterMessage(msg).All()
		if len(lines) != 1 {
			t.Fatalf("want one %q line, got %v", msg, logs.All())
		}
		f := lines[0].ContextMap()
		if f["tenant_id"] != p.TenantID.String() || f["token_id"] == nil || f["actor"] != p.Subject {
			t.Errorf("%q = %v, want tenant, token and actor", msg, f)
		}
	}
	for _, e := range logs.All() {
		if strings.Contains(fmt.Sprint(e.ContextMap()), stubPlaintext) || strings.Contains(e.Message, stubPlaintext) {
			t.Fatalf("the plaintext token is in a log line: %v", e)
		}
	}
}

// A grant only a platform admin may make is refused with a warning naming
// who asked.
func TestRefusedGrantsAreLogged(t *testing.T) {
	ctx := ctxAs(apiutil.RoleTenantAdmin)
	p, _ := auth.PrincipalFromContext(ctx)
	h, logs := logged(&stubIssuer{}, &fakeStore{})
	for _, req := range []*adminv1.APITokenServiceCreateRequest{
		{Parent: "tenants/" + uuid.New().String(), DisplayName: "svc", Audience: []string{"data"}, RateLimit: unlimited},
		{Parent: "tenants/" + p.TenantID.String(), DisplayName: "svc", Audience: []string{"data"}, RateLimit: unlimited, Roles: []string{apiutil.RolePlatformAdmin}},
	} {
		if _, err := h.Create(ctx, req); code(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied", code(err))
		}
	}
	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	if len(warns) != 2 {
		t.Fatalf("want two warnings, got %v", logs.All())
	}
	for _, w := range warns {
		if w.ContextMap()["actor"] != p.Subject {
			t.Errorf("%q names actor %v, want %s", w.Message, w.ContextMap()["actor"], p.Subject)
		}
	}
}

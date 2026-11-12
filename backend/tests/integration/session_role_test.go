//go:build integration

package integration

// The console's session role between the real iam and data roles (ADR-0048):
// the browser holds a cookie and nothing else, and every call it makes reaches
// the data plane as its user.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/session"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	pbdata "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

const (
	sessionTestOrigin   = "https://console.test"
	sessionTestSubject  = "session-role@test"
	sessionTestPassword = "a-password-long-enough-for-the-policy"
)

// sessionRole serves the session role in front of the iam and data roles and
// returns its URL, and the tenant its user belongs to.
func sessionRole(t *testing.T, rotateAfter time.Duration) (string, string) {
	t.Helper()
	bases, tenant := sessionReplicas(t, rotateAfter, 1)
	return bases[0], tenant
}

// sessionReplicas serves replicas of the session role, each its own process
// as far as anything it remembers goes, in front of one iam and data role.
func sessionReplicas(t *testing.T, rotateAfter time.Duration, replicas int) ([]string, string) {
	t.Helper()
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "session-role")
	mustCreateCollection(t, h.PoolMigrate, tenant, "docs")
	hash, err := auth.HashPassword(sessionTestPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO users (tenant_id, subject, password_hash, roles) VALUES ($1, $2, $3, '["platform.admin"]')`,
		tenant, sessionTestSubject, hash); err != nil {
		t.Fatalf("create user: %v", err)
	}

	iamCfg, iamURL := iamRole(t, h)
	jwks := iamURL + sessionkey.WellKnownPath
	dataMux, _, err := app.AssembleDataMux(context.Background(), splitDeps(t, dataRoleConfig(t, h, jwks), config.RoleAPI))
	if err != nil {
		t.Fatalf("AssembleDataMux: %v", err)
	}
	data := httptest.NewServer(dataMux)
	t.Cleanup(data.Close)

	cfg := iamCfg
	cfg.Auth.SessionKeyPath = ""
	cfg.Auth.JWKSURL = jwks
	cfg.Session.ConsoleOrigins = []string{sessionTestOrigin}
	cfg.Session.Upstreams.IAMURL = iamURL
	cfg.Session.Upstreams.DataURL = data.URL
	cfg.Session.Upstreams.AdminURL = data.URL
	cfg.Session.RotateAfter = rotateAfter
	var bases []string
	for range replicas {
		handler, err := app.BuildSessionHandler(context.Background(), cfg, app.BuildMeta{Version: "test"}, zap.NewNop())
		if err != nil {
			t.Fatalf("BuildSessionHandler: %v", err)
		}
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		bases = append(bases, srv.URL)
	}
	return bases, tenant.String()
}

// sessionAnswer is what the browser keeps of one answer: its status and the
// session cookie it set, if any.
type sessionAnswer struct {
	status int
	cookie *http.Cookie
}

// sessionPost posts body to the role as the console's page does.
func sessionPost(t *testing.T, base, path, cookie, body string) sessionAnswer {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", sessionTestOrigin)
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	out := sessionAnswer{status: res.StatusCode}
	for _, c := range res.Cookies() {
		if c.Name == session.CookieName {
			out.cookie = c
		}
	}
	return out
}

// listObjects calls the data plane through the role with the session cookie
// and nothing else: no token anywhere on the browser's side.
func listObjects(t *testing.T, base, cookie, tenant string) error {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Origin", sessionTestOrigin)
	info.RequestHeader().Set("Cookie", session.CookieName+"="+cookie)
	client := paladindatav1connect.NewObjectServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base+session.PathRPC)))
	_, err := client.ListObjects(ctx, &pbdata.ListObjectsRequest{Parent: "tenants/" + tenant + "/collections/docs"})
	return err
}

func TestSessionRole_TheCookieIsTheWholeCredential(t *testing.T) {
	base, tenant := sessionRole(t, time.Hour)

	res := sessionPost(t, base, session.PathLogin, "", `{"subject":"`+sessionTestSubject+`","password":"`+sessionTestPassword+`"}`)
	c := res.cookie
	if res.status != http.StatusOK || c == nil || c.Value == "" {
		t.Fatalf("login = %d, cookie %v; want 200 and a session cookie", res.status, c)
	}

	if err := listObjects(t, base, c.Value, tenant); err != nil {
		t.Fatalf("a call through the role with the cookie: %v", err)
	}
	if err := listObjects(t, base, "", tenant); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("a call without the cookie: %v, want unauthenticated", err)
	}

	out := sessionPost(t, base, session.PathLogout, c.Value, "")
	if gone := out.cookie; out.status != http.StatusNoContent || gone == nil || gone.MaxAge >= 0 {
		t.Fatalf("logout = %d, cookie %v; want 204 and the cookie cleared", out.status, gone)
	}
	if err := listObjects(t, base, c.Value, tenant); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("the signed-out cookie replayed: %v, want unauthenticated", err)
	}
}

func TestSessionRole_RefusesAWrongPassword(t *testing.T) {
	base, _ := sessionRole(t, time.Hour)
	res := sessionPost(t, base, session.PathLogin, "", `{"subject":"`+sessionTestSubject+`","password":"wrong"}`)
	if res.status != http.StatusUnauthorized || res.cookie != nil {
		t.Fatalf("wrong password = %d, cookie %v; want 401 and none", res.status, res.cookie)
	}
}

func sessionLogin(t *testing.T, base string) string {
	t.Helper()
	res := sessionPost(t, base, session.PathLogin, "", `{"subject":"`+sessionTestSubject+`","password":"`+sessionTestPassword+`"}`)
	if res.status != http.StatusOK || res.cookie == nil {
		t.Fatalf("login = %d", res.status)
	}
	return res.cookie.Value
}

// A refresh token older than rotate_after is rotated, its age read by the iam
// role's JWKS, and the new one carries the session.
func TestSessionRole_RotatesAnOldRefreshToken(t *testing.T) {
	base, tenant := sessionRole(t, time.Nanosecond)
	cookie := sessionLogin(t, base)
	// iat has a one-second grain: let the token be older than a nanosecond.
	time.Sleep(time.Second)

	res := sessionPost(t, base, session.PathRefresh, cookie, "")
	if res.status != http.StatusNoContent || res.cookie == nil || res.cookie.Value == cookie {
		t.Fatalf("refresh = %d, cookie %v; want 204 and a rotated token", res.status, res.cookie)
	}
	if err := listObjects(t, base, res.cookie.Value, tenant); err != nil {
		t.Fatalf("a call with the rotated cookie: %v", err)
	}
}

func TestSessionRole_LeavesAYoungRefreshTokenAlone(t *testing.T) {
	base, _ := sessionRole(t, time.Hour)
	cookie := sessionLogin(t, base)
	res := sessionPost(t, base, session.PathRefresh, cookie, "")
	if res.status != http.StatusNoContent || res.cookie != nil {
		t.Fatalf("refresh = %d, cookie %v; want 204 and no new cookie", res.status, res.cookie)
	}
}

// Signed out at one replica, a copied cookie is refused at the other at once:
// no replica answers from what it remembers of the session.
func TestSessionRole_ASignOutReachesEveryReplica(t *testing.T) {
	bases, tenant := sessionReplicas(t, time.Hour, 2)
	cookie := sessionLogin(t, bases[0])
	if err := listObjects(t, bases[1], cookie, tenant); err != nil {
		t.Fatalf("the other replica, before sign-out: %v", err)
	}
	if out := sessionPost(t, bases[0], session.PathLogout, cookie, ""); out.status != http.StatusNoContent {
		t.Fatalf("logout = %d", out.status)
	}
	if err := listObjects(t, bases[1], cookie, tenant); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("the other replica, after sign-out: %v, want unauthenticated", err)
	}
}

package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	pbiam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const (
	testOrigin       = "https://console.example"
	testRefresh      = "refresh-1"
	testRotated      = "refresh-2"
	testSwitched     = "refresh-tenant-b"
	testAccessTTL    = 900
	testRefreshTTL   = 86400
	testUserAgentVer = "1.2.3"
)

// fakeIAM is the iam plane: it mints, rotates and revokes, and records the
// calls it got.
type fakeIAM struct {
	paladiniamv1connect.UnimplementedAuthServiceHandler

	mu        sync.Mutex
	exchanges []string // audience of each ExchangeAudience
	revoked   []string
	rotateErr error
	refused   bool // every refresh token is refused
	switchTo  string
	userAgent string
	forwarded string
	auth      string
}

func (f *fakeIAM) seen(ctx context.Context) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f.userAgent = info.RequestHeader().Get(headerUserAgent)
	f.forwarded = info.RequestHeader().Get(headerForwardedFor)
	f.auth = info.RequestHeader().Get(headerAuthorization)
}

func (f *fakeIAM) Login(ctx context.Context, req *pbiam.LoginRequest) (*pbiam.LoginResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen(ctx)
	if req.GetPassword() != "right" || req.GetRequestedAudience() != paladin.AudienceIAM {
		return nil, connect.NewError(connect.CodeUnauthenticated, "invalid credentials")
	}
	return &pbiam.LoginResponse{
		Tokens: &pbiam.TokenPair{AccessToken: "login-access", RefreshToken: testRefresh, RefreshExpiresInSeconds: testRefreshTTL},
		User:   &pbiam.User{Subject: req.GetSubject()},
	}, nil
}

func (f *fakeIAM) ExchangeAudience(ctx context.Context, req *pbiam.ExchangeAudienceRequest) (*pbiam.ExchangeAudienceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refused {
		return nil, connect.NewError(connect.CodeUnauthenticated, "refresh token revoked")
	}
	f.exchanges = append(f.exchanges, req.GetTargetAudience())
	return &pbiam.ExchangeAudienceResponse{
		AccessToken:            "access-for-" + req.GetTargetAudience() + "-from-" + req.GetRefreshToken(),
		AccessExpiresInSeconds: testAccessTTL,
	}, nil
}

func (f *fakeIAM) RefreshToken(context.Context, *pbiam.RefreshTokenRequest) (*pbiam.RefreshTokenResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rotateErr != nil {
		return nil, f.rotateErr
	}
	return &pbiam.RefreshTokenResponse{Tokens: &pbiam.TokenPair{RefreshToken: testRotated, RefreshExpiresInSeconds: testRefreshTTL}}, nil
}

func (f *fakeIAM) SwitchTenant(ctx context.Context, req *pbiam.SwitchTenantRequest) (*pbiam.SwitchTenantResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen(ctx)
	f.switchTo = req.GetTargetTenantId()
	return &pbiam.SwitchTenantResponse{
		Tokens: &pbiam.TokenPair{AccessToken: "switched-access", RefreshToken: testSwitched, RefreshExpiresInSeconds: testRefreshTTL},
		User:   &pbiam.User{TenantId: req.GetTargetTenantId()},
	}, nil
}

// Revoke, as the iam plane does, wants the caller's own iam access token.
func (f *fakeIAM) Revoke(ctx context.Context, req *pbiam.RevokeRequest) (*pbiam.RevokeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen(ctx)
	if f.auth != "Bearer access-for-"+paladin.AudienceIAM+"-from-"+req.GetToken() {
		return nil, connect.NewError(connect.CodeUnauthenticated, "no bearer")
	}
	f.revoked = append(f.revoked, req.GetToken())
	return &pbiam.RevokeResponse{}, nil
}

// fakeVerifier dates every refresh token issuedAt.
type fakeVerifier struct {
	issuedAt time.Time
	err      error
}

func (v fakeVerifier) IssuedAt(context.Context, string) (time.Time, error) {
	return v.issuedAt, v.err
}

// planeCall is what a plane got from the role.
type planeCall struct {
	path, auth, cookie, origin, userAgent, forwarded, idempotency string
}

// streamChunks and streamGap shape the audit stream the fake admin plane
// serves: longer in all than the role's write timeout in the stream test.
const (
	streamChunks = 4
	streamGap    = 100 * time.Millisecond
)

// plane records each call and answers with a body of its own.
func plane(t *testing.T, calls *[]planeCall, mu *sync.Mutex) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*calls = append(*calls, planeCall{
			path: r.URL.Path, auth: r.Header.Get(headerAuthorization), cookie: r.Header.Get(headerCookie),
			origin:    r.Header.Get(headerOrigin),
			userAgent: r.Header.Get(headerUserAgent), forwarded: r.Header.Get(headerForwardedFor),
			idempotency: r.Header.Get("Idempotency-Key"),
		})
		mu.Unlock()
		if r.URL.Path == "/audit/stream" {
			for i := range streamChunks {
				_, _ = io.WriteString(w, "data: "+strconv.Itoa(i)+"\n\n")
				w.(http.Flusher).Flush()
				time.Sleep(streamGap)
			}
			return
		}
		_, _ = io.WriteString(w, "from-plane")
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

type rig struct {
	t     *testing.T
	h     *Handler
	iam   *fakeIAM
	calls []planeCall
	mu    sync.Mutex
	now   time.Time
}

func newRig(t *testing.T, v fakeVerifier) *rig {
	t.Helper()
	r := &rig{t: t, iam: &fakeIAM{}, now: time.Unix(1_700_000_000, 0)}
	server := connect.NewServer()
	paladiniamv1connect.RegisterAuthServiceHandler(server, r.iam)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	iamSrv := httptest.NewServer(mux)
	t.Cleanup(iamSrv.Close)
	iamClient := paladiniamv1connect.NewAuthServiceClient(connect.NewClient(connecthttp.NewTransport(iamSrv.Client(), iamSrv.URL)))

	planeURL := plane(t, &r.calls, &r.mu)
	h, err := New(Options{
		IAM:         iamClient,
		Planes:      map[Plane]*url.URL{PlaneData: planeURL, PlaneAdmin: planeURL, PlaneIAM: planeURL},
		Refresh:     v,
		Origins:     []string{testOrigin},
		RotateAfter: 5 * time.Minute,
		Version:     testUserAgentVer,
		Now:         func() time.Time { return r.now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.h = h
	return r
}

// call sends a request as the console's browser would: from its origin,
// with the session cookie when refresh is set.
func (r *rig) call(method, path, refresh, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(r.t.Context(), method, path, strings.NewReader(body))
	req.Header.Set(headerOrigin, testOrigin)
	req.Header.Set(headerForwardedFor, "203.0.113.7")
	if refresh != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: refresh})
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	return nil
}

func TestLoginSetsTheCookieAndKeepsTheTokensFromTheBrowser(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	rec := r.call(http.MethodPost, PathLogin, "", `{"subject":"ada","password":"right"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil || c.Value != testRefresh || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie = %+v, want the refresh token, httpOnly, Secure, Strict, on /", c)
	}
	if c.MaxAge != testRefreshTTL {
		t.Errorf("cookie lives %ds, want the refresh token's %ds", c.MaxAge, testRefreshTTL)
	}
	if strings.Contains(rec.Body.String(), "login-access") || strings.Contains(rec.Body.String(), testRefresh) {
		t.Errorf("the body hands the browser a token: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"subject":"ada"`) {
		t.Errorf("body = %s, want the user", rec.Body)
	}
	if r.iam.userAgent != ConsoleUserAgentProduct+"/"+testUserAgentVer || r.iam.forwarded != "203.0.113.7" {
		t.Errorf("iam saw UA %q and XFF %q, want the console and the browser's address", r.iam.userAgent, r.iam.forwarded)
	}
}

func TestLoginRefusalReachesTheBrowserWithoutACookie(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	rec := r.call(http.MethodPost, PathLogin, "", `{"subject":"ada","password":"wrong"}`)
	if rec.Code != http.StatusUnauthorized || sessionCookie(rec) != nil {
		t.Fatalf("wrong password = %d, cookie %v; want 401 and none", rec.Code, sessionCookie(rec))
	}
	var body struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != connect.CodeUnauthenticated.String() {
		t.Errorf("code = %q, want unauthenticated", body.Code)
	}
}

// A call forwarded to a plane carries the session's access token for that
// plane and nothing of the browser's credentials.
func TestForwardAttachesThePlanesTokenAndDropsTheBrowsersCredentials(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	for procedure, audience := range map[string]string{
		"/paladin.data.v1.ObjectService/ListObjects":  paladin.AudienceData,
		"/paladin.admin.v1.TenantService/ListTenants": paladin.AudienceAdmin,
		"/paladin.iam.v1.AuthService/WhoAmI":          paladin.AudienceIAM,
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, PathRPC+procedure, strings.NewReader("{}"))
		req.Header.Set(headerOrigin, testOrigin)
		req.Header.Set(headerAuthorization, "Bearer from-the-page")
		req.Header.Set("Idempotency-Key", "k1")
		req.AddCookie(&http.Cookie{Name: CookieName, Value: testRefresh})
		rec := httptest.NewRecorder()
		r.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "from-plane" {
			t.Fatalf("%s = %d %s", procedure, rec.Code, rec.Body)
		}
		got := r.calls[len(r.calls)-1]
		if got.path != procedure {
			t.Errorf("plane got %q, want %q", got.path, procedure)
		}
		if want := "Bearer access-for-" + audience + "-from-" + testRefresh; got.auth != want {
			t.Errorf("%s: Authorization %q, want %q", procedure, got.auth, want)
		}
		if got.cookie != "" || got.origin != "" {
			t.Errorf("%s: the plane got the browser's cookie %q or origin %q", procedure, got.cookie, got.origin)
		}
		if got.idempotency != "k1" || got.userAgent != ConsoleUserAgentProduct+"/"+testUserAgentVer {
			t.Errorf("%s: idempotency %q, UA %q", procedure, got.idempotency, got.userAgent)
		}
	}
}

// Exchange is asked once per session and audience while the token lives,
// and again once it nears expiry.
func TestForwardReusesAnAccessTokenUntilItNearsExpiry(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	path := PathRPC + "/paladin.data.v1.ObjectService/ListObjects"
	r.call(http.MethodPost, path, testRefresh, "{}")
	r.call(http.MethodPost, path, testRefresh, "{}")
	if len(r.iam.exchanges) != 1 {
		t.Fatalf("exchanges = %v, want one for two calls", r.iam.exchanges)
	}
	r.now = r.now.Add(testAccessTTL*time.Second - accessEarlyExpiry)
	r.call(http.MethodPost, path, testRefresh, "{}")
	if len(r.iam.exchanges) != 2 {
		t.Errorf("exchanges = %v, want a fresh one near expiry", r.iam.exchanges)
	}
}

func TestForwardRefuses(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	for name, tc := range map[string]struct {
		path, refresh string
		want          int
	}{
		"no session": {PathRPC + "/paladin.data.v1.ObjectService/ListObjects", "", http.StatusUnauthorized},
		"no plane":   {PathRPC + "/grpc.health.v1.Health/Check", testRefresh, http.StatusNotFound},
		"no method":  {PathRPC + "/paladin.data.v1.ObjectService", testRefresh, http.StatusNotFound},
		"login":      {PathRPC + paladiniamv1connect.AuthServiceLoginProcedure, testRefresh, http.StatusForbidden},
		"exchange":   {PathRPC + paladiniamv1connect.AuthServiceExchangeAudienceProcedure, testRefresh, http.StatusForbidden},
		"refresh":    {PathRPC + paladiniamv1connect.AuthServiceRefreshTokenProcedure, testRefresh, http.StatusForbidden},
		"switch":     {PathRPC + paladiniamv1connect.AuthServiceSwitchTenantProcedure, testRefresh, http.StatusForbidden},
		"revoke":     {PathRPC + paladiniamv1connect.AuthServiceRevokeProcedure, testRefresh, http.StatusForbidden},
	} {
		if rec := r.call(http.MethodPost, tc.path, tc.refresh, "{}"); rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, rec.Code, tc.want)
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("planes were called: %v", r.calls)
	}
}

// A session IAM no longer honours is signed out: the cookie goes.
func TestForwardSignsOutARefusedSession(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	r.iam.refused = true
	rec := r.call(http.MethodPost, PathRPC+"/paladin.data.v1.ObjectService/ListObjects", testRefresh, "{}")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refused session = %d, want 401", rec.Code)
	}
	if c := sessionCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("cookie = %+v, want it cleared", c)
	}
}

func TestStateChangingCallsNeedTheConsolesOrigin(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	for _, origin := range []string{"", "https://evil.example", "https://console.example.evil"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, PathLogin, strings.NewReader(`{"subject":"ada","password":"right"}`))
		if origin != "" {
			req.Header.Set(headerOrigin, origin)
		}
		rec := httptest.NewRecorder()
		r.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || sessionCookie(rec) != nil {
			t.Errorf("origin %q = %d, want 403 and no cookie", origin, rec.Code)
		}
	}
	// A read needs none: SameSite=Strict keeps the cookie off foreign pages.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, PathRPC+"/paladin.data.v1.ObjectService/GetObject", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: testRefresh})
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET without Origin = %d, want forwarded", rec.Code)
	}
}

func TestRotateLeavesAYoungTokenAlone(t *testing.T) {
	r := newRig(t, fakeVerifier{issuedAt: time.Unix(1_700_000_000, 0).Add(-time.Minute)})
	rec := r.call(http.MethodPost, PathRefresh, testRefresh, "")
	if rec.Code != http.StatusNoContent || sessionCookie(rec) != nil {
		t.Fatalf("young token = %d, cookie %v; want 204 and no new cookie", rec.Code, sessionCookie(rec))
	}
}

func TestRotateRotatesAnOldToken(t *testing.T) {
	r := newRig(t, fakeVerifier{issuedAt: time.Unix(1_700_000_000, 0).Add(-10 * time.Minute)})
	rec := r.call(http.MethodPost, PathRefresh, testRefresh, "")
	if c := sessionCookie(rec); rec.Code != http.StatusNoContent || c == nil || c.Value != testRotated {
		t.Fatalf("old token = %d, cookie %v; want 204 and the rotated token", rec.Code, c)
	}
}

// The request that loses a concurrent rotation keeps its session: the
// winner's answer carries the new cookie.
func TestRotateLosingTheRaceKeepsTheSession(t *testing.T) {
	r := newRig(t, fakeVerifier{issuedAt: time.Unix(1_700_000_000, 0).Add(-10 * time.Minute)})
	r.iam.rotateErr = connect.NewError(connect.CodeAborted, "superseded")
	rec := r.call(http.MethodPost, PathRefresh, testRefresh, "")
	if rec.Code != http.StatusNoContent || sessionCookie(rec) != nil {
		t.Fatalf("lost race = %d, cookie %v; want 204, cookie untouched", rec.Code, sessionCookie(rec))
	}
}

// A token whose age cannot be read — the JWKS not fetched while iam
// restarts — is not a verdict on the session: it stays signed in, and IAM
// judges the token on its next call.
func TestRotateKeepsTheSessionWhenItCannotReadTheAge(t *testing.T) {
	r := newRig(t, fakeVerifier{err: errors.New("jwks: not fetched yet")})
	rec := r.call(http.MethodPost, PathRefresh, testRefresh, "")
	if rec.Code != http.StatusNoContent || sessionCookie(rec) != nil {
		t.Fatalf("unreadable age = %d, cookie %v; want 204 and the cookie untouched", rec.Code, sessionCookie(rec))
	}
}

func TestSwitchTenantReplacesTheCookie(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	rec := r.call(http.MethodPost, PathSwitchTenant, testRefresh, `{"targetTenantId":"tenant-b"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch = %d %s", rec.Code, rec.Body)
	}
	if c := sessionCookie(rec); c == nil || c.Value != testSwitched {
		t.Errorf("cookie = %v, want the new tenant's refresh token", c)
	}
	if r.iam.switchTo != "tenant-b" || r.iam.auth != "Bearer access-for-"+paladin.AudienceIAM+"-from-"+testRefresh {
		t.Errorf("iam switched to %q as %q", r.iam.switchTo, r.iam.auth)
	}
	if strings.Contains(rec.Body.String(), testSwitched) || strings.Contains(rec.Body.String(), "switched-access") {
		t.Errorf("the body hands the browser a token: %s", rec.Body)
	}
}

func TestLogoutRevokesAndClears(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	rec := r.call(http.MethodPost, PathLogout, testRefresh, "")
	if c := sessionCookie(rec); rec.Code != http.StatusNoContent || c == nil || c.MaxAge >= 0 {
		t.Fatalf("logout = %d, cookie %v; want 204 and the cookie cleared", rec.Code, c)
	}
	if len(r.iam.revoked) != 1 || r.iam.revoked[0] != testRefresh {
		t.Errorf("revoked = %v, want the session's refresh token", r.iam.revoked)
	}
}

func TestAuditStreamIsForwardedToTheAdminPlane(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	rec := r.call(http.MethodGet, PathAuditStream, testRefresh, "")
	if rec.Code != http.StatusOK || len(r.calls) != 1 || r.calls[0].path != "/audit/stream" {
		t.Fatalf("audit stream = %d, plane calls %v", rec.Code, r.calls)
	}
	if want := "Bearer access-for-" + paladin.AudienceAdmin + "-from-" + testRefresh; r.calls[0].auth != want {
		t.Errorf("Authorization %q, want %q", r.calls[0].auth, want)
	}
}

func TestNewRefusesOptionsItCannotServe(t *testing.T) {
	u, _ := url.Parse("http://plane")
	planes := map[Plane]*url.URL{PlaneData: u, PlaneAdmin: u, PlaneIAM: u}
	iam := paladiniamv1connect.NewAuthServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://iam")))
	base := Options{IAM: iam, Planes: planes, Refresh: fakeVerifier{}, Origins: []string{testOrigin}, RotateAfter: time.Minute}
	for name, mutate := range map[string]func(*Options){
		"no rotation": func(o *Options) { o.RotateAfter = 0 },
		"no plane":    func(o *Options) { o.Planes = map[Plane]*url.URL{PlaneData: u} },
		"no verifier": func(o *Options) { o.Refresh = nil },
	} {
		o := base
		mutate(&o)
		if _, err := New(o); err == nil {
			t.Errorf("%s: built, want refused", name)
		}
	}
	if _, err := New(base); err != nil {
		t.Errorf("complete options: %v", err)
	}
}

// The audit stream lasts past the listener's write timeout, which bounds one
// answer, not a feed the page keeps open.
func TestAuditStreamOutlivesTheWriteTimeout(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	srv := httptest.NewUnstartedServer(r.h)
	srv.Config.WriteTimeout = streamGap
	srv.Start()
	t.Cleanup(srv.Close)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+PathAuditStream, nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: testRefresh})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("the stream was cut off: %v (got %q)", err, body)
	}
	if got := strings.Count(string(body), "data: "); got != streamChunks {
		t.Errorf("got %d events, want %d: %q", got, streamChunks, body)
	}
}

// Each forwarded call is logged with the plane's answer.
func TestForwardIsLogged(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	core, logs := observer.New(zap.DebugLevel)
	r.h.log = zap.New(core)
	r.call(http.MethodPost, PathRPC+"/paladin.data.v1.ObjectService/ListObjects", testRefresh, "{}")
	entries := logs.FilterMessage("session: forwarded").All()
	if len(entries) != 1 {
		t.Fatalf("%d forwarded lines, want one", len(entries))
	}
	f := entries[0].ContextMap()
	if f["path"] != "/paladin.data.v1.ObjectService/ListObjects" || f["status"] != int64(http.StatusOK) {
		t.Errorf("logged %v, want the procedure and 200", f)
	}
}

// With no origin configured, a call that changes state passes only when its
// Origin names the host it was made to: the console's own, as the ingress
// forwards it.
func TestOriginMayNameTheHostTheCallArrivedOn(t *testing.T) {
	r := newRig(t, fakeVerifier{})
	r.h.origins = map[string]bool{}
	for name, tc := range map[string]struct {
		origin, forwardedHost string
		allowed               bool
	}{
		"same host":              {"http://example.com", "", true},
		"forwarded host":         {"https://console.test", "console.test", true},
		"forwarded host, case":   {"https://Console.Test", "console.test", true},
		"another host":           {"https://evil.example", "", false},
		"a suffix of the host":   {"https://example.com.evil", "", false},
		"host not the forwarded": {"http://example.com", "console.test", false},
		"no origin":              {"", "", false},
		"not a url":              {"::", "", false},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, PathLogin, strings.NewReader(`{"subject":"ada","password":"right"}`))
		if tc.origin != "" {
			req.Header.Set(headerOrigin, tc.origin)
		}
		if tc.forwardedHost != "" {
			req.Header.Set(headerForwardedHost, tc.forwardedHost)
		}
		rec := httptest.NewRecorder()
		r.h.ServeHTTP(rec, req)
		if allowed := rec.Code != http.StatusForbidden; allowed != tc.allowed {
			t.Errorf("%s: %d, want allowed=%v", name, rec.Code, tc.allowed)
		}
	}
}

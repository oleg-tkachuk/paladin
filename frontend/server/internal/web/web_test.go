package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/frontend/server/contract"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/policy"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/site"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/web"
)

const relayTimeout = 5 * time.Second

type fixture struct {
	handler http.Handler
	logs    *observer.ObservedLogs
	names   contract.Names
}

func newFixture(t *testing.T, iam http.Handler, banner web.Banner) fixture {
	t.Helper()
	names, err := contract.Load()
	require.NoError(t, err)
	files := fstest.MapFS{
		"index.html":         {Data: []byte("home")},
		"404.html":           {Data: []byte("not found")},
		"login.html":         {Data: []byte("login")},
		"login.txt":          {Data: []byte("login rsc")},
		"health.html":        {Data: []byte("health")},
		"oauth/consent.html": {Data: []byte("consent")},
		"tenants/_.html":     {Data: []byte("tenant")},
	}
	s, err := site.New(files, names.PlaceholderSegment)
	require.NoError(t, err)

	authorize := "http://iam.invalid" + names.AuthorizePath
	transport := http.DefaultTransport
	if iam != nil {
		upstream := httptest.NewServer(iam)
		t.Cleanup(upstream.Close)
		authorize = upstream.URL + names.AuthorizePath
	}
	core, logs := observer.New(zapcore.DebugLevel)
	return fixture{
		handler: web.Handler(web.Config{
			Names:        names,
			Site:         s,
			Policy:       policy.New([]string{"https://s3.example.com"}),
			Banner:       banner,
			AuthorizeURL: authorize,
			Relay:        web.RelayClient(transport, relayTimeout),
			Log:          zap.New(core),
		}),
		logs:  logs,
		names: names,
	}
}

func (f fixture) do(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	return rec
}

func (f fixture) signedIn(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.AddCookie(&http.Cookie{Name: f.names.SessionCookie, Value: "x"})
	return r
}

func TestGate(t *testing.T) {
	f := newFixture(t, nil, web.Banner{})

	t.Run("a page without the cookie goes to sign in, keeping where it was", func(t *testing.T) {
		rec := f.do(httptest.NewRequest(http.MethodGet, "/tenants/acme?tab=x", nil))
		require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
		loc, err := url.Parse(rec.Header().Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, f.names.LoginPath, loc.Path)
		assert.Equal(t, "/tenants/acme?tab=x", loc.Query().Get(f.names.NextParam))
	})

	t.Run("with the cookie the page is served", func(t *testing.T) {
		rec := f.do(f.signedIn(http.MethodGet, "/tenants/acme", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "tenant", rec.Body.String())
	})

	for _, p := range []string{"/login", "/login.txt", "/api/health/live", "/oauth/consent", "/_next/static/x.js"} {
		t.Run(p+" is open without a session", func(t *testing.T) {
			rec := f.do(httptest.NewRequest(http.MethodGet, p, nil))
			assert.NotEqual(t, http.StatusTemporaryRedirect, rec.Code)
		})
	}

	t.Run("the router's payload request returns to the page, not the payload", func(t *testing.T) {
		for path, want := range map[string]string{
			"/tenants/acme/billing.txt": "/tenants/acme/billing",
			"/index.txt":                "/",
		} {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.Header.Set(site.RSCHeader, "1")
			loc, err := url.Parse(f.do(r).Header().Get("Location"))
			require.NoError(t, err)
			assert.Equal(t, want, loc.Query().Get(f.names.NextParam), path)
		}
	})

	t.Run("a prefix is a path, not a string", func(t *testing.T) {
		rec := f.do(httptest.NewRequest(http.MethodGet, "/loginx", nil))
		assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	})
}

func TestHeaders(t *testing.T) {
	f := newFixture(t, nil, web.Banner{})

	rec := f.do(httptest.NewRequest(http.MethodGet, "/login", nil))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Contains(t, rec.Header().Get(policy.CSPHeader), "form-action 'self'")
	assert.Contains(t, rec.Header().Get(policy.CSPHeader), "https://s3.example.com")

	consent := f.do(httptest.NewRequest(http.MethodGet, f.names.ConsentPath, nil))
	assert.NotContains(t, consent.Header().Get(policy.CSPHeader), "form-action")

	redirect := f.do(httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusTemporaryRedirect, redirect.Code)
	assert.NotEmpty(t, redirect.Header().Get(policy.CSPHeader), "on a redirect as well")
}

func TestLive(t *testing.T) {
	f := newFixture(t, nil, web.Banner{})
	rec := f.do(httptest.NewRequest(http.MethodGet, f.names.LivePath, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestBanner(t *testing.T) {
	t.Run("as the environment sets it", func(t *testing.T) {
		f := newFixture(t, nil, web.Banner{Content: "Maintenance", Severity: "warning", URL: "/health", Permanent: "true", Position: "both"})
		rec := f.do(f.signedIn(http.MethodGet, f.names.BannerPath, nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		assert.JSONEq(t, `{"content":"Maintenance","severity":"warning","url":"/health","permanent":"true","position":"both"}`, rec.Body.String())
	})

	t.Run("null without content", func(t *testing.T) {
		f := newFixture(t, nil, web.Banner{Content: "  ", Severity: "info"})
		rec := f.do(f.signedIn(http.MethodGet, f.names.BannerPath, nil))
		assert.JSONEq(t, `null`, rec.Body.String())
	})
}

func TestRelay(t *testing.T) {
	const form = "login=admin&password=secret&client_id=agent"
	var got struct{ body, contentType, path string }
	iam := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.body, got.contentType, got.path = string(b), r.Header.Get("Content-Type"), r.URL.Path
		w.Header().Set("Location", "desktop-agent://callback?code=abc")
		w.Header().Set("Set-Cookie", "leak=1")
		w.WriteHeader(http.StatusFound)
	})
	f := newFixture(t, iam, web.Banner{})

	rec := f.do(httptest.NewRequest(http.MethodPost, f.names.AuthorizePath, strings.NewReader(form)))

	assert.Equal(t, form, got.body, "the form as the browser sent it")
	assert.Equal(t, "application/x-www-form-urlencoded", got.contentType)
	assert.Equal(t, f.names.AuthorizePath, got.path)
	assert.Equal(t, http.StatusFound, rec.Code, "the redirect is the browser's to follow")
	assert.Equal(t, "desktop-agent://callback?code=abc", rec.Header().Get("Location"))
	assert.Empty(t, rec.Header().Get("Set-Cookie"), "only the named headers pass")

	relayed := f.logs.FilterMessage("consent form relayed").AllUntimed()
	require.Len(t, relayed, 1)
	assert.Equal(t, int64(http.StatusFound), relayed[0].ContextMap()["status"])
	for _, e := range f.logs.AllUntimed() {
		for _, v := range e.ContextMap() {
			if s, ok := v.(string); ok {
				assert.NotContains(t, s, "secret", "the password never reaches a log")
			}
		}
	}
}

func TestRelay_UpstreamDown(t *testing.T) {
	f := newFixture(t, nil, web.Banner{}) // http://iam.invalid
	rec := f.do(httptest.NewRequest(http.MethodPost, f.names.AuthorizePath, strings.NewReader("a=b")))
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, 1, f.logs.FilterMessage("consent relay: the IAM plane did not answer").Len())
}

func TestRelay_TooLarge(t *testing.T) {
	f := newFixture(t, http.NotFoundHandler(), web.Banner{})
	big := strings.Repeat("a", web.MaxConsentForm+1)
	rec := f.do(httptest.NewRequest(http.MethodPost, f.names.AuthorizePath, strings.NewReader(big)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestLegacyRedirects(t *testing.T) {
	f := newFixture(t, nil, web.Banner{})
	for _, tc := range []struct{ from, to string }{
		{"/ops", "/health"},
		{"/storage-backends/primary/buckets", "/storage-backends/primary"},
		{"/tenants/acme/buckets/primary/pool", "/storage-backends/primary/buckets/pool"},
		{"/tenants/acme/buckets/primary/pool/lifecycle", "/storage-backends/primary/buckets/pool/lifecycle"},
		{"/tenants/acme/buckets/b%201/my.pool", "/storage-backends/b%201/buckets/my.pool"},
	} {
		t.Run(tc.from, func(t *testing.T) {
			rec := f.do(f.signedIn(http.MethodGet, tc.from, nil))
			assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
			assert.Equal(t, tc.to, rec.Header().Get("Location"))
		})
	}

	t.Run("the router's payload request goes to the new page's payload", func(t *testing.T) {
		for from, to := range map[string]string{
			"/ops.txt":                                   "/health.txt",
			"/storage-backends/primary/buckets.txt":      "/storage-backends/primary.txt",
			"/tenants/acme/buckets/primary/pool/tab.txt": "/storage-backends/primary/buckets/pool/tab.txt",
		} {
			r := f.signedIn(http.MethodGet, from, nil)
			r.Header.Set(site.RSCHeader, "1")
			rec := f.do(r)
			assert.Equal(t, http.StatusTemporaryRedirect, rec.Code, from)
			assert.Equal(t, to, rec.Header().Get("Location"), from)
		}
	})

	t.Run("a page that did not move is left alone", func(t *testing.T) {
		for _, p := range []string{"/tenants/acme/buckets", "/storage-backends/primary", "/opsx"} {
			assert.NotEqual(t, http.StatusTemporaryRedirect, f.do(f.signedIn(http.MethodGet, p, nil)).Code, p)
		}
	})

	t.Run("behind the gate, like the pages they were", func(t *testing.T) {
		rec := f.do(httptest.NewRequest(http.MethodGet, "/ops", nil))
		assert.True(t, strings.HasPrefix(rec.Header().Get("Location"), f.names.LoginPath))
	})
}

func TestMethodsAndLogs(t *testing.T) {
	f := newFixture(t, nil, web.Banner{})

	rec := f.do(f.signedIn(http.MethodPost, "/tenants/acme", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "a static page takes no writes")
	refused := f.logs.FilterMessage("request refused: method not allowed").AllUntimed()
	require.Len(t, refused, 1)
	assert.Equal(t, zapcore.WarnLevel, refused[0].Level)
	assert.Equal(t, "/tenants/acme", refused[0].ContextMap()["path"])

	rec = f.do(f.signedIn(http.MethodGet, "/nowhere/at/all", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not found", rec.Body.String())
	assert.Equal(t, 1, f.logs.FilterMessage("request for a path no route serves").Len())

	f.do(f.signedIn(http.MethodGet, "/tenants/acme", nil))
	served := f.logs.FilterMessage("request served").FilterField(zap.String("path", "/tenants/acme")).AllUntimed()
	require.Len(t, served, 1)
	assert.Equal(t, int64(http.StatusOK), served[0].ContextMap()["status"])
}

// The router strips a trailing ".txt" from the address it loads on; a page
// whose id ends so is sent where the dot is encoded.
func TestTxtSuffixRedirect(t *testing.T) {
	f := newFixture(t, nil, web.Banner{})

	rec := f.do(f.signedIn(http.MethodGet, "/tenants/notes.txt?tab=x", nil))
	assert.Equal(t, http.StatusPermanentRedirect, rec.Code)
	assert.Equal(t, "/tenants/notes%2Etxt?tab=x", rec.Header().Get("Location"))

	rec = f.do(f.signedIn(http.MethodGet, "/tenants/notes%2Etxt", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "tenant", rec.Body.String())

	rsc := f.signedIn(http.MethodGet, "/tenants/notes.txt", nil)
	rsc.Header.Set(site.RSCHeader, "1")
	assert.NotEqual(t, http.StatusPermanentRedirect, f.do(rsc).Code, "the router's own payload request is not redirected")
}

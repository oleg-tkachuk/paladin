package site_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oleg-tkachuk/paladin/frontend/server/internal/site"
)

const placeholder = "_"

// export is the shape `next build` writes with output: "export".
func export() fstest.MapFS {
	file := func(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }
	return fstest.MapFS{
		"index.html":                             file("home"),
		"index.txt":                              file("home rsc"),
		"404.html":                               file("not found"),
		"login.html":                             file("login"),
		"favicon.ico":                            file("icon"),
		"tenants.html":                           file("tenants"),
		"tenants/new.html":                       file("new tenant"),
		"tenants/_.html":                         file("tenant"),
		"tenants/_.txt":                          file("tenant rsc"),
		"tenants/_/billing.html":                 file("billing"),
		"tenants/_/billing.txt":                  file("billing rsc"),
		"tenants/_/billing/__next._tree.txt":     file("tree"),
		"tenants/_/collections/_/objects/_.html": file("object"),
		"_next/static/chunks/a1.js":              file("js"),
	}
}

func newSite(t *testing.T) *site.Site {
	t.Helper()
	s, err := site.New(export(), placeholder)
	require.NoError(t, err)
	return s
}

func TestResolve(t *testing.T) {
	s := newSite(t)
	for _, tc := range []struct {
		name string
		path string
		rsc  bool
		want string
	}{
		{"the root", "/", false, "index.html"},
		{"the root's payload", "/index.txt", true, "index.txt"},
		{"a static page", "/login", false, "login.html"},
		{"an asset", "/favicon.ico", false, "favicon.ico"},
		{"a hashed chunk", "/_next/static/chunks/a1.js", false, "_next/static/chunks/a1.js"},
		{"a literal before the placeholder", "/tenants/new", false, "tenants/new.html"},
		{"a dynamic segment", "/tenants/acme", false, "tenants/_.html"},
		{"a page below a dynamic segment", "/tenants/acme/billing", false, "tenants/_/billing.html"},
		{"a trailing slash", "/tenants/acme/billing/", false, "tenants/_/billing.html"},
		{"the payload of a dynamic page", "/tenants/acme.txt", true, "tenants/_.txt"},
		{"the payload below one", "/tenants/acme/billing.txt", true, "tenants/_/billing.txt"},
		{"a segment file", "/tenants/acme/billing/__next._tree.txt", true, "tenants/_/billing/__next._tree.txt"},
		{"an encoded slash inside a segment", "/tenants/a%2Fb/collections/x%2Fy/objects/k%2Fey", false, "tenants/_/collections/_/objects/_.html"},
		{"an id ending in .txt, as a page", "/tenants/notes.txt", false, "tenants/_.html"},
		{"the placeholder itself", "/tenants/_", false, "tenants/_.html"},
		{"a malformed escape, as a dynamic value", "/tenants/bad%zz", false, "tenants/_.html"},
		{"a dot-dot segment, as a dynamic value", "/tenants/%2e%2e/billing", false, "tenants/_/billing.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := s.Resolve(tc.path, tc.rsc)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolve_NotFound(t *testing.T) {
	s := newSite(t)
	for _, p := range []string{
		"/nowhere",
		"/tenants/acme/nowhere",
		"/tenants/acme/billing/extra",
		"/../etc/passwd",
	} {
		_, ok := s.Resolve(p, false)
		assert.False(t, ok, p)
	}
}

func TestNew_RefusesAnExportWithoutIndex(t *testing.T) {
	_, err := site.New(fstest.MapFS{"login.html": {Data: []byte("x")}}, placeholder)
	require.ErrorContains(t, err, "no index.html")
}

func TestServe(t *testing.T) {
	s := newSite(t)

	t.Run("a page, revalidated, with an ETag", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Serve(rec, httptest.NewRequest(http.MethodGet, "/tenants/acme", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "tenant", rec.Body.String())
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
		assert.NotEmpty(t, rec.Header().Get("ETag"))
	})

	t.Run("a hashed chunk is immutable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Serve(rec, httptest.NewRequest(http.MethodGet, "/_next/static/chunks/a1.js", nil))
		assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
		assert.Equal(t, "text/javascript; charset=utf-8", rec.Header().Get("Content-Type"))
	})

	t.Run("the RSC payload when the router asks", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tenants/acme/billing.txt", nil)
		req.Header.Set(site.RSCHeader, "1")
		s.Serve(rec, req)
		assert.Equal(t, "billing rsc", rec.Body.String())
	})

	t.Run("an unchanged file is not sent again", func(t *testing.T) {
		first := httptest.NewRecorder()
		s.Serve(first, httptest.NewRequest(http.MethodGet, "/login", nil))
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.Header.Set("If-None-Match", first.Header().Get("ETag"))
		rec := httptest.NewRecorder()
		s.Serve(rec, req)
		assert.Equal(t, http.StatusNotModified, rec.Code)
	})

	t.Run("an unknown path gets the export's 404 page", func(t *testing.T) {
		rec := httptest.NewRecorder()
		_, found := s.Serve(rec, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
		assert.False(t, found)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "not found", rec.Body.String())
	})

	t.Run("HEAD answers without a body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.Serve(rec, httptest.NewRequest(http.MethodHead, "/tenants/acme", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Body.String())
	})
}

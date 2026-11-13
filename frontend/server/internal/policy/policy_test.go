package policy_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/oleg-tkachuk/paladin/frontend/server/internal/policy"
)

func TestStorageOrigins_Keeps(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"nothing set", "", nil},
		{"an empty list", " , ", nil},
		{"one origin", "https://s3.paladin.local", []string{"https://s3.paladin.local"}},
		{"several, trimmed, with a port", " https://s3.example.com , http://localhost:9000 ",
			[]string{"https://s3.example.com", "http://localhost:9000"}},
		{"a trailing slash", "https://s3.example.com/", []string{"https://s3.example.com"}},
		{"upper case", "HTTPS://S3.Example.com", []string{"https://s3.example.com"}},
		{"a repeat once", "https://a.test,https://a.test", []string{"https://a.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origins, invalid := policy.StorageOrigins(tc.raw)
			assert.Equal(t, tc.want, origins)
			assert.Empty(t, invalid)
		})
	}
}

func TestStorageOrigins_DropsAndReports(t *testing.T) {
	for _, raw := range []string{
		"https://s3.example.com/bucket",
		"https://s3.example.com?x=1",
		"https://*.example.com",
		"s3.example.com",
		"ftp://s3.example.com",
		"*",
		"https://user@s3.example.com",
	} {
		t.Run(raw, func(t *testing.T) {
			origins, invalid := policy.StorageOrigins("https://ok.test," + raw)
			assert.Equal(t, []string{"https://ok.test"}, origins)
			assert.Equal(t, []string{raw}, invalid)
		})
	}
}

func directives(csp string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(csp, "; ") {
		name, value, _ := strings.Cut(d, " ")
		out[name] = value
	}
	return out
}

func TestCSP(t *testing.T) {
	p := policy.New([]string{"https://s3.example.com", "http://localhost:9000"})

	d := directives(p.CSP(false))
	assert.Equal(t, "'self'", d["default-src"])
	assert.Equal(t, "'none'", d["frame-ancestors"])
	assert.Equal(t, "'self' 'unsafe-inline'", d["script-src"], "no 'unsafe-eval' in a built console")
	assert.Equal(t, "'self' https://s3.example.com http://localhost:9000", d["connect-src"])
	assert.Equal(t, "'self' data: blob: https://s3.example.com http://localhost:9000", d["img-src"])
	assert.Equal(t, "'self' https://s3.example.com http://localhost:9000", d["frame-src"])
	assert.Equal(t, "'self'", d["form-action"])
	assert.Contains(t, d, "upgrade-insecure-requests")

	consent := directives(p.CSP(true))
	assert.NotContains(t, consent, "form-action",
		"the consent form is answered with a redirect to the client's own scheme")
	assert.Equal(t, d["default-src"], consent["default-src"])
}

func TestCSP_WithoutStorage(t *testing.T) {
	d := directives(policy.New(nil).CSP(false))
	assert.Equal(t, "'self'", d["connect-src"])
	assert.Equal(t, "'self' data: blob:", d["img-src"])
}

func TestApply(t *testing.T) {
	h := http.Header{}
	policy.New(nil).Apply(h, false)
	assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"))
	assert.Equal(t, "off", h.Get("X-DNS-Prefetch-Control"))
	assert.Equal(t, "camera=(), microphone=(), geolocation=()", h.Get("Permissions-Policy"))
	assert.Contains(t, h.Get("Strict-Transport-Security"), "max-age=63072000")
	assert.NotEmpty(t, h.Get(policy.CSPHeader))
}

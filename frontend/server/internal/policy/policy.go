// Package policy holds the headers every console response carries: the
// Content-Security-Policy and the fixed security headers.
//
// The CSP starts at default-src 'self' and opens only what the console needs.
// Next.js injects inline styles and inline bootstrap scripts, so both allow
// 'unsafe-inline'. The one other place the browser goes is a storage backend,
// on a presigned URL: the deployment names those origins, and they are added
// to connect-src for uploads and text previews, img-src for images and
// frame-src for PDFs.
package policy

import (
	"net/http"
	"net/url"
	"strings"
)

// CSPHeader is the response header the policy travels in.
const CSPHeader = "Content-Security-Policy"

const (
	listSeparator = ","
	wildcard      = "*"
	rootSlash     = "/"
	directiveSep  = "; "
	sourceSep     = " "
)

var allowedSchemes = map[string]bool{"https": true, "http": true}

var baseDirectives = []string{
	"default-src 'self'",
	"base-uri 'self'",
	"frame-ancestors 'none'",
	"object-src 'none'",
	"font-src 'self' data:",
	"style-src 'self' 'unsafe-inline'",
	"script-src 'self' 'unsafe-inline'",
}

// formActionSelf ends every form on this origin, on every page but consent.
const formActionSelf = "form-action 'self'"

const upgradeInsecure = "upgrade-insecure-requests"

// securityHeaders are set on every response, before the CSP.
var securityHeaders = [][2]string{
	{"Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload"},
	{"X-Frame-Options", "DENY"},
	{"X-Content-Type-Options", "nosniff"},
	{"Referrer-Policy", "strict-origin-when-cross-origin"},
	{"X-DNS-Prefetch-Control", "off"},
	{"Permissions-Policy", "camera=(), microphone=(), geolocation=()"},
}

// StorageOrigins reads the comma-separated list of storage origins. An entry
// is kept only when it is an http(s) origin and nothing more: a path, a query
// or a wildcard would widen or blur what the policy allows, so such an entry
// is returned as invalid rather than guessed at.
func StorageOrigins(raw string) (origins, invalid []string) {
	seen := map[string]bool{}
	for _, entry := range strings.Split(raw, listSeparator) {
		value := strings.TrimSpace(entry)
		if value == "" {
			continue
		}
		origin, ok := originOf(value)
		switch {
		case !ok:
			invalid = append(invalid, value)
		case !seen[origin]:
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	return origins, invalid
}

func originOf(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || !allowedSchemes[u.Scheme] || u.Host == "" {
		return "", false
	}
	// `*.example.com` parses as a host name; to a policy it is a wildcard.
	if strings.Contains(u.Host, wildcard) || u.User != nil {
		return "", false
	}
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	// `https://s3.example.com/` and `https://s3.example.com` name the same
	// origin; anything past the root does not.
	if !strings.EqualFold(strings.TrimSuffix(value, rootSlash), origin) {
		return "", false
	}
	return origin, true
}

// Policy writes the headers for one deployment.
type Policy struct {
	withForms    string
	withoutForms string
}

// New builds the policy for the given storage origins.
func New(storageOrigins []string) Policy {
	storage := ""
	for _, o := range storageOrigins {
		storage += sourceSep + o
	}
	directives := append([]string{}, baseDirectives...)
	directives = append(directives,
		"img-src 'self' data: blob:"+storage,
		"connect-src 'self'"+storage,
		"frame-src 'self'"+storage,
	)
	join := func(ds []string) string {
		return strings.Join(append(ds, upgradeInsecure), directiveSep)
	}
	return Policy{
		withForms: join(append(append([]string{}, directives...), formActionSelf)),
		// The consent form posts here and is answered with a redirect to the
		// client's redirect_uri: another origin, often a custom scheme. Browsers
		// hold a form's redirects to form-action, no source list can name every
		// client's scheme, and form-action does not fall back to default-src,
		// so the consent page goes without it. It renders only its own form.
		withoutForms: join(directives),
	}
}

// CSP is the policy for a page; consent is the OAuth consent page.
func (p Policy) CSP(consent bool) string {
	if consent {
		return p.withoutForms
	}
	return p.withForms
}

// Apply sets the security headers and the page's CSP.
func (p Policy) Apply(h http.Header, consent bool) {
	for _, kv := range securityHeaders {
		h.Set(kv[0], kv[1])
	}
	h.Set(CSPHeader, p.CSP(consent))
}

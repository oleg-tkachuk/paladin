// Package web is the console's HTTP surface (ADR-0049): the static export,
// and the few things a static file cannot do — the security headers and CSP,
// the sign-in redirect, the operator's banner, the liveness probe, the OAuth
// consent relay, and the redirects from paths that moved.
//
// The cookie check here is a convenience, not a boundary: it spares an
// anonymous visitor a shell that would only bounce to the login page. The
// session role judges the cookie on every call, and the planes the token it
// attaches (ADR-0048).
package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/frontend/server/contract"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/policy"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/site"
)

const (
	// MaxConsentForm bounds the consent form relayed to the IAM plane: a
	// login, a password and the OAuth request's fields.
	MaxConsentForm = 64 << 10

	formContentType = "application/x-www-form-urlencoded"
	jsonContentType = "application/json"
	headerCache     = "Cache-Control"
	headerType      = "Content-Type"
	cacheNoStore    = "no-store"
	rscExt          = ".txt"
	pathSeparator   = "/"
)

// relayedHeaders are the parts of the AS's answer the browser needs: where to
// go, and what the body is.
var relayedHeaders = []string{"Location", "Content-Type", "Cache-Control"}

// Banner is the operator's banner as the environment sets it. The browser
// checks it again (lib/banner.ts), so the fields pass through as written.
type Banner struct {
	Content   string `json:"content"`
	Severity  string `json:"severity"`
	URL       string `json:"url"`
	Permanent string `json:"permanent"`
	Position  string `json:"position"`
}

// Config is what the handler serves.
type Config struct {
	Names  contract.Names
	Site   *site.Site
	Policy policy.Policy
	Banner Banner
	// AuthorizeURL is the IAM plane's /oauth/authorize.
	AuthorizeURL string
	// Relay dials the IAM plane; it must not follow redirects.
	Relay *http.Client
	Log   *zap.Logger
}

// RelayClient is the client for the consent relay: the AS answers with a
// redirect the browser follows, never this server.
func RelayClient(transport http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Handler is the console's handler.
func Handler(cfg Config) http.Handler {
	s := &server{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+cfg.Names.LivePath, s.live)
	mux.HandleFunc("GET "+cfg.Names.BannerPath, s.banner)
	mux.HandleFunc("POST "+cfg.Names.AuthorizePath, s.relay)
	for pattern, h := range s.legacyRedirects() {
		mux.HandleFunc(pattern, h)
	}
	mux.HandleFunc("GET /", s.static)
	return s.logged(s.headers(s.gate(mux)))
}

type server struct{ cfg Config }

func (s *server) live(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(headerType, jsonContentType)
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (s *server) banner(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(headerType, jsonContentType)
	w.Header().Set(headerCache, cacheNoStore)
	var body any // null: no banner
	if strings.TrimSpace(s.cfg.Banner.Content) != "" {
		body = s.cfg.Banner
	}
	_ = json.NewEncoder(w).Encode(body)
}

// relay passes the consent form to the AS as the browser sent it, and the
// AS's answer back unchanged: a redirect to the client's redirect_uri, or
// back to the consent page with an error. Nothing is kept.
func (s *server) relay(w http.ResponseWriter, r *http.Request) {
	log := s.cfg.Log.With(zap.String("path", r.URL.Path))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxConsentForm))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			log.Warn("consent form refused: too large", zap.Int64("limit", tooLarge.Limit))
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		log.Warn("consent form unreadable", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.AuthorizeURL, strings.NewReader(string(body)))
	if err != nil {
		log.Error("consent relay: build the request", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	req.Header.Set(headerType, formContentType)
	resp, err := s.cfg.Relay.Do(req)
	if err != nil {
		log.Error("consent relay: the IAM plane did not answer", zap.String("upstream", s.cfg.AuthorizeURL), zap.Error(err))
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, name := range relayedHeaders {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Warn("consent relay: answer cut short", zap.Error(err))
	}
	log.Info("consent form relayed", zap.Int("status", resp.StatusCode))
}

func (s *server) static(w http.ResponseWriter, r *http.Request) {
	s.cfg.Site.Serve(w, r)
}

// legacyRedirects are the paths that moved, each sent to its new place. They
// were pages that only redirected; a static export has no server to do it.
func (s *server) legacyRedirects() map[string]http.HandlerFunc {
	to := func(w http.ResponseWriter, r *http.Request, target string) {
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	}
	bucket := func(w http.ResponseWriter, r *http.Request) {
		target := "/storage-backends/" + url.PathEscape(r.PathValue("backend")) +
			"/buckets/" + url.PathEscape(r.PathValue("bucket"))
		if tab := r.PathValue("tab"); tab != "" {
			target += pathSeparator + tab
		}
		to(w, r, target)
	}
	return map[string]http.HandlerFunc{
		// The nav and older links point here; operations live on /health.
		"GET /ops": func(w http.ResponseWriter, r *http.Request) { to(w, r, "/health") },
		// A backend's buckets are listed on the backend's page (ADR-0033).
		"GET /storage-backends/{backend}/buckets": func(w http.ResponseWriter, r *http.Request) {
			to(w, r, "/storage-backends/"+url.PathEscape(r.PathValue("backend")))
		},
		// A bucket has one page, under its backend (ADR-0033); links into the
		// copy under its owner's workspace land on the same tab there.
		"GET /tenants/{tenant}/buckets/{backend}/{bucket}":          bucket,
		"GET /tenants/{tenant}/buckets/{backend}/{bucket}/{tab...}": bucket,
	}
}

// gate sends a request without the session cookie to sign in, unless its
// path is open without one.
func (s *server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.public(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(s.cfg.Names.SessionCookie); err == nil && c.Value != "" {
			next.ServeHTTP(w, r)
			return
		}
		target := s.cfg.Names.LoginPath + "?" + url.Values{s.cfg.Names.NextParam: {r.URL.RequestURI()}}.Encode()
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	})
}

// public reports a path open without a session: a public prefix, a path
// below one, or a public page's RSC payload.
func (s *server) public(p string) bool {
	for _, pre := range s.cfg.Names.PublicPrefixes {
		if p == pre || p == pre+rscExt || strings.HasPrefix(p, pre+pathSeparator) {
			return true
		}
	}
	return false
}

func (s *server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.cfg.Policy.Apply(w.Header(), r.URL.Path == s.cfg.Names.ConsentPath)
		next.ServeHTTP(w, r)
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// logged records every request at the level its outcome deserves.
func (s *server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		level, msg := zapcore.DebugLevel, "request served"
		switch {
		case rec.status >= http.StatusInternalServerError:
			level, msg = zapcore.ErrorLevel, "request failed"
		case rec.status == http.StatusMethodNotAllowed:
			level, msg = zapcore.WarnLevel, "request refused: method not allowed"
		case rec.status == http.StatusNotFound:
			msg = "request for a path no route serves"
		}
		s.cfg.Log.Log(level, msg,
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", rec.status),
			zap.Duration("duration", time.Since(start)),
		)
	})
}

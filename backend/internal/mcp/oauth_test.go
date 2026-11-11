package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes/exchange"

	apitoken "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/tests/sessiontest"
)

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name           string
		authz, xlegate string
		want           string
	}{
		{"authorization bearer", "Bearer abc.def.ghi", "", "abc.def.ghi"},
		{"bearer case-insensitive", "bearer tok", "", "tok"},
		{"x-paladin-token fallback", "", "legacy-tok", "legacy-tok"},
		{"authorization wins over x-paladin", "Bearer pref", "legacy", "pref"},
		{"neither", "", "", ""},
		{"non-bearer authorization falls back", "Basic xyz", "legacy", "legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
			if tc.authz != "" {
				r.Header.Set("Authorization", tc.authz)
			}
			if tc.xlegate != "" {
				r.Header.Set("X-Paladin-Token", tc.xlegate)
			}
			if got := BearerToken(r); got != tc.want {
				t.Errorf("BearerToken = %q, want %q", got, tc.want)
			}
		})
	}
}

// The RFC 9728 document is limes's, served where RFC 9728 §3.1 puts it for
// a resource with a path — the well-known path, then the resource's — and
// at the bare well-known path too, for a client that looks there.
func TestProtectedResourceMetadata(t *testing.T) {
	const (
		resource = "https://paladin.example.com/mcp"
		issuer   = "https://paladin.example.com"
	)
	h, err := ProtectedResourceMetadataHandler(config.MCPOAuth{
		ResourceURL:          resource,
		AuthorizationServers: []string{issuer},
		ScopesSupported:      []string{"paladin.read", "paladin.write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := ProtectedResourceMetadataPaths(resource)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{exchange.WellKnownProtectedResource + "/mcp", exchange.WellKnownProtectedResource}; !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, paths[0], nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), fmt.Sprintf("max-age=%d", int(exchange.MetadataMaxAge/time.Second)); got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	var doc exchange.ResourceMetadata
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if doc.Resource != resource || !slices.Equal(doc.AuthorizationServers, []string{issuer}) ||
		!slices.Equal(doc.BearerMethodsSupported, []string{exchange.BearerMethodHeader}) {
		t.Errorf("document = %+v", doc)
	}
}

// A resource that is not https has no document: limes refuses it, and the
// configuration does first.
func TestProtectedResourceMetadataRefusesHTTP(t *testing.T) {
	if _, err := ProtectedResourceMetadataHandler(config.MCPOAuth{
		ResourceURL: "http://localhost:8095/mcp", AuthorizationServers: []string{"https://localhost:8085"},
	}); err == nil {
		t.Error("an http resource was served")
	}
}

// oauthFakeVerifier accepts exactly one token value; everything else errors.
type oauthFakeVerifier struct{ good string }

func (f oauthFakeVerifier) Verify(_ context.Context, tok string) (*auth.Principal, error) {
	if tok == f.good {
		return &auth.Principal{Subject: "svc"}, nil
	}
	return nil, errors.New("bad token")
}

func TestRequireBearer(t *testing.T) {
	const resourceMeta = "https://paladin.example.com/.well-known/oauth-protected-resource"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := RequireBearer(next, oauthFakeVerifier{good: "valid-tok"}, resourceMeta)

	t.Run("missing token → 401 + challenge", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		wa := rec.Header().Get("WWW-Authenticate")
		if !strings.HasPrefix(wa, "Bearer ") || !strings.Contains(wa, `resource_metadata="`+resourceMeta+`"`) {
			t.Errorf("WWW-Authenticate = %q", wa)
		}
	})

	t.Run("invalid token → 401 invalid_token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer garbage")
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
			t.Errorf("WWW-Authenticate = %q, want invalid_token", rec.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("valid token passes through", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer valid-tok")
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("status = %d body = %q, want 200/ok", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid token via X-Paladin-Token fallback", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
		r.Header.Set("X-Paladin-Token", "valid-tok")
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (X-Paladin-Token back-compat)", rec.Code)
		}
	})
}

// The ADR-0036 edge: a paladin-mcp token opens a session; a plane token and
// an API token, which work against the planes directly, do not.
func TestRequireBearer_OnlyMCPAudience(t *testing.T) {
	keys := sessiontest.Keys(t)
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", Keys: keys, AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	mint := func(audience string) string {
		tok, _, err := iss.MintAccess(issuer.AccessClaims{Subject: "u", TenantID: uuid.New(), Audience: audience, OAuthClientID: "agent"})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	verifier := sessiontest.Verifier(keys, "paladin", auth.AudienceMCP)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	strict := RequireBearer(next, verifier, "", OnlyMCPAudience())
	lenient := RequireBearer(next, verifier, "")

	status := func(h http.Handler, tok string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	apiToken := apitoken.TokenPrefix + "abc"
	for _, tc := range []struct {
		name string
		h    http.Handler
		tok  string
		want int
	}{
		{"paladin-mcp", strict, mint(auth.AudienceMCP), http.StatusOK},
		{"a plane token", strict, mint(auth.AudienceData), http.StatusUnauthorized},
		{"an admin token", strict, mint(auth.AudienceAdmin), http.StatusUnauthorized},
		{"an API token", strict, apiToken, http.StatusUnauthorized},
		{"an API token, legacy edge", lenient, apiToken, http.StatusOK},
	} {
		if got := status(tc.h, tc.tok); got != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, got, tc.want)
		}
	}
}

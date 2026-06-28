package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name        string
		authz, xocp string
		want        string
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
			if tc.xocp != "" {
				r.Header.Set("X-PALADIN-Token", tc.xocp)
			}
			if got := BearerToken(r); got != tc.want {
				t.Errorf("BearerToken = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	h := ProtectedResourceMetadataHandler(config.MCPOAuth{
		ResourceURL:          "https://paladin.example.com/mcp",
		AuthorizationServers: []string{"https://paladin.example.com"},
		ScopesSupported:      []string{"paladin.read", "paladin.write"},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, WellKnownProtectedResource, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if doc["resource"] != "https://paladin.example.com/mcp" {
		t.Errorf("resource = %v", doc["resource"])
	}
	servers, _ := doc["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != "https://paladin.example.com" {
		t.Errorf("authorization_servers = %v", doc["authorization_servers"])
	}
	methods, _ := doc["bearer_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "header" {
		t.Errorf("bearer_methods_supported = %v", doc["bearer_methods_supported"])
	}
}

func TestAuthorizationServerMetadata(t *testing.T) {
	h := AuthorizationServerMetadataHandler(config.MCPOAuthAS{
		Issuer:                "https://paladin.example.com",
		AuthorizationEndpoint: "https://paladin.example.com/oauth/authorize",
		TokenEndpoint:         "https://paladin.example.com/oauth/token",
		// RegistrationEndpoint + JWKSURI left empty → must be omitted.
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, WellKnownAuthorizationServer, nil))

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if doc["issuer"] != "https://paladin.example.com" {
		t.Errorf("issuer = %v", doc["issuer"])
	}
	if _, present := doc["registration_endpoint"]; present {
		t.Error("empty registration_endpoint must be omitted")
	}
	pkce, _ := doc["code_challenge_methods_supported"].([]any)
	if len(pkce) != 1 || pkce[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v (PKCE S256 is mandatory)", doc["code_challenge_methods_supported"])
	}
	grants, _ := doc["grant_types_supported"].([]any)
	if len(grants) != 2 {
		t.Errorf("grant_types_supported = %v", doc["grant_types_supported"])
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

	t.Run("valid token via X-PALADIN-Token fallback", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
		r.Header.Set("X-PALADIN-Token", "valid-tok")
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (X-PALADIN-Token back-compat)", rec.Code)
		}
	})
}

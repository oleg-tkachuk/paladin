package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

func metadataDoc(t *testing.T, as config.MCPOAuthAS, tokenExchange bool) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	MetadataHandler(as, tokenExchange).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, WellKnownAuthorizationServer, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	return doc
}

func docStrings(doc map[string]any, key string) []string {
	raw, _ := doc[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func TestMetadataHandler(t *testing.T) {
	as := config.MCPOAuthAS{
		Issuer:                "https://paladin.example.com",
		AuthorizationEndpoint: "https://paladin.example.com/oauth/authorize",
		TokenEndpoint:         "https://paladin.example.com/oauth/token",
		// RegistrationEndpoint + JWKSURI left empty → must be omitted.
	}
	doc := metadataDoc(t, as, false)
	if doc["issuer"] != as.Issuer || doc["token_endpoint"] != as.TokenEndpoint {
		t.Errorf("issuer, token_endpoint = %v, %v", doc["issuer"], doc["token_endpoint"])
	}
	if _, present := doc["registration_endpoint"]; present {
		t.Error("empty registration_endpoint must be omitted")
	}
	if got := docStrings(doc, "code_challenge_methods_supported"); !slices.Equal(got, []string{PKCEMethodS256}) {
		t.Errorf("code_challenge_methods_supported = %v (PKCE S256 is mandatory)", got)
	}
	if got := docStrings(doc, "grant_types_supported"); !slices.Equal(got, []string{GrantTypeAuthorizationCode, GrantTypeRefreshToken}) {
		t.Errorf("grant_types_supported = %v", got)
	}

	withExchange := metadataDoc(t, as, true)
	if got := docStrings(withExchange, "grant_types_supported"); !slices.Contains(got, GrantTypeTokenExchange) {
		t.Errorf("grant_types_supported with token exchange = %v", got)
	}
}

// The revocation endpoint is published beside the token endpoint, which Mount
// serves on the same mux, and not at all when the token endpoint is
// configured somewhere Mount does not put it.
func TestMetadataHandler_RevocationEndpoint(t *testing.T) {
	cases := []struct {
		name, tokenEndpoint, want string
	}{
		{"beside the token endpoint", "https://iam.example.com/oauth/token", "https://iam.example.com/oauth/revoke"},
		{"under a path prefix", "https://example.com/iam/oauth/token", "https://example.com/iam/oauth/revoke"},
		{"a token endpoint elsewhere", "https://example.com/token", ""},
		{"no token endpoint", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := metadataDoc(t, config.MCPOAuthAS{Issuer: "https://iam.example.com", TokenEndpoint: tc.tokenEndpoint}, false)
			got, present := doc["revocation_endpoint"]
			if tc.want == "" {
				if present {
					t.Errorf("revocation_endpoint = %v, want it omitted", got)
				}
				if _, auth := doc["revocation_endpoint_auth_methods_supported"]; auth {
					t.Error("auth methods published for an omitted revocation endpoint")
				}
				return
			}
			if got != tc.want {
				t.Errorf("revocation_endpoint = %v, want %s", got, tc.want)
			}
			if m := docStrings(doc, "revocation_endpoint_auth_methods_supported"); !slices.Equal(m, tokenEndpointAuthMethodsSupported) {
				t.Errorf("revocation auth methods = %v, want the token endpoint's", m)
			}
		})
	}
}

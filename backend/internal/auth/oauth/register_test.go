package oauth

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

func registerHandler(t *testing.T, schemes []string) (*memStore, *Handler) {
	t.Helper()
	store := newMemStore()
	h := testHandlerCfg(t, store, &memRefresh{}, sampleUser(), config.OAuthAS{
		Enabled: true, DynamicRegistration: true, AuthorizationCodeTTL: time.Minute,
		AllowedRedirectSchemes: schemes,
	})
	return store, h
}

func register(t *testing.T, h *Handler, body string) (int, registerResponse) {
	t.Helper()
	rec := postJSON(h, "/oauth/register", body)
	var resp registerResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

// A client registering as RFC 7591 says — scope a space-separated string, no
// audiences — gets what it asked for, or the consentable ceiling; and an MCP
// agent, the usual self-registering client, the MCP server's audience.
func TestRegister_StandardClientMetadata(t *testing.T) {
	store, h := registerHandler(t, []string{"https"})
	h.WithMCPResource(mcpResourceURL)

	for _, tc := range []struct {
		name       string
		body       string
		wantScopes []string
	}{
		{"scope as RFC 7591 spells it", `{"redirect_uris":["https://a.example/cb"],"scope":"paladin.read"}`, []string{auth.OAuthScopeRead}},
		{"both spellings merged", `{"redirect_uris":["https://a.example/cb"],"scope":"paladin.read","scope_list":["paladin.write","paladin.read"]}`, []string{auth.OAuthScopeRead, auth.OAuthScopeWrite}},
		{"no scope: the consentable ceiling", `{"redirect_uris":["https://a.example/cb"]}`, defaultRegisteredScopes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, resp := register(t, h, tc.body)
			if code != http.StatusCreated {
				t.Fatalf("status %d", code)
			}
			got := store.clients[resp.ClientID]
			if !slices.Equal(got.AllowedScopes, tc.wantScopes) {
				t.Errorf("scopes = %v, want %v", got.AllowedScopes, tc.wantScopes)
			}
			if !slices.Equal(got.AllowedAudiences, []string{auth.AudienceMCP}) {
				t.Errorf("audiences = %v, want the MCP server's", got.AllowedAudiences)
			}
			if resp.Scope != scopesJoin(tc.wantScopes) || resp.ClientIDIssuedAt == 0 ||
				resp.TokenEndpointAuthMethod != authMethodNone || !slices.Contains(resp.GrantTypes, GrantTypeAuthorizationCode) {
				t.Errorf("response = %+v", resp)
			}
		})
	}

	// Without an MCP server to find, the data plane.
	store, plain := registerHandler(t, []string{"https"})
	_, resp := register(t, plain, `{"redirect_uris":["https://a.example/cb"]}`)
	if got := store.clients[resp.ClientID].AllowedAudiences; !slices.Equal(got, []string{auth.AudienceData}) {
		t.Errorf("audiences without an MCP server = %v", got)
	}
}

func TestRegister_RefusesAnAudienceNotToBeHad(t *testing.T) {
	_, h := registerHandler(t, []string{"https"})
	for _, aud := range []string{auth.AudienceIAM, "paladin-bogus"} {
		code, _ := register(t, h, `{"redirect_uris":["https://a.example/cb"],"audiences":["`+aud+`"]}`)
		if code != http.StatusBadRequest {
			t.Errorf("audience %s: status %d, want 400", aud, code)
		}
	}
	if code, _ := register(t, h, `{"redirect_uris":["https://a.example/cb"],"audiences":["paladin-admin"]}`); code != http.StatusCreated {
		t.Errorf("paladin-admin: status %d, want 201", code)
	}
}

// http is a native client's loopback redirect (RFC 8252) and nothing else,
// even where the operator allowed the scheme.
func TestRegister_HTTPOnlyOnLoopback(t *testing.T) {
	_, h := registerHandler(t, []string{"https", schemeHTTP})
	for uri, want := range map[string]int{
		"http://127.0.0.1:53682/callback": http.StatusCreated,
		"http://localhost:8976/cb":        http.StatusCreated,
		"http://[::1]:9000/cb":            http.StatusCreated,
		"http://agent.example/cb":         http.StatusBadRequest,
		"http://127.0.0.1.example/cb":     http.StatusBadRequest,
		"https://agent.example/cb":        http.StatusCreated,
	} {
		if code, _ := register(t, h, `{"redirect_uris":["`+uri+`"]}`); code != want {
			t.Errorf("%s: status %d, want %d", uri, code, want)
		}
	}
	// Not allowed at all unless the operator lists it.
	_, strict := registerHandler(t, []string{"https"})
	if code, _ := register(t, strict, `{"redirect_uris":["http://127.0.0.1:53682/callback"]}`); code != http.StatusBadRequest {
		t.Errorf("loopback http with only https allowed: status %d, want 400", code)
	}
}

// End to end: a client that registered itself the standard way authorizes
// for the MCP resource URL and is issued paladin-mcp.
func TestRegister_SelfRegisteredAgentReachesTheMCPServer(t *testing.T) {
	store, h := registerHandler(t, []string{"https", schemeHTTP})
	h.WithMCPResource(mcpResourceURL)
	code, resp := register(t, h, `{"client_name":"agent","redirect_uris":["http://127.0.0.1:53682/callback"],"scope":"paladin.read"}`)
	if code != http.StatusCreated {
		t.Fatalf("register: status %d", code)
	}
	client := store.clients[resp.ClientID]
	u := sampleUser()
	h2 := testHandlerCfg(t, store, &memRefresh{}, u, config.OAuthAS{
		Enabled: true, AuthorizationCodeTTL: time.Minute, AllowedRedirectSchemes: []string{"https", schemeHTTP},
	}).WithMCPResource(mcpResourceURL)
	tok := authorizeAndExchange(t, h2, u, client, mcpResourceURL)
	if got := accessAudience(t, tok.AccessToken); got != auth.AudienceMCP {
		t.Errorf("audience = %q, want %q", got, auth.AudienceMCP)
	}
}

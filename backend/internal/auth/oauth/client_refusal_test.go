package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// The secret the confidential client of these tests is registered with, and
// the wrong one it is refused for. Neither may reach a log line.
const (
	refusalClientSecret = "the-registered-client-secret"
	refusalWrongSecret  = "a-wrong-client-secret"
)

// oauthDescription is the error_description of an RFC 6749 §5.2 answer.
func oauthDescription(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body: %v (%s)", err, body)
	}
	return e.Description
}

// Each endpoint that authenticates a client answers its failure with a fixed
// description the package exports, never the error's own text, and logs the
// cause on the request's refusal line. The secret reaches neither.
func TestClientRefusalsAnswerFixedTexts(t *testing.T) {
	client := confidentialClient(t, refusalClientSecret)
	endpoints := map[string]struct {
		path string
		form url.Values
	}{
		"token, authorization_code": {TokenPath, url.Values{
			"grant_type": {GrantTypeAuthorizationCode}, "code": {"c"}, "redirect_uri": {client.RedirectURIs[0]},
		}},
		"token, refresh_token": {TokenPath, url.Values{
			"grant_type": {GrantTypeRefreshToken}, "refresh_token": {"r"},
		}},
		"revoke": {RevokePath, url.Values{revokeParamToken: {"r"}}},
	}
	cases := []struct {
		name     string
		clientID string
		secret   string
		want     string
	}{
		{"an unknown client", "nobody", "", descUnknownClient},
		{"no secret", client.ClientID, "", descClientSecretRequired},
		{"a wrong secret", client.ClientID, refusalWrongSecret, descClientSecretInvalid},
	}
	for name, ep := range endpoints {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				store := newMemStore()
				store.clients[client.ClientID] = client
				h := testHandler(t, store, &memRefresh{}, sampleUser())
				form := url.Values{"client_id": {tc.clientID}}
				for k, v := range ep.form {
					form[k] = v
				}
				if tc.secret != "" {
					form.Set("client_secret", tc.secret)
				}
				rec, logs := postMounted(t, h, ep.path, form)
				if rec.Code != http.StatusUnauthorized || oauthError(t, rec.Body.Bytes()) != errInvalidClient {
					t.Fatalf("status %d body %s, want 401 %s", rec.Code, rec.Body.String(), errInvalidClient)
				}
				desc := oauthDescription(t, rec.Body.Bytes())
				if desc != tc.want {
					t.Errorf("error_description = %q, want %q", desc, tc.want)
				}
				if !slices.Contains(ClientRefusals(), desc) {
					t.Errorf("error_description %q is not in ClientRefusals, which the guard test reads", desc)
				}
				fuzzassert.Text(t, desc, tc.clientID, tc.secret)

				lines := logs.FilterMessage(requestRefusedMessage).All()
				if len(lines) != 1 || lines[0].Level != zapcore.WarnLevel {
					t.Fatalf("refusal lines = %v, want one at warn", lines)
				}
				cause, _ := lines[0].ContextMap()[fieldCause].(string)
				if cause == "" {
					t.Errorf("the refusal line carries no %s: %v", fieldCause, lines[0].ContextMap())
				}
				for _, entry := range logs.All() {
					for k, v := range entry.ContextMap() {
						if s, ok := v.(string); ok && (strings.Contains(s, refusalClientSecret) || strings.Contains(s, refusalWrongSecret)) {
							t.Errorf("%q logs the secret in %s: %q", entry.Message, k, s)
						}
					}
				}
			})
		}
	}
}

// A token exchange for a grant that consents to neither scope is refused as
// invalid_scope with the exchange's fixed description: auth.ErrNoConsent,
// which the policy wraps into the refusal, does not reach the client.
func TestTokenExchange_NoConsentAnswersAFixedText(t *testing.T) {
	u := sampleUser()
	f := newExchangeFixture(t, u)
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", Keys: testKeys, AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	noConsent, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: u.UserID.String(), TenantID: u.TenantID, Audience: auth.AudienceMCP,
		OAuthClientID: agentWithMCPID, OAuthScopes: []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := postForm(f.h, TokenPath, exchangeForm(noConsent, auth.TokenPlaneData))
	if rec.Code != http.StatusBadRequest || oauthError(t, rec.Body.Bytes()) != "invalid_scope" {
		t.Fatalf("status %d body %s, want 400 invalid_scope", rec.Code, rec.Body.String())
	}
	desc := oauthDescription(t, rec.Body.Bytes())
	if desc == "" || strings.Contains(desc, auth.ErrNoConsent.Error()) {
		t.Errorf("error_description = %q, want the exchange's fixed text", desc)
	}
	fuzzassert.Text(t, desc)
}

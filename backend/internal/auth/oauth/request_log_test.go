package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// postMounted posts form to path through the routes Mount serves, with a
// logger on the request's context, and returns what that logger wrote.
func postMounted(t *testing.T, h *Handler, path string, form url.Values) (*httptest.ResponseRecorder, *observer.ObservedLogs) {
	t.Helper()
	mux := http.NewServeMux()
	h.Mount(mux)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContext(context.Background(), zap.New(core))
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec, logs
}

// Every request an OAuth endpoint answers leaves one line: a success at
// info, a refusal at warn with its RFC 6749 error code, whether the endpoint
// answered it as JSON or as a redirect to the client. Nothing a client sends
// as a secret reaches the line.
func TestEndpointsLogEveryRequest(t *testing.T) {
	const secret = "never-logged-secret-value"
	client := publicClient()
	cases := []struct {
		name     string
		path     string
		form     url.Values
		level    zapcore.Level
		message  string
		endpoint string
		errCode  string
	}{
		{
			name: "a grant type the endpoint does not serve", path: TokenPath,
			form:  url.Values{"grant_type": {"password"}, "client_id": {client.ClientID}, "password": {secret}},
			level: zapcore.WarnLevel, message: requestRefusedMessage, endpoint: TokenPath, errCode: "unsupported_grant_type",
		},
		{
			name: "a consent the user denies, answered by redirect", path: AuthorizePath,
			form: url.Values{
				"client_id": {client.ClientID}, "redirect_uri": {client.RedirectURIs[0]}, "scope": {grantScope},
				"code_challenge": {ComputeS256Challenge(strings.Repeat("a", 50))}, "code_challenge_method": {PKCEMethodS256},
				"state": {"st"}, "action": {"deny"}, "username": {"svc@acme"}, "password": {secret},
			},
			level: zapcore.WarnLevel, message: requestRefusedMessage, endpoint: AuthorizePath, errCode: "access_denied",
		},
		{
			name: "a revocation of a token the server never issued", path: RevokePath,
			form:  url.Values{"client_id": {client.ClientID}, "token": {secret}},
			level: zapcore.InfoLevel, message: requestServedMessage, endpoint: RevokePath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			store.clients[client.ClientID] = client
			h := testHandler(t, store, &memRefresh{}, sampleUser())
			_, logs := postMounted(t, h, tc.path, tc.form)

			lines := logs.FilterMessage(tc.message).All()
			if len(lines) != 1 {
				t.Fatalf("%d %q lines, want 1; all: %v", len(lines), tc.message, logs.All())
			}
			line := lines[0]
			if line.Level != tc.level {
				t.Errorf("level = %v, want %v", line.Level, tc.level)
			}
			fields := line.ContextMap()
			if fields[fieldEndpoint] != tc.endpoint || fields[fieldClientID] != client.ClientID {
				t.Errorf("fields = %v, want endpoint %s and client %s", fields, tc.endpoint, client.ClientID)
			}
			if got, _ := fields[fieldError].(string); got != tc.errCode {
				t.Errorf("error = %q, want %q", got, tc.errCode)
			}
			for _, entry := range logs.All() {
				if strings.Contains(fmt.Sprint(entry.ContextMap()), secret) || strings.Contains(entry.Message, secret) {
					t.Errorf("a secret the client sent was logged: %v", entry.ContextMap())
				}
			}
		})
	}
}

// A token exchange handed to limes is logged there — the exchange granted
// or refused — so the endpoint does not log it a second time. The same
// grant answered by Paladin itself, while the exchange is not configured,
// is still logged here.
func TestEndpointsLeaveADelegatedExchangeToLimes(t *testing.T) {
	logged := func(logs *observer.ObservedLogs) int {
		return logs.FilterMessage(requestServedMessage).Len() + logs.FilterMessage(requestRefusedMessage).Len()
	}
	f := newExchangeFixture(t, sampleUser())
	for name, form := range map[string]url.Values{
		"granted": exchangeForm(f.subject, auth.TokenPlaneData),
		"refused": exchangeForm("not-a-token", auth.TokenPlaneData),
	} {
		t.Run(name, func(t *testing.T) {
			if _, logs := postMounted(t, f.h, TokenPath, form); logged(logs) != 0 {
				t.Errorf("a delegated exchange was logged by the endpoint too: %v", logs.All())
			}
		})
	}
	t.Run("not configured", func(t *testing.T) {
		store := newMemStore()
		h := testHandler(t, store, &memRefresh{}, sampleUser())
		if _, logs := postMounted(t, h, TokenPath, exchangeForm("not-a-token")); logged(logs) != 1 {
			t.Errorf("%d lines for an exchange Paladin refused itself, want 1: %v", logged(logs), logs.All())
		}
	})
}

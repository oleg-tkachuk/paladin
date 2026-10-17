package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const (
	testExchangeClient = "paladin-mcp-server"
	testExchangeSecret = "s3cret+/=" // characters form encoding would change
	testExpiresIn      = 300
)

// fakeAS is an Authorization Server that checks the exchange request and
// answers with a token naming the subject, audience and call number.
type fakeAS struct {
	calls  atomic.Int32
	refuse bool
	// tokenType overrides the issued_token_type when set.
	tokenType string
}

func (f *fakeAS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := f.calls.Add(1)
	user, pass, ok := r.BasicAuth()
	_ = r.ParseForm()
	switch {
	case r.URL.Path != oauth.TokenPath, !ok, user != testExchangeClient, pass != testExchangeSecret,
		r.PostForm.Get("grant_type") != oauth.GrantTypeTokenExchange,
		r.PostForm.Get("subject_token_type") != oauth.TokenTypeAccessToken,
		r.PostForm.Get("client_id") != testExchangeClient:
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "bad request shape"})
		return
	case f.refuse:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_target", "error_description": "no"})
		return
	}
	tokenType := oauth.TokenTypeAccessToken
	if f.tokenType != "" {
		tokenType = f.tokenType
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":      fmt.Sprintf("%s@%s#%d", r.PostForm.Get("subject_token"), r.PostForm.Get("audience"), n),
		"issued_token_type": tokenType,
		"token_type":        "Bearer",
		"expires_in":        testExpiresIn,
	})
}

type exchangeHarness struct {
	as  *fakeAS
	ex  *Exchanger
	now time.Time
}

func newExchangeHarness(t *testing.T) *exchangeHarness {
	t.Helper()
	h := &exchangeHarness{as: &fakeAS{}, now: time.Unix(1_800_000_000, 0)}
	srv := httptest.NewServer(h.as)
	t.Cleanup(srv.Close)
	h.ex = NewExchanger(srv.Client(), srv.URL+oauth.TokenPath, testExchangeClient, testExchangeSecret)
	h.ex.now = func() time.Time { return h.now }
	return h
}

func (h *exchangeHarness) token(t *testing.T, subject, audience string) string {
	t.Helper()
	tok, err := h.ex.Token(context.Background(), subject, audience)
	if err != nil {
		t.Fatalf("Token(%s, %s): %v", subject, audience, err)
	}
	return tok
}

func TestExchanger_CachesPerSubjectAndAudience(t *testing.T) {
	h := newExchangeHarness(t)
	first := h.token(t, "alice", auth.AudienceData)
	if first != "alice@"+auth.AudienceData+"#1" {
		t.Fatalf("token = %q", first)
	}
	if again := h.token(t, "alice", auth.AudienceData); again != first {
		t.Errorf("a second call exchanged again: %q", again)
	}
	h.token(t, "alice", auth.AudienceAdmin)
	h.token(t, "bob", auth.AudienceData)
	if got := h.as.calls.Load(); got != 3 {
		t.Errorf("exchanges = %d, want 3: one per subject and audience", got)
	}
}

func TestExchanger_RenewsBeforeExpiry(t *testing.T) {
	h := newExchangeHarness(t)
	first := h.token(t, "alice", auth.AudienceData)
	lifetime := testExpiresIn * time.Second

	h.now = h.now.Add(lifetime - paladin.TokenRefreshMargin - time.Second)
	if got := h.token(t, "alice", auth.AudienceData); got != first {
		t.Errorf("renewed before the margin: %q", got)
	}
	h.now = h.now.Add(time.Second)
	if got := h.token(t, "alice", auth.AudienceData); got == first {
		t.Error("served a token inside its refresh margin")
	}
}

func TestExchanger_InvalidateDropsThatAudience(t *testing.T) {
	h := newExchangeHarness(t)
	data := h.token(t, "alice", auth.AudienceData)
	admin := h.token(t, "alice", auth.AudienceAdmin)
	h.ex.Invalidate(auth.AudienceData)
	if got := h.token(t, "alice", auth.AudienceData); got == data {
		t.Error("an invalidated token was served again")
	}
	if got := h.token(t, "alice", auth.AudienceAdmin); got != admin {
		t.Errorf("another audience's token was dropped: %q", got)
	}
}

func TestExchanger_Refusals(t *testing.T) {
	h := newExchangeHarness(t)
	h.as.refuse = true
	if _, err := h.ex.Token(context.Background(), "alice", auth.AudienceAdmin); !errors.Is(err, ErrExchangeRefused) {
		t.Fatalf("err = %v, want ErrExchangeRefused", err)
	}
	h.as.refuse = false
	if _, err := h.ex.Token(context.Background(), "alice", auth.AudienceAdmin); err != nil {
		t.Errorf("a refusal was cached: %v", err)
	}

	h.as.tokenType = "urn:ietf:params:oauth:token-type:refresh_token"
	if _, err := h.ex.Token(context.Background(), "bob", auth.AudienceData); err == nil {
		t.Error("a token of another type was accepted")
	}

	wrong := NewExchanger(nil, h.ex.tokenURL, testExchangeClient, "wrong")
	if _, err := wrong.Token(context.Background(), "alice", auth.AudienceData); !errors.Is(err, ErrExchangeRefused) {
		t.Errorf("wrong secret: err = %v, want ErrExchangeRefused", err)
	}
}

func TestExchanger_CacheIsBounded(t *testing.T) {
	h := newExchangeHarness(t)
	for i := range exchangeCacheMaxEntries + 1 {
		h.token(t, fmt.Sprintf("agent-%d", i), auth.AudienceData)
	}
	h.ex.mu.Lock()
	n := len(h.ex.cache)
	h.ex.mu.Unlock()
	if n > exchangeCacheMaxEntries {
		t.Errorf("cache holds %d entries, bound is %d", n, exchangeCacheMaxEntries)
	}
	// Expired entries go first: with half of a full cache stale, an insert
	// keeps the live half rather than dropping everything.
	h.ex.mu.Lock()
	clear(h.ex.cache)
	for i := range exchangeCacheMaxEntries {
		expires := h.now // stale
		if i%2 == 0 {
			expires = h.now.Add(time.Hour) // live
		}
		h.ex.cache[exchangeKey{subject: fmt.Sprint("entry-", i), audience: auth.AudienceData}] = exchanged{expires: expires}
	}
	h.ex.mu.Unlock()
	h.token(t, "fresh", auth.AudienceData)
	h.ex.mu.Lock()
	n = len(h.ex.cache)
	h.ex.mu.Unlock()
	if want := exchangeCacheMaxEntries/2 + 1; n != want {
		t.Errorf("after evicting the expired, cache holds %d, want %d", n, want)
	}
}

// The cache keys on a digest: no caller token sits in it.
func TestExchanger_KeepsNoSubjectToken(t *testing.T) {
	h := newExchangeHarness(t)
	h.token(t, "alice-secret-token", auth.AudienceData)
	h.ex.mu.Lock()
	defer h.ex.mu.Unlock()
	for k := range h.ex.cache {
		if k.subject == "alice-secret-token" {
			t.Error("the subject token is a cache key in the clear")
		}
	}
}

func TestExchangedTokens(t *testing.T) {
	h := newExchangeHarness(t)
	src := exchangedTokens{defaults: Credentials{Bearer: "stdio-token"}, ex: h.ex}

	// stdio: no MCP request on ctx, the default bearer unexchanged.
	if got, _ := src.Token(context.Background(), auth.AudienceData); got != "stdio-token" {
		t.Errorf("no request: %q, want the default bearer", got)
	}
	// An HTTP request's bearer is exchanged for the plane.
	ctx := context.WithValue(context.Background(), credentialsKey{}, Credentials{Bearer: "alice"})
	if got, _ := src.Token(ctx, auth.AudienceAdmin); got != "alice@"+auth.AudienceAdmin+"#1" {
		t.Errorf("request bearer: %q, want it exchanged", got)
	}
	// A request with only a capability sends no bearer, and exchanges nothing.
	capOnly := context.WithValue(context.Background(), credentialsKey{}, Credentials{Capability: "cap"})
	if got, _ := src.Token(capOnly, auth.AudienceData); got != "" {
		t.Errorf("capability only: %q, want none", got)
	}
	if got := h.as.calls.Load(); got != 1 {
		t.Errorf("exchanges = %d, want 1", got)
	}
	// Invalidate reaches the exchanger, as the SDK's retry needs.
	src.Invalidate(auth.AudienceAdmin)
	if got, _ := src.Token(ctx, auth.AudienceAdmin); got == "alice@"+auth.AudienceAdmin+"#1" {
		t.Error("Invalidate did not reach the exchanger")
	}
}

// Each refusal keeps a code that says whose problem it is, so the SDK does not
// read it as the plane being down.
func TestExchangeRefusalCodes(t *testing.T) {
	for _, tc := range []struct {
		oauth string
		want  connect.Code
	}{
		{oauthErrInvalidGrant, connect.CodeUnauthenticated},
		{oauthErrInvalidTarget, connect.CodePermissionDenied},
		{oauthErrSlowDown, connect.CodeResourceExhausted},
		{"invalid_client", connect.CodeInternal},
		{"", connect.CodeInternal},
	} {
		r := &ExchangeRefusal{Code: tc.oauth}
		if got := r.connectCode(); got != tc.want {
			t.Errorf("%q: code = %v, want %v", tc.oauth, got, tc.want)
		}
	}
}

// What an agent reads when its token could not be exchanged: why, from the
// Authorization Server, through the SDK's own wrapping of a token failure —
// not "the admin plane could not complete the call; try again later".
func TestToolErrorExplainsARefusedExchange(t *testing.T) {
	const why = "the user may not be issued paladin-admin"
	refusal := &ExchangeRefusal{Audience: auth.AudienceAdmin, Code: oauthErrInvalidTarget, Description: why}
	fromExchanger := connect.NewError(refusal.connectCode(), refusal.Error()).WithCause(refusal)
	// paladin.tokenError: the Connect code kept, the error as the cause.
	fromSDK := connect.NewError(connect.CodeOf(fromExchanger), fromExchanger.Error()).WithCause(fromExchanger)

	got := toolError("paladin_list_backends", fromSDK).Error()
	if !strings.Contains(got, why) || !strings.Contains(got, "admin plane") {
		t.Errorf("toolError = %q, want the plane and the reason", got)
	}
	if strings.Contains(got, "try again later") {
		t.Errorf("toolError = %q reads a refusal as an outage", got)
	}

	// Without a description the OAuth code stands in.
	bare := &ExchangeRefusal{Code: oauthErrInvalidGrant}
	if got := toolError("paladin_list_backends", bare).Error(); !strings.Contains(got, oauthErrInvalidGrant) {
		t.Errorf("toolError = %q, want the OAuth code", got)
	}
}

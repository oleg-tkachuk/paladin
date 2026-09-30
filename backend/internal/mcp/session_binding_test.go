package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// twoUsers accepts two tokens for two different subjects.
type twoUsers struct{}

func (twoUsers) Verify(_ context.Context, tok string) (*auth.Principal, error) {
	switch tok {
	case "alice-tok", "alice-tok-refreshed":
		return &auth.Principal{Subject: "alice"}, nil
	case "bob-tok":
		return &auth.Principal{Subject: "bob"}, nil
	}
	return nil, errors.New("bad token")
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

func edgeServer(t *testing.T, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "t", Version: "0"}, nil)
	h := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv }, nil)
	ts := httptest.NewServer(wrap(h))
	t.Cleanup(ts.Close)
	return ts
}

// reply is what the tests read of a response; post closes the body itself.
type reply struct {
	StatusCode int
	Header     http.Header
}

func post(t *testing.T, url, header, token, session, body string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		if header == "Authorization" {
			token = "Bearer " + token
		}
		req.Header.Set(header, token)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}
}

func openSession(t *testing.T, url, header, token string) string {
	t.Helper()
	resp := post(t, url, header, token, "", initBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize with %q: status %d", token, resp.StatusCode)
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("no session id")
	}
	return sid
}

const listBody = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`

func TestRequireBearerBindsSessionToPrincipal(t *testing.T) {
	ts := edgeServer(t, func(h http.Handler) http.Handler { return RequireBearer(h, twoUsers{}, "") })
	sid := openSession(t, ts.URL, "Authorization", "alice-tok")

	// A refreshed token for the same principal keeps the session.
	if got := post(t, ts.URL, "Authorization", "alice-tok-refreshed", sid, listBody).StatusCode; got != http.StatusOK {
		t.Errorf("same principal, new token: status %d, want 200", got)
	}
	// Another principal who learned the session id is refused.
	if got := post(t, ts.URL, "Authorization", "bob-tok", sid, listBody).StatusCode; got == http.StatusOK {
		t.Errorf("another principal drove the session: status %d", got)
	}
}

// A garbage token used to open a session whenever OAuth was off; the edge
// only checked that a header was present.
func TestRequireBearerRefusesAnUnverifiableToken(t *testing.T) {
	ts := edgeServer(t, func(h http.Handler) http.Handler { return RequireBearer(h, twoUsers{}, "") })
	resp := post(t, ts.URL, "Authorization", "garbage", "", initBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
	}
}

// An API token is verified by the planes, not here, and is bound by its hash.
func TestRequireBearerPassesAPITokensAndBindsThem(t *testing.T) {
	ts := edgeServer(t, func(h http.Handler) http.Handler { return RequireBearer(h, twoUsers{}, "") })
	sid := openSession(t, ts.URL, "Authorization", "paladin_pat_one")
	if got := post(t, ts.URL, "Authorization", "paladin_pat_two", sid, listBody).StatusCode; got == http.StatusOK {
		t.Errorf("a different API token drove the session: status %d", got)
	}
}

// The legacy header reaches the SDK too, which reads Authorization only.
func TestRequireBearerAcceptsTheLegacyHeader(t *testing.T) {
	ts := edgeServer(t, func(h http.Handler) http.Handler { return RequireBearer(h, twoUsers{}, "") })
	openSession(t, ts.URL, "X-Paladin-Token", "alice-tok")
}

func TestRequireTokenBindsSessionToTheCredential(t *testing.T) {
	ts := edgeServer(t, RequireToken)
	sid := openSession(t, ts.URL, "Authorization", "tok-a")
	if got := post(t, ts.URL, "Authorization", "tok-a", sid, listBody).StatusCode; got != http.StatusOK {
		t.Errorf("same credential: status %d, want 200", got)
	}
	if got := post(t, ts.URL, "Authorization", "tok-b", sid, listBody).StatusCode; got == http.StatusOK {
		t.Errorf("another credential drove the session: status %d", got)
	}
}

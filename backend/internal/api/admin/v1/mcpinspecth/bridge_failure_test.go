package mcpinspecth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// timeoutErr is a transport error that reports itself as a timeout, as the
// HTTP client's does when its deadline passes.
type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }

// The bridge status an admin reads is a fixed verdict: never the transport's
// own text, which names the replica's URL and whatever the remote chose.
func TestBridgeVerdict(t *testing.T) {
	const target = "http://user:s3cret@mcp-0.mcp:8095/status?token=t0ken"
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"nobody asked":  {nil, msgNoReplicaAnswered},
		"dial refused":  {&url.Error{Op: "Get", URL: target, Err: errors.New("dial tcp 10.0.0.7:8095: connect: connection refused")}, msgBridgeUnreachable},
		"timed out":     {&url.Error{Op: "Get", URL: target, Err: timeoutErr{}}, msgBridgeTimedOut},
		"deadline":      {fmt.Errorf("get: %w", context.DeadlineExceeded), msgBridgeTimedOut},
		"bad status":    {&bridgeStatusError{status: http.StatusServiceUnavailable}, "the MCP server answered HTTP 503"},
		"unreadable":    {&bridgeAnswerError{cause: errors.New("invalid character '<' looking for beginning of value")}, msgBridgeUnreadable},
		"anything else": {errors.New("tls: " + target), msgBridgeUnreachable},
	} {
		t.Run(name, func(t *testing.T) {
			got := bridgeVerdict(tc.err)
			if got != tc.want {
				t.Errorf("verdict %q, want %q", got, tc.want)
			}
			for _, leak := range []string{"s3cret", "t0ken", "mcp-0", "10.0.0.7", "invalid character"} {
				if strings.Contains(got, leak) {
					t.Errorf("verdict %q carries %q", got, leak)
				}
			}
		})
	}
}

// What the RPC answers for a replica that answered with a failure status.
func TestGetBridgeStatusAnswersAClassifiedVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	h := newTestHandler(t, srv.URL+"/sessions")

	res, err := h.GetBridgeStatus(ctxAs("admin"), &adminv1.GetBridgeStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "the MCP server answered HTTP 502"; res.GetError() != want {
		t.Errorf("error %q, want %q", res.GetError(), want)
	}
}

// A replica nobody listens on is unreachable, and the answer does not name it.
func TestGetBridgeStatusHidesTheReplicaURL(t *testing.T) {
	h := newTestHandler(t, "http://127.0.0.1:1/sessions")
	res, err := h.GetBridgeStatus(ctxAs("admin"), &adminv1.GetBridgeStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetError() != msgBridgeUnreachable {
		t.Errorf("error %q, want %q", res.GetError(), msgBridgeUnreachable)
	}
}

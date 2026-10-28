package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

const readTool = "paladin_list_backends"

// A tool call the platform failed is logged with its cause before the model
// is told to try again; one that describes the request is the model's answer.
func TestLogToolFailure(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		logged bool
	}{
		{"an unreachable plane", connect.NewError(connect.CodeUnavailable, "dial tcp 10.0.0.1:8080: connection refused"), true},
		{"an internal error", connect.NewError(connect.CodeInternal, "pool closed"), true},
		{"a transport error", errors.New("context deadline exceeded"), true},
		{"a denial", connect.NewError(connect.CodePermissionDenied, "denied by policy"), false},
		{"a missing object", connect.NewError(connect.CodeNotFound, "no backend"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			logToolFailure(logger.WithContext(context.Background(), zap.New(core)), readTool, c.err)
			got := logs.FilterMessage(toolFailedMessage).All()
			if logged := len(got) == 1; logged != c.logged {
				t.Fatalf("logged = %v, want %v", logged, c.logged)
			}
			if c.logged && got[0].ContextMap()["tool"] != readTool {
				t.Errorf("fields = %v", got[0].ContextMap())
			}
		})
	}
}

// The SDK's own reports reach the service's log; without a logger it keeps
// its discarding default.
func TestSDKLogger(t *testing.T) {
	if SDKLogger(nil) != nil {
		t.Error("a nil logger made an SDK logger")
	}
	core, logs := observer.New(zap.DebugLevel)
	SDKLogger(zap.New(core)).Error("failed to connect")
	if logs.FilterMessage("failed to connect").Len() != 1 {
		t.Error("the SDK's line did not reach the log")
	}
}

type refusingVerifier struct{}

var errExpired = errors.New("jwt: token is expired")

func (refusingVerifier) Verify(context.Context, string) (*auth.Principal, error) {
	return nil, errExpired
}

// The edge logs why it refused a token, never the token.
func TestRequireBearerLogsWhy(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.e30.sig"
	core, logs := observer.New(zap.DebugLevel)
	h := RequireBearer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a refused token reached the server")
	}), refusingVerifier{}, "https://mcp.example/.well-known/oauth-protected-resource")
	req := httptest.NewRequestWithContext(logger.WithContext(context.Background(), zap.New(core)), http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	got := logs.FilterMessage(tokenRefusedMessage).All()
	if len(got) != 1 || got[0].ContextMap()["error"] != errExpired.Error() {
		t.Fatalf("logged %v, want the verifier's reason", got)
	}
	for _, e := range logs.All() {
		for k, v := range e.ContextMap() {
			if s, ok := v.(string); ok && s == token {
				t.Errorf("field %s carries the token", k)
			}
		}
	}
}

// Through a real call: a plane that is down leaves a line naming the tool,
// though the model is only told to try again.
func TestToolCallToADownPlaneIsLogged(t *testing.T) {
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(plane.Close)
	core, logs := observer.New(zap.DebugLevel)
	// The in-process session's handlers run on a context with no logger of
	// its own, so the line goes to the global one.
	t.Cleanup(zap.ReplaceGlobals(zap.New(core)))

	cs := dialInProcess(t, mustClients(t)(NewClientsWithCapability(plane.Client(), plane.URL, plane.URL, plane.URL, "", "cap")))
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: readTool, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("a call to a down plane succeeded")
	}
	got := logs.FilterMessage(toolFailedMessage).All()
	if len(got) != 1 || got[0].ContextMap()["tool"] != readTool {
		t.Errorf("logged %v, want one line naming %s", got, readTool)
	}
}

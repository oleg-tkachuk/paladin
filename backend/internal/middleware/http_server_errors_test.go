package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

const (
	plainPath = "/oauth/token"
	rpcPath   = "/paladin.iam.v1.AuthService/Login"
	clientID  = "req-from-the-client"
)

func serve(t *testing.T, path, requestID string, status int) (*httptest.ResponseRecorder, *observer.ObservedLogs, string) {
	t.Helper()
	core, logs := observer.New(zap.InfoLevel)
	var seen string
	h := LogHTTPServerErrors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(HeaderRequestID)
		w.WriteHeader(status)
	}))
	req := httptest.NewRequestWithContext(logger.WithContext(context.Background(), zap.New(core)), http.MethodPost, path, nil)
	if requestID != "" {
		req.Header.Set(HeaderRequestID, requestID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, logs, seen
}

func TestLogHTTPServerErrors(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		status int
		logged bool
	}{
		{name: "a server error", path: plainPath, status: http.StatusInternalServerError, logged: true},
		{name: "an unavailable upstream", path: plainPath, status: http.StatusServiceUnavailable, logged: true},
		{name: "a client error", path: plainPath, status: http.StatusBadRequest},
		{name: "a success", path: plainPath, status: http.StatusOK},
		// LogOutcome logs these, with the RPC's own cause.
		{name: "an RPC", path: rpcPath, status: http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, logs, _ := serve(t, c.path, clientID, c.status)
			got := logs.FilterMessage(httpFailedMessage).All()
			if logged := len(got) == 1; logged != c.logged {
				t.Fatalf("logged = %v (%d lines), want %v", logged, len(got), c.logged)
			}
			if !c.logged {
				return
			}
			f := got[0].ContextMap()
			if f["path"] != c.path || f["status"] != int64(c.status) || f["request_id"] != clientID || f["method"] != http.MethodPost {
				t.Errorf("fields = %v", f)
			}
		})
	}
}

// The client's request id is kept and echoed; a request without one gets one.
func TestLogHTTPServerErrorsRequestID(t *testing.T) {
	rec, _, seen := serve(t, plainPath, clientID, http.StatusOK)
	if seen != clientID || rec.Header().Get(HeaderRequestID) != clientID {
		t.Errorf("handler saw %q, response says %q, want %q", seen, rec.Header().Get(HeaderRequestID), clientID)
	}
	rec, _, seen = serve(t, plainPath, "", http.StatusOK)
	if seen == "" || rec.Header().Get(HeaderRequestID) != seen {
		t.Errorf("minted %q, response says %q", seen, rec.Header().Get(HeaderRequestID))
	}
}

// A streamed response still streams: the recorder is a Flusher over one.
func TestStatusRecorderFlushes(t *testing.T) {
	rec := httptest.NewRecorder()
	var w http.ResponseWriter = &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	f, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("the recorder is not a Flusher")
	}
	f.Flush()
	if !rec.Flushed {
		t.Error("Flush did not reach the wrapped writer")
	}
	if http.NewResponseController(w).Flush() != nil {
		t.Error("ResponseController cannot flush through the recorder")
	}
}

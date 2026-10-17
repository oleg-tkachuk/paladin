package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// errStoreDown is what a store that cannot be reached answers.
var errStoreDown = errors.New("dial tcp: connection refused")

// upsertFailing is a store whose writes fail, as one with its database down.
type upsertFailing struct{ *memStore }

func (upsertFailing) UpsertClient(context.Context, Client) error { return errStoreDown }

// A server error the endpoints answer leaves its cause in the log, under the
// request's own logger: these are plain HTTP handlers, which no RPC
// interceptor logs for.
func TestServerErrorIsLoggedWithItsCause(t *testing.T) {
	h := testHandler(t, upsertFailing{newMemStore()}, &memRefresh{}, sampleUser())
	core, logs := observer.New(zap.ErrorLevel)
	ctx := logger.WithContext(t.Context(), zap.New(core))
	body := `{"client_name":"agent","redirect_uris":["https://agent.example/cb"],"token_endpoint_auth_method":"none"}`
	rec := httptest.NewRecorder()
	h.handleRegister(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, "/oauth/register", strings.NewReader(body)))

	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), errServerError) {
		t.Fatalf("status = %d body = %s, want 500 server_error", rec.Code, rec.Body.String())
	}
	got := logs.FilterMessage(serverErrorMessage).All()
	if len(got) != 1 {
		t.Fatalf("logged %d server-error lines, want 1", len(got))
	}
	fields := got[0].ContextMap()
	if fields["reason"] != "registration failed" || !strings.Contains(fields["error"].(string), errStoreDown.Error()) {
		t.Errorf("fields = %v, want the reason and the store's error", fields)
	}
	// The client is told the reason, never the cause.
	if strings.Contains(rec.Body.String(), errStoreDown.Error()) {
		t.Error("the response carries the internal error")
	}
}

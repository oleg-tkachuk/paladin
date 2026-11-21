package mcpinspecth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	mcppkg "github.com/oleg-tkachuk/paladin/backend/internal/mcp"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

func observedHandler(cfg config.MCP) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewHandler(cfg, allowAll())
	h.SetLogger(zap.New(core))
	return h, logs
}

// A replica that fails is skipped so one bad pod cannot blank the page, and
// it used to be skipped without a word: the operator saw fewer sessions and
// nothing said which pod was missing.
func TestListSessionsLogsASkippedReplica(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	h, logs := observedHandler(config.MCP{})
	h.sessionsURL = srv.URL
	h.httpClient = srv.Client()

	if _, err := h.ListSessions(ctxAs("admin"), &adminv1.ListSessionsRequest{}); err != nil {
		t.Fatal(err)
	}
	lines := logs.FilterLevelExact(zapcore.WarnLevel).FilterField(zap.String("target", srv.URL)).All()
	if len(lines) != 1 {
		t.Fatalf("want one warn line naming the replica, got %v", logs.All())
	}
}

// A bridge nobody could ask is the answer GetBridgeStatus gives, and the log
// says so too.
func TestGetBridgeStatusLogsAnUnreachableBridge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	h, logs := observedHandler(config.MCP{})
	h.sessionsURL = srv.URL
	h.httpClient = srv.Client()

	res, err := h.GetBridgeStatus(ctxAs("admin"), &adminv1.GetBridgeStatusRequest{})
	if err != nil || res.GetReachable() || res.GetError() == "" {
		t.Fatalf("status = %+v, %v; want unreachable with an error", res, err)
	}
	if logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("mcp bridge unreachable").Len() != 1 {
		t.Errorf("want one warn line, got %v", logs.All())
	}
}

func TestInspectLogsAnUnreadableClientStore(t *testing.T) {
	h, logs := observedHandler(config.MCP{})
	h.WithOAuthClients(fakeClients{err: errors.New("pool closed")})
	if _, err := h.Inspect(ctxAs("admin"), &adminv1.MCPInspectRequest{}); err != nil {
		t.Fatal(err)
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Errorf("want one error line, got %v", logs.All())
	}
}

func TestBridgeAnswered(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusOK: true, http.StatusNoContent: true,
		http.StatusSwitchingProtocols: false, http.StatusMultipleChoices: false, http.StatusForbidden: false,
	} {
		if got := bridgeAnswered(status); got != want {
			t.Errorf("bridgeAnswered(%d) = %v, want %v", status, got, want)
		}
	}
}

func sessions(n int) []mcppkg.SessionInfo {
	t0 := time.Unix(1_700_000_000, 0).UTC()
	out := make([]mcppkg.SessionInfo, n)
	for i := range out {
		out[i] = mcppkg.SessionInfo{ID: fmt.Sprintf("s%02d", i), StartedAt: t0.Add(time.Duration(i) * time.Second)}
	}
	return out
}

func ids(infos []mcppkg.SessionInfo) string {
	s := ""
	for _, i := range infos {
		s += i.ID + ","
	}
	return s
}

// Paging the merged list: a page of the size asked for (or the default), a
// cursor only when more follow, and a cursor that names a reaped session
// restarting the listing.
func TestPageSessions(t *testing.T) {
	all := sessions(5)
	for _, tc := range []struct {
		name     string
		page     *commonv1.PageRequest
		want     string
		wantNext string
	}{
		{"first page", &commonv1.PageRequest{PageSize: 2}, "s00,s01,", "s01"},
		{"after a cursor", &commonv1.PageRequest{PageSize: 2, PageToken: "s01"}, "s02,s03,", "s03"},
		{"last page exactly", &commonv1.PageRequest{PageSize: 1, PageToken: "s03"}, "s04,", ""},
		{"page past the end", &commonv1.PageRequest{PageSize: 10, PageToken: "s02"}, "s03,s04,", ""},
		{"unknown cursor restarts", &commonv1.PageRequest{PageSize: 2, PageToken: "gone"}, "s00,s01,", "s01"},
		{"default size", nil, "s00,s01,s02,s03,s04,", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, next := pageSessions(all, tc.page)
			if ids(got) != tc.want || next != tc.wantNext {
				t.Errorf("page = %s next %q, want %s next %q", ids(got), next, tc.want, tc.wantNext)
			}
		})
	}
}

func TestPageSessionsDefaultSize(t *testing.T) {
	got, next := pageSessions(sessions(defaultSessionPageSize+1), nil)
	if len(got) != defaultSessionPageSize || next == "" {
		t.Errorf("page of %d next %q, want %d and a cursor", len(got), next, defaultSessionPageSize)
	}
}

func TestSortSessionsBreaksTiesByID(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0).UTC()
	infos := []mcppkg.SessionInfo{{ID: "b", StartedAt: t0}, {ID: "a", StartedAt: t0}, {ID: "c", StartedAt: t0.Add(-time.Second)}}
	sortSessions(infos)
	if ids(infos) != "c,a,b," {
		t.Errorf("order = %s, want c,a,b,", ids(infos))
	}
}

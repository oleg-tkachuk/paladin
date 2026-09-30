package mcpinspecth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcppkg "github.com/oleg-tkachuk/paladin/backend/internal/mcp"
)

func TestSessionTargets_Fallbacks(t *testing.T) {
	// Literal IP, unparseable, and empty all collapse to the URL verbatim —
	// only a resolvable multi-address host fans out (DNS-dependent, not unit
	// tested here).
	for name, u := range map[string]string{
		"literal IP": "http://10.0.0.5:8095/sessions",
		"bad url":    "://nope",
		"empty":      "",
	} {
		h := &Handler{sessionsURL: u}
		got := h.sessionTargets(context.Background())
		if len(got) != 1 || got[0] != u {
			t.Errorf("%s: sessionTargets=%v, want [%q]", name, got, u)
		}
	}
}

func TestFetchSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode([]mcppkg.SessionInfo{{ID: "s1", ToolCallCount: 2}})
	}))
	defer srv.Close()
	h := &Handler{httpClient: srv.Client()}

	infos, err := h.fetchSessions(context.Background(), srv.URL, "Bearer t")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "s1" || infos[0].ToolCallCount != 2 {
		t.Fatalf("infos=%+v", infos)
	}
	if _, err := h.fetchSessions(context.Background(), srv.URL, "wrong"); err == nil {
		t.Error("expected error on 403 status")
	}
}

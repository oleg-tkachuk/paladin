package mcpinspecth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	mcppkg "github.com/oleg-tkachuk/paladin/backend/internal/mcp"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// ─── helpers ───────────────────────────────────────────────────────────────

type fakeAuthorizer struct {
	decision cedar.Decision
	err      error

	gotAction    string
	gotPrincipal *cedar.Principal
	calls        int
}

func (a *fakeAuthorizer) IsAuthorized(_ context.Context, p *cedar.Principal, action string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.calls++
	a.gotAction, a.gotPrincipal = action, p
	return a.decision, a.err
}

func allowAll() *fakeAuthorizer { return &fakeAuthorizer{decision: cedar.DecisionAllow} }

func ctxAs(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", TenantID: uuid.New(), TenantSlug: "acme", Roles: roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// ─── NewHandler ────────────────────────────────────────────────────────────

func TestNewHandlerCarriesConfig(t *testing.T) {
	h := NewHandler(config.MCP{
		HTTP: config.MCPHTTP{SessionsURL: "http://mcp:8095/sessions"},
	}, allowAll())

	if h.sessionsURL != "http://mcp:8095/sessions" {
		t.Errorf("sessionsURL = %q", h.sessionsURL)
	}
	// A missing timeout would let one hung MCP replica stall an admin RPC.
	if h.httpClient == nil || h.httpClient.Timeout == 0 {
		t.Error("the proxy client must carry a timeout")
	}
}

// A nil authorizer would fail open on every inspect RPC, so construction must
// refuse it loudly rather than defer the crash to the first request.
func TestNewHandlerPanicsWithoutAuthorizer(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want a panic for a nil authorizer")
		}
	}()
	NewHandler(config.MCP{}, nil)
}

// ─── authorize ─────────────────────────────────────────────────────────────

func TestAuthorizeRejectsAnonymous(t *testing.T) {
	h := NewHandler(config.MCP{}, allowAll())

	_, err := h.Inspect(context.Background(), connect.NewRequest(&adminv1.MCPInspectRequest{}))
	if code(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code(err))
	}
}

func TestAuthorizeDeniedByPolicy(t *testing.T) {
	az := &fakeAuthorizer{decision: cedar.DecisionDeny}
	h := NewHandler(config.MCP{}, az)

	_, err := h.Inspect(ctxAs("viewer"), connect.NewRequest(&adminv1.MCPInspectRequest{}))
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestAuthorizePolicyErrorIsInternal(t *testing.T) {
	az := &fakeAuthorizer{err: errors.New("cedar exploded")}
	h := NewHandler(config.MCP{}, az)

	_, err := h.Inspect(ctxAs("admin"), connect.NewRequest(&adminv1.MCPInspectRequest{}))
	if code(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", code(err))
	}
}

// The inspect surface is read-only, so it asks for its own read-only action —
// one the schema declares, not a bare word no policy could ever name.
func TestAuthorizeRequestsInspectMCP(t *testing.T) {
	az := allowAll()
	h := NewHandler(config.MCP{}, az)

	if _, err := h.Inspect(ctxAs("admin"), connect.NewRequest(&adminv1.MCPInspectRequest{})); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if az.gotAction != cedar.ActionInspectMCP {
		t.Errorf("action = %q, want %q", az.gotAction, cedar.ActionInspectMCP)
	}
	if az.gotPrincipal == nil || az.gotPrincipal.Subject != "tester" || az.gotPrincipal.TenantSlug != "acme" {
		t.Errorf("principal not forwarded: %+v", az.gotPrincipal)
	}
}

func TestListSessionsIsGatedToo(t *testing.T) {
	h := NewHandler(config.MCP{}, &fakeAuthorizer{decision: cedar.DecisionDeny})

	_, err := h.ListSessions(ctxAs("viewer"), connect.NewRequest(&adminv1.ListSessionsRequest{}))
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

// ─── Inspect projection ────────────────────────────────────────────────────

func TestInspectProjectsConfig(t *testing.T) {
	h := NewHandler(config.MCP{
		Upstreams: config.MCPUpstreams{
			AdminURL: "http://admin:8090", DataURL: "http://data:8080", IAMURL: "http://iam:8085",
		},
		Stdio: config.MCPStdio{Enabled: true, Profile: "readonly"},
		HTTP: config.MCPHTTP{
			Enabled: true, Addr: ":8095", Profile: "admin", SessionTimeout: 90 * time.Second,
		},
	}, allowAll())

	res, err := h.Inspect(ctxAs("admin"), connect.NewRequest(&adminv1.MCPInspectRequest{}))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	msg := res.Msg
	if msg.Upstreams.AdminUrl != "http://admin:8090" ||
		msg.Upstreams.DataUrl != "http://data:8080" ||
		msg.Upstreams.IamUrl != "http://iam:8085" {
		t.Errorf("upstreams = %+v", msg.Upstreams)
	}
	if !msg.Transports.Stdio.Enabled || msg.Transports.Stdio.Profile != "readonly" {
		t.Errorf("stdio transport = %+v", msg.Transports.Stdio)
	}
	// SessionTimeout is a Duration in config and seconds on the wire.
	if msg.Transports.Http.SessionTimeoutSeconds != 90 {
		t.Errorf("session timeout = %d, want 90", msg.Transports.Http.SessionTimeoutSeconds)
	}
	if len(msg.ToolCatalog) != len(mcppkg.DefaultCatalog) {
		t.Errorf("catalog size = %d, want %d", len(msg.ToolCatalog), len(mcppkg.DefaultCatalog))
	}
}

func TestToolCatalogProjectsEveryField(t *testing.T) {
	h := NewHandler(config.MCP{}, allowAll())

	got := h.toolCatalog()
	if len(got) != len(mcppkg.DefaultCatalog) {
		t.Fatalf("catalog size = %d, want %d", len(got), len(mcppkg.DefaultCatalog))
	}
	byName := map[string]*adminv1.MCPTool{}
	for _, tool := range got {
		byName[tool.Name] = tool
	}
	for _, src := range mcppkg.DefaultCatalog {
		tool, ok := byName[src.Name]
		if !ok {
			t.Fatalf("tool %q missing from the projection", src.Name)
		}
		if tool.Audience != src.Audience || tool.CapabilityOp != src.CapabilityOp ||
			tool.Mutates != src.Mutates || tool.Description != src.Description {
			t.Errorf("tool %q projected as %+v, want %+v", src.Name, tool, src)
		}
	}
}

// ─── alwaysDeny ────────────────────────────────────────────────────────────

func TestAlwaysDenyDefaultsToBuiltIn(t *testing.T) {
	h := NewHandler(config.MCP{}, allowAll())

	got := h.alwaysDeny()
	if len(got) != len(mcppkg.DefaultAlwaysDeny) {
		t.Fatalf("got %v, want the built-in default %v", got, mcppkg.DefaultAlwaysDeny)
	}
}

// An explicitly empty list means "deny nothing" and must NOT fall back to the
// built-in blacklist — that distinction is why the field is a nil-able slice.
func TestAlwaysDenyEmptySliceMeansDenyNothing(t *testing.T) {
	h := NewHandler(config.MCP{AlwaysDeny: []string{}}, allowAll())

	if got := h.alwaysDeny(); len(got) != 0 {
		t.Fatalf("an explicit empty list must deny nothing, got %v", got)
	}
}

func TestAlwaysDenyOverride(t *testing.T) {
	h := NewHandler(config.MCP{AlwaysDeny: []string{"danger_*"}}, allowAll())

	got := h.alwaysDeny()
	if len(got) != 1 || got[0] != "danger_*" {
		t.Fatalf("got %v, want [danger_*]", got)
	}
}

// The returned slice must be a copy: a caller mutating it must not corrupt the
// handler's config for every later request.
func TestAlwaysDenyReturnsACopy(t *testing.T) {
	h := NewHandler(config.MCP{AlwaysDeny: []string{"a", "b"}}, allowAll())

	got := h.alwaysDeny()
	got[0] = "mutated"
	if again := h.alwaysDeny(); again[0] != "a" {
		t.Errorf("config was mutated through the returned slice: %v", again)
	}
}

// ─── profiles ──────────────────────────────────────────────────────────────

func TestProfilesReportBuiltInSource(t *testing.T) {
	h := NewHandler(config.MCP{}, allowAll())

	got := h.profiles()
	if len(got) != len(mcppkg.DefaultProfiles) {
		t.Fatalf("got %d profiles, want %d", len(got), len(mcppkg.DefaultProfiles))
	}
	for _, p := range got {
		if p.Source != "built_in" {
			t.Errorf("profile %q source = %q, want built_in", p.Name, p.Source)
		}
	}
}

func TestProfilesAreSortedByName(t *testing.T) {
	h := NewHandler(config.MCP{Profiles: map[string]config.MCPProfile{
		"zzz": {Tools: []string{"*"}},
		"aaa": {Tools: []string{"*"}},
	}}, allowAll())

	got := h.profiles()
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatalf("profiles out of order at %d: %q > %q", i, got[i-1].Name, got[i].Name)
		}
	}
}

// Source labelling is what lets the UI flag operator drift, so the two
// override shapes must be distinguishable.
func TestProfilesLabelOverrideVsUserOnly(t *testing.T) {
	// Pick a real built-in name so the override case is genuine.
	var builtIn string
	for name := range mcppkg.DefaultProfiles {
		builtIn = name
		break
	}
	if builtIn == "" {
		t.Skip("no built-in profiles to override")
	}

	h := NewHandler(config.MCP{Profiles: map[string]config.MCPProfile{
		builtIn:            {Tools: []string{"*"}},
		"operator_special": {Tools: []string{"*"}},
	}}, allowAll())

	sources := map[string]string{}
	for _, p := range h.profiles() {
		sources[p.Name] = p.Source
	}
	if sources[builtIn] != "user_override" {
		t.Errorf("%q source = %q, want user_override", builtIn, sources[builtIn])
	}
	if sources["operator_special"] != "user_only" {
		t.Errorf("operator_special source = %q, want user_only", sources["operator_special"])
	}
}

func TestProfilesExposeRawPatternsAndDeny(t *testing.T) {
	h := NewHandler(config.MCP{Profiles: map[string]config.MCPProfile{
		"p": {Tools: []string{"paladin_*"}, Deny: []string{"paladin_delete_*"}},
	}}, allowAll())

	var p *adminv1.MCPProfile
	for _, cand := range h.profiles() {
		if cand.Name == "p" {
			p = cand
		}
	}
	if p == nil {
		t.Fatal("profile p missing")
	}
	if len(p.RawPatterns) != 1 || p.RawPatterns[0] != "paladin_*" {
		t.Errorf("RawPatterns = %v", p.RawPatterns)
	}
	if len(p.Deny) != 1 || p.Deny[0] != "paladin_delete_*" {
		t.Errorf("Deny = %v", p.Deny)
	}
	// Tools is the *expanded* list, so a denied tool must not appear in it.
	for _, name := range p.Tools {
		if matchAny(name, []string{"paladin_delete_*"}) {
			t.Errorf("denied tool %q leaked into the expanded list", name)
		}
	}
}

// ─── expandProfile / matchAny ──────────────────────────────────────────────

func TestExpandProfileWildcardIncludesEverythingNotDenied(t *testing.T) {
	got := expandProfile([]string{"*"}, nil, nil)
	if len(got) != len(mcppkg.DefaultCatalog) {
		t.Fatalf("got %d tools, want the whole catalog (%d)", len(got), len(mcppkg.DefaultCatalog))
	}
}

func TestExpandProfileEmptyAllowMatchesNothing(t *testing.T) {
	if got := expandProfile(nil, nil, nil); len(got) != 0 {
		t.Fatalf("no allow patterns must expand to nothing, got %v", got)
	}
}

// Deny beats allow — both the profile's own list and the global blacklist.
func TestExpandProfileDenyWins(t *testing.T) {
	if len(mcppkg.DefaultCatalog) == 0 {
		t.Skip("empty catalog")
	}
	victim := mcppkg.DefaultCatalog[0].Name

	t.Run("profile deny", func(t *testing.T) {
		got := expandProfile([]string{"*"}, []string{victim}, nil)
		for _, n := range got {
			if n == victim {
				t.Fatalf("%q should have been denied", victim)
			}
		}
		if len(got) != len(mcppkg.DefaultCatalog)-1 {
			t.Errorf("got %d tools, want %d", len(got), len(mcppkg.DefaultCatalog)-1)
		}
	})

	t.Run("always deny", func(t *testing.T) {
		got := expandProfile([]string{"*"}, nil, []string{victim})
		for _, n := range got {
			if n == victim {
				t.Fatalf("%q should have been denied globally", victim)
			}
		}
	})
}

func TestExpandProfilePreservesCatalogOrder(t *testing.T) {
	got := expandProfile([]string{"*"}, nil, nil)
	for i, tool := range mcppkg.DefaultCatalog {
		if got[i] != tool.Name {
			t.Fatalf("position %d = %q, want %q (catalog order)", i, got[i], tool.Name)
		}
	}
}

func TestMatchAny(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		want     bool
	}{
		{"paladin_list_buckets", []string{"*"}, true},
		{"paladin_list_buckets", []string{"paladin_list_*"}, true},
		{"paladin_list_buckets", []string{"paladin_list_buckets"}, true},
		{"paladin_list_buckets", []string{"paladin_delete_*"}, false},
		{"paladin_list_buckets", nil, false},
		{"paladin_list_buckets", []string{"nope", "paladin_list_*"}, true},
	}
	for _, tc := range cases {
		if got := matchAny(tc.name, tc.patterns); got != tc.want {
			t.Errorf("matchAny(%q, %v) = %v, want %v", tc.name, tc.patterns, got, tc.want)
		}
	}
}

// ─── sessionTargets fan-out ────────────────────────────────────────────────

type stubResolver struct {
	addrs []string
	err   error
	got   string
}

func (r *stubResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	r.got = host
	return r.addrs, r.err
}

// The registry is process-local, so a single LB'd query would only ever see one
// pod. Every resolved address must become its own target, port preserved.
func TestSessionTargetsFansOutAcrossReplicas(t *testing.T) {
	res := &stubResolver{addrs: []string{"10.1.1.1", "10.1.1.2", "10.1.1.3"}}
	h := &Handler{sessionsURL: "http://mcp-headless:8095/sessions", resolver: res}

	got := h.sessionTargets(context.Background())
	want := []string{
		"http://10.1.1.1:8095/sessions",
		"http://10.1.1.2:8095/sessions",
		"http://10.1.1.3:8095/sessions",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if res.got != "mcp-headless" {
		t.Errorf("looked up %q, want the bare hostname", res.got)
	}
}

func TestSessionTargetsFanOutWithoutPort(t *testing.T) {
	res := &stubResolver{addrs: []string{"10.1.1.1", "10.1.1.2"}}
	h := &Handler{sessionsURL: "http://mcp-headless/sessions", resolver: res}

	got := h.sessionTargets(context.Background())
	want := []string{"http://10.1.1.1/sessions", "http://10.1.1.2/sessions"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A single-address host (ClusterIP) collapses to the original URL — the
// pre-fan-out behaviour.
func TestSessionTargetsSingleAddressCollapses(t *testing.T) {
	h := &Handler{
		sessionsURL: "http://mcp:8095/sessions",
		resolver:    &stubResolver{addrs: []string{"10.1.1.1"}},
	}

	got := h.sessionTargets(context.Background())
	if len(got) != 1 || got[0] != "http://mcp:8095/sessions" {
		t.Errorf("got %v, want the URL verbatim", got)
	}
}

// An unresolvable host must degrade to the verbatim URL rather than yielding
// no targets at all — that would silently blank the admin session view.
func TestSessionTargetsResolveErrorCollapses(t *testing.T) {
	h := &Handler{
		sessionsURL: "http://mcp:8095/sessions",
		resolver:    &stubResolver{err: errors.New("NXDOMAIN")},
	}

	got := h.sessionTargets(context.Background())
	if len(got) != 1 || got[0] != "http://mcp:8095/sessions" {
		t.Errorf("got %v, want the URL verbatim", got)
	}
}

// A handler built without the seam (older direct construction) must still work.
func TestSessionTargetsNilResolverFallsBack(t *testing.T) {
	h := &Handler{sessionsURL: "http://10.0.0.5:8095/sessions"} // literal IP, no lookup

	got := h.sessionTargets(context.Background())
	if len(got) != 1 {
		t.Errorf("got %v, want a single target", got)
	}
}

// ─── ListSessions ──────────────────────────────────────────────────────────

// An unconfigured proxy has nothing to report; that is an empty list, not an
// error — the graceful-degrade contract.
func TestListSessionsEmptyWhenUnconfigured(t *testing.T) {
	h := NewHandler(config.MCP{}, allowAll())

	res, err := h.ListSessions(ctxAs("admin"), connect.NewRequest(&adminv1.ListSessionsRequest{}))
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(res.Msg.Sessions) != 0 {
		t.Errorf("want no sessions, got %d", len(res.Msg.Sessions))
	}
}

func TestListSessionsProxiesAndSorts(t *testing.T) {
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-jwt" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode([]mcppkg.SessionInfo{
			{ID: "s2", AgentSubject: "agent-b", StartedAt: t0.Add(time.Minute), LastSeen: t0, RequestCount: 5},
			{ID: "s1", AgentSubject: "agent-a", StartedAt: t0, LastSeen: t0, ToolCallCount: 3},
		})
	}))
	defer srv.Close()

	h := NewHandler(config.MCP{}, allowAll())
	h.sessionsURL = srv.URL
	h.httpClient = srv.Client()

	req := connect.NewRequest(&adminv1.ListSessionsRequest{})
	req.Header().Set("Authorization", "Bearer admin-jwt")

	res, err := h.ListSessions(ctxAs("admin"), req)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(res.Msg.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(res.Msg.Sessions))
	}
	// Sorted by StartedAt, so the older s1 comes first regardless of the
	// order the replica returned them in.
	if res.Msg.Sessions[0].Id != "s1" || res.Msg.Sessions[1].Id != "s2" {
		t.Errorf("order = %s,%s, want s1,s2", res.Msg.Sessions[0].Id, res.Msg.Sessions[1].Id)
	}
	if res.Msg.Sessions[0].ToolCallCount != 3 || res.Msg.Sessions[1].RequestCount != 5 {
		t.Errorf("counters not projected: %+v", res.Msg.Sessions)
	}
	if res.Msg.Sessions[0].StartedAt == nil || !res.Msg.Sessions[0].StartedAt.AsTime().Equal(t0) {
		t.Errorf("StartedAt not projected: %v", res.Msg.Sessions[0].StartedAt)
	}
}

// An unreachable or erroring replica contributes nothing rather than failing
// the whole RPC — one bad pod must not blank the admin view.
func TestListSessionsDegradesOnReplicaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := NewHandler(config.MCP{}, allowAll())
	h.sessionsURL = srv.URL
	h.httpClient = srv.Client()

	res, err := h.ListSessions(ctxAs("admin"), connect.NewRequest(&adminv1.ListSessionsRequest{}))
	if err != nil {
		t.Fatalf("a failing replica must not fail the RPC, got %v", err)
	}
	if len(res.Msg.Sessions) != 0 {
		t.Errorf("want no sessions, got %d", len(res.Msg.Sessions))
	}
}

// Ids are minted per pod, so a collision across replicas is unexpected; if one
// happens the freshest last_seen must win rather than an arbitrary replica.
func TestListSessionsDeduplicatesByFreshestLastSeen(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]mcppkg.SessionInfo{
			{ID: "dup", StartedAt: t0, LastSeen: t0, RequestCount: 1},
			{ID: "dup", StartedAt: t0, LastSeen: t0.Add(time.Minute), RequestCount: 99},
		})
	}))
	defer srv.Close()

	h := NewHandler(config.MCP{}, allowAll())
	h.sessionsURL = srv.URL
	h.httpClient = srv.Client()

	res, err := h.ListSessions(ctxAs("admin"), connect.NewRequest(&adminv1.ListSessionsRequest{}))
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(res.Msg.Sessions) != 1 {
		t.Fatalf("want the duplicate collapsed, got %d", len(res.Msg.Sessions))
	}
	if res.Msg.Sessions[0].RequestCount != 99 {
		t.Errorf("kept the stale entry: %+v", res.Msg.Sessions[0])
	}
}

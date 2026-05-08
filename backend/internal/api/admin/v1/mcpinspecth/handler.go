// Package mcpinspecth implements admin/v1.MCPInspectService — read-
// only operator visibility into the MCP bridge configuration.
//
// The MCP bridge sits between agentic runtimes and the PALADIN Connect
// API. What tools agents see, which are denied, and which upstreams
// the bridge dispatches to is config + built-in defaults merged at
// boot. This handler flattens that resolution so an admin UI can
// render the *effective* surface without re-implementing the merge
// logic in JS.
//
// Authorisation: every RPC requires platform.admin via Cedar — same
// gate as the other admin/v1 services.
package mcpinspecth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
	mcppkg "github.com/oleg-tkachuk/paladin/internal/mcp"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// Handler builds an MCPInspectService implementation from the live
// config + bridge defaults. The cfg snapshot is taken at construction
// — until hot-reload lands the handler returns the same view for
// the lifetime of the process.
type Handler struct {
	cfg    config.MCP
	policy cedar.Authorizer
}

func NewHandler(cfg config.MCP, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("mcpinspecth: policy authorizer is required")
	}
	return &Handler{cfg: cfg, policy: policy}
}

// authorize gates an RPC against Cedar with the tenant-resource
// shape every other admin handler uses. Action is "read" — the
// inspect surface is read-only.
func (h *Handler) authorize(ctx context.Context) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{
			Subject:    p.Subject,
			TenantID:   p.TenantID,
			TenantSlug: p.TenantSlug,
			Roles:      p.Roles,
			Scopes:     apiutil.ScopeStrings(p.Scopes),
		},
		"read",
		&cedar.Resource{TenantID: p.TenantID, TenantSlug: p.TenantSlug},
		cedar.RequestContext{},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("mcp inspect denied"))
	}
	return nil
}

// Inspect returns the merged effective MCP configuration: profiles
// (built-ins overlaid by operator overrides), the always-deny list,
// the tool catalog, upstreams, and transport settings.
func (h *Handler) Inspect(ctx context.Context, _ *connect.Request[adminv1.MCPInspectRequest]) (*connect.Response[adminv1.MCPInspectResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}

	resp := &adminv1.MCPInspectResponse{
		Profiles:    h.profiles(),
		AlwaysDeny:  h.alwaysDeny(),
		ToolCatalog: h.toolCatalog(),
		Upstreams: &adminv1.MCPUpstreams{
			AdminUrl: h.cfg.Upstreams.AdminURL,
			DataUrl:  h.cfg.Upstreams.DataURL,
			IamUrl:   h.cfg.Upstreams.IAMURL,
		},
		Transports: &adminv1.MCPTransports{
			Stdio: &adminv1.MCPTransportStdio{
				Enabled: h.cfg.Stdio.Enabled,
				Profile: h.cfg.Stdio.Profile,
			},
			Http: &adminv1.MCPTransportHTTP{
				Enabled:               h.cfg.HTTP.Enabled,
				Addr:                  h.cfg.HTTP.Addr,
				Profile:               h.cfg.HTTP.Profile,
				SessionTimeoutSeconds: int64(h.cfg.HTTP.SessionTimeout / time.Second),
			},
		},
	}
	return connect.NewResponse(resp), nil
}

// profiles merges built-in DefaultProfiles with operator overrides
// from cfg.MCP.Profiles. Each profile reports its source layer
// (built_in / user_only / user_override) so the UI can flag operator
// drift from defaults.
func (h *Handler) profiles() []*adminv1.MCPProfile {
	type entry struct {
		patterns []string
		deny     []string
		source   string
	}
	merged := map[string]entry{}

	for name, patterns := range mcppkg.DefaultProfiles {
		merged[name] = entry{
			patterns: append([]string(nil), patterns...),
			source:   "built_in",
		}
	}
	for name, p := range h.cfg.Profiles {
		src := "user_only"
		if _, ok := merged[name]; ok {
			src = "user_override"
		}
		merged[name] = entry{
			patterns: append([]string(nil), p.Tools...),
			deny:     append([]string(nil), p.Deny...),
			source:   src,
		}
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]*adminv1.MCPProfile, 0, len(merged))
	for _, name := range names {
		e := merged[name]
		out = append(out, &adminv1.MCPProfile{
			Name:        name,
			RawPatterns: e.patterns,
			Deny:        e.deny,
			Source:      e.source,
			// Tools is the *expanded* allow-list — every tool from
			// DefaultCatalog that the profile's patterns match,
			// minus deny matches. Lets the UI render "this is what
			// agents actually see" without re-implementing the
			// glob-expansion in JS.
			Tools: expandProfile(e.patterns, e.deny, h.alwaysDeny()),
		})
	}
	return out
}

// alwaysDeny returns the effective global blacklist: cfg.MCP.AlwaysDeny
// when non-nil (empty slice = explicit "deny nothing"); built-in
// DefaultAlwaysDeny otherwise.
func (h *Handler) alwaysDeny() []string {
	if h.cfg.AlwaysDeny != nil {
		out := make([]string, len(h.cfg.AlwaysDeny))
		copy(out, h.cfg.AlwaysDeny)
		return out
	}
	out := make([]string, len(mcppkg.DefaultAlwaysDeny))
	copy(out, mcppkg.DefaultAlwaysDeny)
	return out
}

// toolCatalog projects mcp.DefaultCatalog into the proto shape.
func (h *Handler) toolCatalog() []*adminv1.MCPTool {
	out := make([]*adminv1.MCPTool, 0, len(mcppkg.DefaultCatalog))
	for _, t := range mcppkg.DefaultCatalog {
		out = append(out, &adminv1.MCPTool{
			Name:         t.Name,
			Audience:     t.Audience,
			Description:  t.Description,
			CapabilityOp: t.CapabilityOp,
			Mutates:      t.Mutates,
		})
	}
	return out
}

// expandProfile resolves an allow-pattern list against DefaultCatalog,
// applying the profile's deny + the always-deny denylist. Mirrors
// mcp.ToolFilter.Allow but produces a full enumeration the UI can
// render without re-running the matcher.
func expandProfile(allowPatterns, profileDeny, alwaysDeny []string) []string {
	denySet := append([]string(nil), profileDeny...)
	denySet = append(denySet, alwaysDeny...)

	out := make([]string, 0)
	for _, t := range mcppkg.DefaultCatalog {
		if matchAny(t.Name, denySet) {
			continue
		}
		if matchAny(t.Name, allowPatterns) {
			out = append(out, t.Name)
		}
	}
	return out
}

// matchAny returns true when name matches at least one pattern in
// patterns. Pattern grammar mirrors mcp.matchPattern: literal "*"
// matches anything; trailing-* is a prefix wildcard; otherwise
// exact match.
func matchAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if mcppkg.PatternMatch(p, name) {
			return true
		}
	}
	return false
}

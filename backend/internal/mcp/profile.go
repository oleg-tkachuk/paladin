package mcp

import (
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/config"
)

// Profile gating for the MCP tool catalog.
//
// Layered model:
//
//   1. Profile — a named allow-list selected per transport (stdio / http).
//      Built-ins: read_only, agent_safe, admin. Operators can override or
//      extend via cfg.MCP.Profiles.
//   2. AlwaysDeny — a global denylist applied AFTER profile expansion.
//      Encodes the "antithesis to capability model" set: tools a
//      well-behaved agent must never see (issue its own capability,
//      mint M2M tokens, manage users, rewrite policies). Wins over
//      every profile, including admin.
//   3. Bridge per-RPC auth — the underlying Connect call still goes
//      through the plane's interceptors (Cedar + capability caveats),
//      so even an over-permissive catalog gets blocked at the actual
//      RPC. Profiles are about catalog hygiene, not authorisation.
//
// Pattern syntax: trailing `*` is a prefix wildcard ("paladin_list_*"
// matches every tool whose name starts with "paladin_list_"). The literal
// "*" matches every tool. No regexp, no infix matching — keeps
// operator-supplied YAML predictable.

// DefaultProfiles is the baked-in profile set used when cfg.MCP.Profiles
// is empty or doesn't define a requested name. Each value is the full
// list of tool patterns the profile allows.
//
// Add a tool? Update DefaultCatalog (the ground-truth set of registered
// tools) AND, if it should be visible by default, add it to the
// appropriate profile here.
var DefaultProfiles = map[string][]string{
	"read_only": {
		"paladin_list_*",
		"paladin_get_*",
		"paladin_query_*",
		"paladin_audit_recent",
		"paladin_validate_policy",
		"paladin_simulate_authz",
	},
	"agent_safe": {
		"paladin_list_*",
		"paladin_get_*",
		"paladin_query_*",
		"paladin_audit_recent",
		"paladin_validate_policy",
		"paladin_simulate_authz",
		"paladin_presign_download",
		"paladin_upload_object",
		"paladin_complete_object",
		"paladin_set_object_tags",
	},
	"admin": {
		"*",
	},
}

// DefaultAlwaysDeny is the baked-in global blacklist. Applied unless
// cfg.MCP.AlwaysDeny is non-empty (in which case the operator-supplied
// list replaces it — empty list explicitly = no deny).
//
// Rationale per entry:
//
//   - paladin_capability_*  — agent issuing/delegating/revoking its own
//     capability is a trivial bypass of caveats.
//   - paladin_apitoken_*    — same logic for M2M hashed-bearer tokens.
//   - paladin_create_user   — privileged identity creation; human-only.
//   - paladin_grant_user_*  — role escalation surface.
//   - paladin_revoke_*      — agent revoking auth primitives is destructive.
//   - paladin_set_policy /
//     paladin_delete_policy — agent rewriting Cedar = privilege re-grant.
//   - paladin_login         — credentials never via agent context.
//   - paladin_create_tenant /
//     paladin_delete_tenant — tenant lifecycle is human-only.
var DefaultAlwaysDeny = []string{
	"paladin_capability_*",
	"paladin_apitoken_*",
	"paladin_create_user",
	"paladin_grant_user_*",
	"paladin_revoke_*",
	"paladin_set_policy",
	"paladin_delete_policy",
	"paladin_login",
	"paladin_create_tenant",
	"paladin_delete_tenant",
}

// ToolFilter resolves a tool name to allow / deny based on a profile +
// the global always-deny list. Built once at server construction and
// queried per AddTool call.
type ToolFilter struct {
	allow []string
	deny  []string
}

// NewToolFilter builds a filter for the given profile name. Resolution:
//
//  1. Look up profile in cfg.MCP.Profiles; if missing, fall back to
//     DefaultProfiles[name]; if still missing, allow nothing (closed
//     by default — explicit profile name unknown is a config bug).
//  2. AlwaysDeny: prefer cfg.MCP.AlwaysDeny when non-nil (empty slice
//     = deny nothing); otherwise DefaultAlwaysDeny.
//  3. Per-profile Deny entries are merged into the deny list.
func NewToolFilter(cfg config.MCP, profileName string) *ToolFilter {
	if profileName == "" {
		profileName = "read_only"
	}

	var allow, deny []string
	if p, ok := cfg.Profiles[profileName]; ok {
		allow = append(allow, p.Tools...)
		deny = append(deny, p.Deny...)
	} else if patterns, ok := DefaultProfiles[profileName]; ok {
		allow = append(allow, patterns...)
	}
	// else: unknown profile → allow stays empty → filter denies everything.

	if cfg.AlwaysDeny != nil {
		deny = append(deny, cfg.AlwaysDeny...)
	} else {
		deny = append(deny, DefaultAlwaysDeny...)
	}

	return &ToolFilter{allow: allow, deny: deny}
}

// Allow returns true when the tool should be registered in the catalog.
// Deny patterns win over Allow patterns — a tool matched by both is
// denied.
func (f *ToolFilter) Allow(toolName string) bool {
	if f == nil {
		return true
	}
	for _, p := range f.deny {
		if matchPattern(p, toolName) {
			return false
		}
	}
	for _, p := range f.allow {
		if matchPattern(p, toolName) {
			return true
		}
	}
	return false
}

// matchPattern: literal "*" matches anything; "prefix*" matches any
// name with that prefix; otherwise exact-match.
func matchPattern(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}

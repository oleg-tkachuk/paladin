package mcp

import (
	"strings"

	"github.com/oleg-tkachuk/paladin-private/internal/config"
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

// ToolMeta is the static metadata for one MCP tool. Mirrored by every
// addTool call in bridge.go; kept here so the operator-visibility
// surface (admin/v1.MCPInspectService) can render the catalog without
// importing the heavy bridge package.
//
// Audience pins which Paladin plane the underlying Connect RPC lands on:
// "admin" / "data" / "iam". CapabilityOp is the capability.Op the
// caller's capability must include for the call to succeed (empty
// when the tool is JWT-only). Mutates is true when the tool issues
// a state-changing RPC — UI flags these for visual emphasis.
type ToolMeta struct {
	Name         string
	Audience     string
	Description  string
	CapabilityOp string
	Mutates      bool
}

// DefaultCatalog is the ground-truth list of tools registered by
// internal/mcp/bridge.go. Order mirrors the addTool calls so a diff
// against bridge.go is direct. Add a tool? Append a row here AND, if
// it should be visible by default, add it to the appropriate
// DefaultProfiles entry below.
var DefaultCatalog = []ToolMeta{
	// ── admin plane: discovery / inspection ──────────────────────
	{Name: "paladin_list_backends", Audience: "admin", Description: "List storage backends registered with the platform."},
	{Name: "paladin_list_buckets", Audience: "admin", Description: "List buckets across all backends."},
	{Name: "paladin_get_bucket", Audience: "admin", Description: "Read a single bucket's metadata + lifecycle."},
	{Name: "paladin_list_tenants", Audience: "admin", Description: "Enumerate tenants the caller can see."},
	{Name: "paladin_get_tenant", Audience: "admin", Description: "Read a tenant's metadata + inherited Cedar policy."},
	{Name: "paladin_list_object_keys", Audience: "admin", Description: "List logical object_keys in a bucket."},
	{Name: "paladin_get_object_key", Audience: "admin", Description: "Read object_key metadata + per-key Cedar policy."},
	{Name: "paladin_create_object_key", Audience: "admin", Description: "Create a new logical object_key.", Mutates: true},
	{Name: "paladin_get_quota", Audience: "admin", Description: "Read tenant or bucket-scoped quota."},
	{Name: "paladin_set_quota", Audience: "admin", Description: "Upsert tenant or bucket quota.", Mutates: true},
	{Name: "paladin_validate_policy", Audience: "admin", Description: "Compile + validate a Cedar policy without applying."},
	{Name: "paladin_get_effective_policy", Audience: "admin", Description: "Read the merged tenant + object_key policy text."},
	{Name: "paladin_simulate_authz", Audience: "admin", Description: "Run a Cedar authz check without performing the call."},
	{Name: "paladin_audit_recent", Audience: "admin", Description: "Tail the audit log; supports CEL filter."},
	{Name: "paladin_audit_export", Audience: "admin", Description: "Materialise an audit-log dump for compliance."},
	{Name: "paladin_list_subscriptions", Audience: "admin", Description: "List event subscriptions per tenant."},
	{Name: "paladin_get_subscription", Audience: "admin", Description: "Read a single event subscription."},
	{Name: "paladin_create_subscription", Audience: "admin", Description: "Create an event subscription with HTTP / Kafka / SQS sink.", Mutates: true},
	{Name: "paladin_update_subscription", Audience: "admin", Description: "Replace filter / sink / disabled on an existing subscription.", Mutates: true},
	{Name: "paladin_delete_subscription", Audience: "admin", Description: "Delete an event subscription.", Mutates: true},
	{Name: "paladin_test_subscription", Audience: "admin", Description: "Deliver a synthetic event to a subscription's sink (non-mutating)."},
	{Name: "paladin_set_lifecycle_rules", Audience: "admin", Description: "Update bucket lifecycle (CEL-based expiration).", Mutates: true},
	{Name: "paladin_validate_cel", Audience: "admin", Description: "Compile-check a CEL expression against a Paladin schema (Object | ObjectKey | AuditLogEntry | EventEnvelope)."},
	{Name: "paladin_get_audit_entry", Audience: "admin", Description: "Read a single audit-log entry by id."},
	{Name: "paladin_system_config", Audience: "admin", Description: "Read the platform's effective runtime config (admin profile only)."},
	{Name: "paladin_reset_usage", Audience: "admin", Description: "Reset accumulated usage counters on a quota (limits unchanged).", Mutates: true},

	// ── data plane: object operations ────────────────────────────
	{Name: "paladin_query_objects", Audience: "data", Description: "List objects under an object_key with CEL filter.", CapabilityOp: "list"},
	{Name: "paladin_get_object", Audience: "data", Description: "Read object metadata.", CapabilityOp: "get"},
	{Name: "paladin_lookup_object", Audience: "data", Description: "Resolve an object by its human key within an object_key.", CapabilityOp: "get"},
	{Name: "paladin_count_objects", Audience: "data", Description: "Count objects under an object_key, optionally CEL-filtered.", CapabilityOp: "list"},
	{Name: "paladin_list_versions", Audience: "data", Description: "List versions of one object.", CapabilityOp: "list"},
	{Name: "paladin_get_version", Audience: "data", Description: "Read one version's metadata.", CapabilityOp: "get"},
	{Name: "paladin_restore_version", Audience: "data", Description: "Promote an older version as current.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_get_object_tags", Audience: "data", Description: "Read object tags.", CapabilityOp: "tag"},
	{Name: "paladin_set_object_tags", Audience: "data", Description: "Update object tags.", CapabilityOp: "tag", Mutates: true},
	{Name: "paladin_presign_download", Audience: "data", Description: "Mint a signed GET URL.", CapabilityOp: "presign"},
	{Name: "paladin_upload_object", Audience: "data", Description: "Initiate object upload (presigned PUT).", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_complete_object", Audience: "data", Description: "Finalise upload + commit object metadata.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_delete_object", Audience: "data", Description: "Delete an object (soft by default; permanent on request).", CapabilityOp: "delete", Mutates: true},
	{Name: "paladin_copy_object", Audience: "data", Description: "Server-side copy of an object to a new object_key + key.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_batch_delete", Audience: "data", Description: "Async bulk delete by names or CEL filter; returns an Operation.", CapabilityOp: "delete", Mutates: true},
	{Name: "paladin_batch_copy", Audience: "data", Description: "Async bulk server-side copy into a destination object_key; returns an Operation.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_batch_restore", Audience: "data", Description: "Async bulk restore of soft-deleted objects; returns an Operation.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_list_operations", Audience: "data", Description: "List async batch operations."},
	{Name: "paladin_get_operation", Audience: "data", Description: "Read one operation's status + progress."},
	{Name: "paladin_cancel_operation", Audience: "data", Description: "Request cancellation of a running operation.", Mutates: true},
	{Name: "paladin_update_object", Audience: "data", Description: "Patch object metadata/tags/content-type via field mask (not the body).", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_delete_object_tags", Audience: "data", Description: "Remove specific tag keys from an object.", CapabilityOp: "tag", Mutates: true},
	{Name: "paladin_list_distinct_tags", Audience: "data", Description: "List distinct tag keys/values in use under an object_key.", CapabilityOp: "list"},
	{Name: "paladin_batch_update_tags", Audience: "data", Description: "Async bulk tag merge/replace across an object_key; returns an Operation.", CapabilityOp: "tag", Mutates: true},
	{Name: "paladin_regenerate_upload_url", Audience: "data", Description: "Re-mint a presigned PUT URL for an in-progress (not completed) upload.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_initiate_multipart_upload", Audience: "data", Description: "Begin a multipart upload for a large object.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_presign_part", Audience: "data", Description: "Mint a presigned PUT URL for one part of a multipart upload.", CapabilityOp: "presign"},
	{Name: "paladin_complete_multipart_upload", Audience: "data", Description: "Finalise a multipart upload from the ordered part list.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_abort_multipart_upload", Audience: "data", Description: "Abort an in-progress multipart upload and discard its parts.", CapabilityOp: "put", Mutates: true},
	{Name: "paladin_list_parts", Audience: "data", Description: "List parts uploaded so far for a multipart upload.", CapabilityOp: "list"},

	// ── iam plane: identity surface ──────────────────────────────
	// NB: most iam tools (login, manage-user) are in DefaultAlwaysDeny —
	// they're never exposed to agents. The few kept here support
	// read-only debugging via admin profile.
	{Name: "paladin_create_user", Audience: "iam", Description: "Create a user (denied by default; admin-profile only).", Mutates: true},
	{Name: "paladin_grant_user_scopes", Audience: "iam", Description: "Grant scopes (denied by default; admin-profile only).", Mutates: true},
}

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
		"paladin_validate_cel",
		"paladin_simulate_authz",
	},
	"agent_safe": {
		"paladin_list_*",
		"paladin_get_*",
		"paladin_query_*",
		"paladin_audit_recent",
		"paladin_validate_policy",
		"paladin_validate_cel",
		"paladin_simulate_authz",
		"paladin_presign_download",
		"paladin_upload_object",
		"paladin_complete_object",
		"paladin_set_object_tags",
		"paladin_update_object",
		"paladin_delete_object_tags",
		"paladin_regenerate_upload_url",
		"paladin_initiate_multipart_upload",
		"paladin_presign_part",
		"paladin_complete_multipart_upload",
		"paladin_abort_multipart_upload",
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
		if PatternMatch(p, toolName) {
			return false
		}
	}
	for _, p := range f.allow {
		if PatternMatch(p, toolName) {
			return true
		}
	}
	return false
}

// PatternMatch: literal "*" matches anything; "prefix*" matches any
// name with that prefix; otherwise exact-match.
func PatternMatch(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}

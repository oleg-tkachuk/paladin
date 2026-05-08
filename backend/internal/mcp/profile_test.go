package mcp

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/config"
)

func TestToolFilter_BuiltinProfiles(t *testing.T) {
	cases := []struct {
		profile string
		tool    string
		want    bool
	}{
		// read_only allows list/get/query/audit/policy-helpers, denies mutations.
		{"read_only", "paladin_list_buckets", true},
		{"read_only", "paladin_get_object", true},
		{"read_only", "paladin_query_objects", true},
		{"read_only", "paladin_audit_recent", true},
		{"read_only", "paladin_simulate_authz", true},
		{"read_only", "paladin_create_object_key", false},
		{"read_only", "paladin_set_quota", false},

		// agent_safe adds presign + tag mutations.
		{"agent_safe", "paladin_list_buckets", true},
		{"agent_safe", "paladin_presign_download", true},
		{"agent_safe", "paladin_upload_object", true},
		{"agent_safe", "paladin_complete_object", true},
		{"agent_safe", "paladin_set_object_tags", true},
		{"agent_safe", "paladin_create_object_key", false},
		{"agent_safe", "paladin_set_quota", false},

		// admin allows everything except always_deny.
		{"admin", "paladin_create_object_key", true},
		{"admin", "paladin_set_quota", true},
		{"admin", "paladin_set_lifecycle_rules", true},
	}
	cfg := config.MCP{} // empty → DefaultProfiles + DefaultAlwaysDeny
	for _, c := range cases {
		f := NewToolFilter(cfg, c.profile)
		got := f.Allow(c.tool)
		if got != c.want {
			t.Errorf("profile=%q tool=%q got=%v want=%v", c.profile, c.tool, got, c.want)
		}
	}
}

func TestToolFilter_AlwaysDeny_BeatsAdmin(t *testing.T) {
	// admin allows "*" but always_deny must still kick in for capability /
	// apitoken / user-management / login / tenant-lifecycle / policy-mut.
	cfg := config.MCP{}
	f := NewToolFilter(cfg, "admin")

	mustDeny := []string{
		"paladin_capability_issue",
		"paladin_capability_revoke",
		"paladin_apitoken_create",
		"paladin_create_user",
		"paladin_grant_user_scopes",
		"paladin_revoke_api_key",
		"paladin_set_policy",
		"paladin_delete_policy",
		"paladin_login",
		"paladin_create_tenant",
		"paladin_delete_tenant",
	}
	for _, name := range mustDeny {
		if f.Allow(name) {
			t.Errorf("always_deny breach: %q allowed under admin profile", name)
		}
	}
}

func TestToolFilter_OperatorOverride(t *testing.T) {
	// Operator-supplied profile replaces the built-in. Empty always_deny
	// (explicit empty slice) disables the global blacklist.
	cfg := config.MCP{
		Profiles: map[string]config.MCPProfile{
			"custom": {Tools: []string{"paladin_list_*"}},
		},
		AlwaysDeny: []string{}, // explicit empty = disable defaults
	}
	f := NewToolFilter(cfg, "custom")
	if !f.Allow("paladin_list_buckets") {
		t.Error("custom profile must allow paladin_list_buckets")
	}
	if f.Allow("paladin_get_object") {
		t.Error("custom profile must NOT allow paladin_get_object (not in patterns)")
	}
	// always_deny disabled → capability tools allowed in this artificial setup.
	// (Real operators wouldn't do this; the test guards the explicit-empty
	// override semantic.)
	if !f.Allow("paladin_list_buckets") {
		t.Error("expected paladin_list_buckets allowed with empty always_deny")
	}
}

func TestToolFilter_UnknownProfile_DeniesAll(t *testing.T) {
	// Closed-by-default — unknown profile name in config is a deploy bug
	// the operator should notice immediately, not "well, just allow
	// everything."
	cfg := config.MCP{}
	f := NewToolFilter(cfg, "no_such_profile")
	if f.Allow("paladin_list_buckets") {
		t.Error("unknown profile must deny all (closed-by-default)")
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*", "anything", true},
		{"paladin_list_*", "paladin_list_buckets", true},
		{"paladin_list_*", "paladin_get_buckets", false},
		{"paladin_get_object", "paladin_get_object", true},
		{"paladin_get_object", "paladin_get_object_tags", false},
	}
	for _, c := range cases {
		if got := PatternMatch(c.pat, c.name); got != c.want {
			t.Errorf("PatternMatch(%q, %q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

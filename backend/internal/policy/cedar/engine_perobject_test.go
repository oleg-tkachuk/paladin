package cedar

import (
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/policies"
)

// TestPolicyReadsPerObjectResourceAttr locks down the compile-time analyzer that
// decides whether ListObjects must Cedar-check every returned object (a policy
// reads a per-object resource attribute) or may rely on the single
// collection-scoped check (every policy is constant across the collection). It
// also pins the cedar-go JSON shape the analyzer walks — if that shape changes
// under a dependency bump, the true-cases below fail loudly.
func TestPolicyReadsPerObjectResourceAttr(t *testing.T) {
	cases := []struct {
		name string
		text string // tenant policy text (builtin is always prepended by compile)
		want bool
	}{
		{
			name: "empty tenant policy — builtin only reads principal/constant attrs",
			text: "",
			want: false,
		},
		{
			name: "collection-scoped permit, no conditions",
			text: `permit (principal, action == Action::"GetObject", resource);`,
			want: false,
		},
		{
			name: "constant resource attr (tenant_id) does not trigger",
			text: `permit (principal, action, resource)
			       when { resource has tenant_id && resource.tenant_id == "t1" };`,
			want: false,
		},
		{
			name: "constant resource attr (collection) does not trigger",
			text: `permit (principal, action, resource)
			       when { resource.collection == "docs" };`,
			want: false,
		},
		{
			name: "tags membership triggers per-row",
			text: `forbid (principal, action, resource)
			       when { resource.tags.contains("classified") };`,
			want: true,
		},
		{
			name: "tag_values (value predicate) triggers per-row",
			text: `forbid (principal, action, resource)
			       when { resource.tag_values has "classified" && resource.tag_values["classified"] == "true" };`,
			want: true,
		},
		{
			name: "state predicate triggers per-row",
			text: `permit (principal, action, resource)
			       when { resource.state == "AVAILABLE" };`,
			want: true,
		},
		{
			name: "size_bytes predicate triggers per-row",
			text: `forbid (principal, action, resource)
			       when { resource.size_bytes > 1000000 };`,
			want: true,
		},
		{
			name: "has-test on a per-object attr triggers per-row",
			text: `permit (principal, action, resource) when { resource has tags };`,
			want: true,
		},
		{
			name: "per-object attr for an action a listing never asks does not trigger",
			text: `forbid (principal, action == Action::"DeleteObject", resource)
			       when { resource.tags.contains("locked") };`,
			want: false,
		},
		{
			name: "the default policy's upload blocklist does not trigger",
			text: `permit (principal, action in [Action::"PutObject", Action::"PresignPut"], resource)
			       when { !(resource has key) || !(resource.key like "*.exe") };`,
			want: false,
		},
		{
			name: "a scope naming GetObject among others triggers",
			text: `forbid (principal, action in [Action::"PutObject", Action::"GetObject"], resource)
			       when { resource.tags.contains("locked") };`,
			want: true,
		},
		{
			name: "a scope equal to GetObject triggers",
			text: `forbid (principal, action == Action::"GetObject", resource)
			       when { resource.state == "PENDING" };`,
			want: true,
		},
		{
			name: "a placement rule on the object triggers: the listing decides placement per row",
			text: `permit (principal, action == Action::"ReadObjectPlacement", resource)
			       when { resource has key && resource.key like "public/*" };`,
			want: true,
		},
		{
			name: "in a single action triggers when it is GetObject",
			text: `forbid (principal, action in Action::"GetObject", resource)
			       when { resource.size_bytes > 10 };`,
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := compile(tc.text)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got := policyReadsPerObjectResourceAttr(set); got != tc.want {
				t.Fatalf("policyReadsPerObjectResourceAttr = %v, want %v", got, tc.want)
			}
		})
	}
}

// The analysis reads an action scope as exactly the actions it names, which is
// sound only while the schema declares no action groups: a group would let
// `action in Action::"Reads"` take GetObject without naming it.
func TestSchemaDeclaresNoActionGroups(t *testing.T) {
	s := loadSchema(t)
	for uid, a := range s.Actions {
		if a.Entity.Parents.Len() > 0 {
			t.Errorf("%s is a member of %v: mayApplyToAction must learn the groups", uid, a.Entity.Parents)
		}
	}
}

// The policy every tenant gets at creation keeps a listing on the single
// collection-scoped check: its one per-object read is the upload blocklist.
func TestDefaultTenantPolicy_ListsWithoutPerRowEval(t *testing.T) {
	set, err := compile(strings.ReplaceAll(policies.DefaultTenantPolicy, policies.TenantPlaceholder, "acme"))
	if err != nil {
		t.Fatalf("compile default policy: %v", err)
	}
	if policyReadsPerObjectResourceAttr(set) {
		t.Fatal("the default policy puts every listing on per-row evaluation")
	}
}

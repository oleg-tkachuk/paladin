package cedar

import (
	"testing"

	"github.com/google/uuid"
)

// TestTagValueAuthorization proves the Object entity exposes tag VALUES (not just
// keys), so a policy can decide on a tag's value. Three objects differ ONLY by
// the value of their "classified" tag; the same forbid rule must deny exactly
// the one whose value is "true".
func TestTagValueAuthorization(t *testing.T) {
	tid := uuid.New()
	// Unconditional read permit + a forbid keyed on the tag's VALUE. The `has`
	// guards avoid an evaluation error on objects lacking the tag.
	const policy = `
permit (principal, action == Action::"GetObject", resource);
forbid (principal, action == Action::"GetObject", resource)
when {
    resource has tag_values &&
    resource.tag_values has "classified" &&
    resource.tag_values["classified"] == "true"
};`
	p := &Principal{Subject: "u", TenantID: tid, TenantSlug: "t", Roles: []string{"tenant.user"}}
	// Key must be set so buildEntities materialises the per-object Object entity.
	obj := func(tags map[string]string) *Resource {
		return &Resource{TenantID: tid, Collection: "k", Key: "o", Tags: tags}
	}

	cases := []struct {
		name string
		tags map[string]string
		want Decision
	}{
		{"classified=true is forbidden", map[string]string{"classified": "true"}, DecisionDeny},
		{"classified=false is allowed", map[string]string{"classified": "false"}, DecisionAllow},
		{"other value under same key is allowed", map[string]string{"classified": "internal"}, DecisionAllow},
		{"missing classified tag is allowed (has-guard)", map[string]string{"team": "eng"}, DecisionAllow},
		{"no tags at all is allowed", map[string]string{}, DecisionAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(t, policy, "t", p, obj(tc.tags)); got != tc.want {
				t.Fatalf("decision = %v, want %v", got, tc.want)
			}
		})
	}
}

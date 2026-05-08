package adapters

import "testing"

// TestRewriteTenantSlugRefs covers the pure string rewrite that powers
// Repository.Rename. The DB transactional path is exercised by the
// integration suite; this isolates the policy-text mutation so its
// edge cases (substring overlap, repeated refs, empty slugs) surface
// without a Postgres round-trip.
func TestRewriteTenantSlugRefs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		old  string
		new_ string
		want string
	}{
		{
			name: "single ref",
			in:   `permit(principal in Tenant::"acme", action, resource);`,
			old:  "acme",
			new_: "acme-2",
			want: `permit(principal in Tenant::"acme-2", action, resource);`,
		},
		{
			name: "multiple refs",
			in: `permit(principal in Tenant::"acme", action, resource);
permit(principal in Tenant::"acme", action == Action::"Foo", resource);`,
			old:  "acme",
			new_: "x",
			want: `permit(principal in Tenant::"x", action, resource);
permit(principal in Tenant::"x", action == Action::"Foo", resource);`,
		},
		{
			name: "substring not rewritten",
			// A bare 'acme' in a comment / string MUST NOT be rewritten —
			// only the full Tenant::"acme" literal counts.
			in:   `// project: acme. policy: permit(principal in Tenant::"acme", action, resource);`,
			old:  "acme",
			new_: "z",
			want: `// project: acme. policy: permit(principal in Tenant::"z", action, resource);`,
		},
		{
			name: "different slug not touched",
			in:   `permit(principal in Tenant::"other", action, resource);`,
			old:  "acme",
			new_: "z",
			want: `permit(principal in Tenant::"other", action, resource);`,
		},
		{
			name: "empty old slug is a no-op",
			in:   `permit(principal in Tenant::"", action, resource);`,
			old:  "",
			new_: "x",
			want: `permit(principal in Tenant::"", action, resource);`,
		},
		{
			name: "empty policy is a no-op",
			in:   "",
			old:  "acme",
			new_: "x",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteTenantSlugRefs(tc.in, tc.old, tc.new_)
			if got != tc.want {
				t.Errorf("rewrite:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

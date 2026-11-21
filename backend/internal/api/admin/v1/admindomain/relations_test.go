package admindomain

import "testing"

// A refused delete names what holds it in the caller's terms; a table with no
// noun is "other records", never its own name.
func TestRelationNoun(t *testing.T) {
	for _, tc := range []struct {
		relation string
		want     string
		named    bool
	}{
		{RelationUsers, "users", true},
		{RelationTenantDefaultBindings, "tenant default bindings", true},
		{RelationPendingMultipartAborts, "multipart uploads awaiting abort", true},
		{RelationBuckets, "owned buckets", true},
		{"some_new_table", "other records", false},
		{"", "other records", false},
	} {
		if got := RelationNoun(tc.relation); got != tc.want {
			t.Errorf("RelationNoun(%q) = %q, want %q", tc.relation, got, tc.want)
		}
		if got := HasRelationNoun(tc.relation); got != tc.named {
			t.Errorf("HasRelationNoun(%q) = %v, want %v", tc.relation, got, tc.named)
		}
	}
}

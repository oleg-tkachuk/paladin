package cel

import (
	"maps"
	"testing"
)

func TestExtractObjectPushdown(t *testing.T) {
	cases := []struct {
		expr          string
		state, prefix string
		contains      string
	}{
		{`state == 'AVAILABLE'`, "AVAILABLE", "", ""},
		{`key.startsWith('logs/')`, "", "logs/", ""},
		{`key.contains('report')`, "", "", "report"},
		{`state == 'PENDING' && key.contains('q1')`, "PENDING", "", "q1"},
		{`state == 'X' || key.contains('y')`, "", "", ""}, // disjunction → nothing pushed
		{`size_bytes > 10`, "", "", ""},                   // unsupported field
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			pd, err := ExtractObjectPushdown(tc.expr)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if pd.StateEq != tc.state || pd.KeyPrefix != tc.prefix || pd.KeyContains != tc.contains {
				t.Errorf("got state=%q prefix=%q contains=%q; want %q/%q/%q",
					pd.StateEq, pd.KeyPrefix, pd.KeyContains, tc.state, tc.prefix, tc.contains)
			}
		})
	}
}

func TestExtractObjectPushdown_MapsAndContentType(t *testing.T) {
	cases := []struct {
		expr     string
		tags     map[string]string
		metadata map[string]string
		ctEq     string
		ctPrefix string
	}{
		{expr: `tags['env'] == 'prod'`, tags: map[string]string{"env": "prod"}},
		{expr: `'prod' == tags['env']`, tags: map[string]string{"env": "prod"}},
		{expr: `tags.env == 'prod'`, tags: map[string]string{"env": "prod"}},
		{
			expr:     `tags['env'] == 'prod' && metadata['owner'] == 'ops' && tags['tier'] == 'gold'`,
			tags:     map[string]string{"env": "prod", "tier": "gold"},
			metadata: map[string]string{"owner": "ops"},
		},
		// Two values for one key cannot both hold; the first is kept.
		{expr: `tags['a'] == 'x' && tags['a'] == 'y'`, tags: map[string]string{"a": "x"}},
		{expr: `content_type == 'image/png'`, ctEq: "image/png"},
		{expr: `content_type.startsWith('image/')`, ctPrefix: "image/"},
		// Nothing pushed for shapes the query cannot express.
		{expr: `tags['a'] == 'x' || tags['b'] == 'y'`},
		{expr: `tags['a'] != 'x'`},
		{expr: `'a' in tags`},
		{expr: `tags[key] == 'x'`},
		{expr: `metadata['a'].startsWith('x')`},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			pd, err := ExtractObjectPushdown(tc.expr)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if !maps.Equal(pd.TagsEq, tc.tags) {
				t.Errorf("tags = %v, want %v", pd.TagsEq, tc.tags)
			}
			if !maps.Equal(pd.MetadataEq, tc.metadata) {
				t.Errorf("metadata = %v, want %v", pd.MetadataEq, tc.metadata)
			}
			if pd.ContentTypeEq != tc.ctEq || pd.ContentTypePrefix != tc.ctPrefix {
				t.Errorf("content_type eq=%q prefix=%q, want %q/%q",
					pd.ContentTypeEq, pd.ContentTypePrefix, tc.ctEq, tc.ctPrefix)
			}
		})
	}
}

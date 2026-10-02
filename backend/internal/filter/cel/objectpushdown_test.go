package cel

import (
	"maps"
	"strings"
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

func TestExtractObjectBranches(t *testing.T) {
	type br = ObjectPushdown
	tags := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		expr string
		want []br // nil = unconstrained
	}{
		{`state == 'AVAILABLE'`, []br{{StateEq: "AVAILABLE"}}},
		{`tags['env'] == 'prod' || tags['env'] == 'stage'`,
			[]br{{TagsEq: tags("env", "prod")}, {TagsEq: tags("env", "stage")}}},
		{`content_type in ['image/png', 'image/jpeg']`,
			[]br{{ContentTypeEq: "image/png"}, {ContentTypeEq: "image/jpeg"}}},
		{`tags['team'] in ['a', 'b']`, []br{{TagsEq: tags("team", "a")}, {TagsEq: tags("team", "b")}}},
		// A conjunct distributes over the disjunction.
		{`state == 'AVAILABLE' && (key.contains('q1') || tags['env'] == 'prod')`,
			[]br{{StateEq: "AVAILABLE", KeyContains: "q1"}, {StateEq: "AVAILABLE", TagsEq: tags("env", "prod")}}},
		// Unrecognised conjuncts only widen their branch.
		{`(size_bytes > 10 && tags['a'] == 'x') || key.startsWith('logs/')`,
			[]br{{TagsEq: tags("a", "x")}, {KeyPrefix: "logs/"}}},
		// One unconstrained disjunct makes the whole OR unconstrained.
		{`tags['a'] == 'x' || size_bytes > 10`, nil},
		{`!(state == 'DELETED') || key.contains('x')`, nil},
		{`state in []`, nil},
		{`size_bytes in [1, 2]`, nil},
		// Past the branch cap: a scan beats nine queries.
		{`content_type in ['a','b','c'] && tags['t'] in ['1','2','3']`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := ExtractObjectBranches(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if len(got) != 1 || !got[0].empty() {
					t.Fatalf("got %+v, want unconstrained", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d branches %+v, want %+v", len(got), got, tc.want)
			}
			for i := range got {
				g, w := got[i], tc.want[i]
				if g.StateEq != w.StateEq || g.KeyPrefix != w.KeyPrefix || g.KeyContains != w.KeyContains ||
					g.ContentTypeEq != w.ContentTypeEq || !maps.Equal(g.TagsEq, w.TagsEq) || !maps.Equal(g.MetadataEq, w.MetadataEq) {
					t.Errorf("branch %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

// Every object the filter accepts is in some branch: the pushdown contract,
// checked by brute force over a small universe of objects.
func TestExtractObjectBranchesNeverDropsAMatch(t *testing.T) {
	exprs := []string{
		`tags['env'] == 'prod' || content_type == 'image/png'`,
		`state == 'AVAILABLE' && (key.contains('a') || tags['env'] in ['prod', 'dev'])`,
		`(key.startsWith('x/') || key.startsWith('y/')) && content_type.startsWith('image/')`,
	}
	var rows []ObjectRow
	for _, st := range []string{"AVAILABLE", "PENDING"} {
		for _, k := range []string{"x/a", "y/b", "z/c"} {
			for _, ct := range []string{"image/png", "text/plain"} {
				for _, env := range []string{"prod", "dev", ""} {
					tg := map[string]string{}
					if env != "" {
						tg["env"] = env
					}
					rows = append(rows, ObjectRow{Key: k, State: st, ContentType: ct, Tags: tg})
				}
			}
		}
	}
	ev := NewEvaluator()
	for _, expr := range exprs {
		prog, err := ev.Compile(ObjectSchema, expr)
		if err != nil {
			t.Fatal(err)
		}
		branches, _ := ExtractObjectBranches(expr)
		for _, row := range rows {
			ok, err := Match(prog, ObjectVars(row))
			if err != nil || !ok {
				continue
			}
			covered := false
			for _, b := range branches {
				if branchAdmits(b, row) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("%s: %+v matches but no branch %+v admits it", expr, row, branches)
			}
		}
	}
}

// branchAdmits is what the SQL for one branch accepts.
func branchAdmits(b ObjectPushdown, r ObjectRow) bool {
	if b.StateEq != "" && r.State != b.StateEq {
		return false
	}
	if b.KeyPrefix != "" && !strings.HasPrefix(r.Key, b.KeyPrefix) {
		return false
	}
	if b.KeyContains != "" && !strings.Contains(r.Key, b.KeyContains) {
		return false
	}
	if b.ContentTypeEq != "" && r.ContentType != b.ContentTypeEq {
		return false
	}
	if b.ContentTypePrefix != "" && !strings.HasPrefix(r.ContentType, b.ContentTypePrefix) {
		return false
	}
	for k, v := range b.TagsEq {
		if r.Tags[k] != v {
			return false
		}
	}
	for k, v := range b.MetadataEq {
		if r.Metadata[k] != v {
			return false
		}
	}
	return true
}

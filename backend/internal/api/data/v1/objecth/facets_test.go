package objecth

import (
	"context"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

func TestCategoryOf(t *testing.T) {
	for contentType, want := range map[string]ContentCategory{
		"image/png":                CategoryImage,
		"video/mp4":                CategoryVideo,
		"audio/ogg":                CategoryAudio,
		"text/markdown":            CategoryText,
		"application/pdf":          CategoryPDF,
		"application/json":         CategoryJSON,
		"application/octet-stream": CategoryOther,
		"application/pdfx":         CategoryOther,
		"":                         CategoryOther,
		"imagex/png":               CategoryOther,
	} {
		if got := CategoryOf(contentType); got != want {
			t.Errorf("CategoryOf(%q) = %d, want %d", contentType, got, want)
		}
	}
}

// The facet clauses are CEL the evaluator compiles, and they accept exactly
// the objects CategoryOf and the state name say they should — the listing
// and the counts must agree on what a facet means.
func TestWithFacets_AcceptsWhatTheFacetsName(t *testing.T) {
	ev := cel.NewEvaluator()
	objects := []Object{
		{Key: "a.png", State: statemachine.StateAvailable, ContentType: "image/png", Tags: map[string]string{"env": "prod"}},
		{Key: "b.pdf", State: statemachine.StatePending, ContentType: "application/pdf", Tags: map[string]string{"env": "dev", "a\"b": "c\\d"}},
		{Key: "c.bin", State: statemachine.StateAvailable, ContentType: "application/octet-stream"},
		{Key: "d.txt", State: statemachine.StateAvailable, ContentType: "text/plain"},
	}
	cases := []struct {
		name   string
		filter string
		facets ObjectFacets
		want   []string
	}{
		{"none", "", ObjectFacets{}, []string{"a.png", "b.pdf", "c.bin", "d.txt"}},
		{"one state", "", ObjectFacets{States: []statemachine.State{statemachine.StatePending}}, []string{"b.pdf"}},
		{"two categories", "", ObjectFacets{Categories: []ContentCategory{CategoryImage, CategoryText}}, []string{"a.png", "d.txt"}},
		{"other", "", ObjectFacets{Categories: []ContentCategory{CategoryOther}}, []string{"c.bin"}},
		{"state and category", "", ObjectFacets{
			States:     []statemachine.State{statemachine.StateAvailable},
			Categories: []ContentCategory{CategoryPDF, CategoryImage},
		}, []string{"a.png"}},
		{"with a filter", `key.startsWith("a") || key.startsWith("c")`, ObjectFacets{Categories: []ContentCategory{CategoryOther}}, []string{"c.bin"}},
		{"one tag", "", ObjectFacets{Tags: []Tag{{Key: "env", Value: "prod"}}}, []string{"a.png"}},
		{"either of two tags", "", ObjectFacets{Tags: []Tag{{Key: "env", Value: "prod"}, {Key: "env", Value: "dev"}}}, []string{"a.png", "b.pdf"}},
		{"a tag quoted as it is written", "", ObjectFacets{Tags: []Tag{{Key: "a\"b", Value: "c\\d"}}}, []string{"b.pdf"}},
		{"a tag and a state", "", ObjectFacets{
			States: []statemachine.State{statemachine.StateAvailable},
			Tags:   []Tag{{Key: "env", Value: "dev"}},
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr := withFacets(c.filter, c.facets)
			prog, err := ev.Compile(cel.ObjectSchema, expr)
			if err != nil {
				t.Fatalf("compile %q: %v", expr, err)
			}
			var got []string
			for _, o := range objects {
				if matchesObject(t, prog, o) {
					got = append(got, o.Key)
				}
			}
			if !equalStrings(got, c.want) {
				t.Errorf("%q kept %v, want %v", expr, got, c.want)
			}
		})
	}
}

// The named facets are pushed into SQL: every branch of the expanded filter
// carries the state and the content type, so a selective facet is one indexed
// query rather than a scan.
func TestWithFacets_PushesDown(t *testing.T) {
	expr := withFacets("", ObjectFacets{
		States:     []statemachine.State{statemachine.StateAvailable},
		Categories: []ContentCategory{CategoryImage, CategoryPDF},
	})
	branches, err := cel.ExtractObjectBranches(expr)
	if err != nil {
		t.Fatalf("ExtractObjectBranches: %v", err)
	}
	const wantBranches = 2 // image/ prefix, application/pdf exact
	if len(branches) != wantBranches {
		t.Fatalf("%d branches, want %d: %+v", len(branches), wantBranches, branches)
	}
	for _, b := range branches {
		if b.StateEq != string(statemachine.StateAvailable) {
			t.Errorf("branch %+v does not push the state", b)
		}
		if b.ContentTypePrefix == "" && b.ContentTypeEq == "" {
			t.Errorf("branch %+v does not push the content type", b)
		}
	}
}

// Each facet is counted with every choice applied but its own.
func TestFacetCountsOf_CountsEachFacetWithoutItsOwnChoices(t *testing.T) {
	tally := ObjectTally{Exact: true, Counts: map[ObjectTallyKey]int64{
		{State: statemachine.StateAvailable, ContentType: "image/png"}:       3,
		{State: statemachine.StatePending, ContentType: "image/jpeg"}:        2,
		{State: statemachine.StateAvailable, ContentType: "application/pdf"}: 5,
	}}

	t.Run("no choices", func(t *testing.T) {
		got := facetCountsOf(tally, ObjectFacets{})
		wantStates := map[statemachine.State]int64{statemachine.StateAvailable: 8, statemachine.StatePending: 2}
		wantCats := map[ContentCategory]int64{CategoryImage: 5, CategoryPDF: 5}
		assertCounts(t, got, wantStates, wantCats)
		if !got.Exact {
			t.Error("an exact tally gave inexact counts")
		}
	})
	t.Run("a category chosen narrows the states, not the categories", func(t *testing.T) {
		got := facetCountsOf(tally, ObjectFacets{Categories: []ContentCategory{CategoryImage}})
		assertCounts(t, got,
			map[statemachine.State]int64{statemachine.StateAvailable: 3, statemachine.StatePending: 2},
			map[ContentCategory]int64{CategoryImage: 5, CategoryPDF: 5})
	})
	t.Run("a state chosen narrows the categories, not the states", func(t *testing.T) {
		got := facetCountsOf(tally, ObjectFacets{States: []statemachine.State{statemachine.StatePending}})
		assertCounts(t, got,
			map[statemachine.State]int64{statemachine.StateAvailable: 8, statemachine.StatePending: 2},
			map[ContentCategory]int64{CategoryImage: 2})
	})
	t.Run("a capped tally stays inexact", func(t *testing.T) {
		if facetCountsOf(ObjectTally{Counts: tally.Counts}, ObjectFacets{}).Exact {
			t.Error("a capped tally gave exact counts")
		}
	})
}

// A tag is counted over the objects the chosen states and categories admit,
// and the chosen tags narrow the states and categories, not the tags.
func TestFacetCountsOf_CountsTagsWithoutTheirOwnChoices(t *testing.T) {
	env := func(v string) Tag { return Tag{Key: "env", Value: v} }
	tally := NewObjectTally()
	chosen := []Tag{env("prod")}
	tally.Tally(statemachine.StateAvailable, "image/png", map[string]string{"env": "prod"}, chosen)
	tally.Tally(statemachine.StateAvailable, "image/png", map[string]string{"env": "dev"}, chosen)
	tally.Tally(statemachine.StatePending, "application/pdf", map[string]string{"env": "dev", "team": "a"}, chosen)
	tally.Tally(statemachine.StateAvailable, "text/plain", nil, chosen)

	t.Run("a tag chosen narrows the states and categories, not the tags", func(t *testing.T) {
		got := facetCountsOf(tally, ObjectFacets{Tags: chosen})
		assertCounts(t, got,
			map[statemachine.State]int64{statemachine.StateAvailable: 1},
			map[ContentCategory]int64{CategoryImage: 1})
		want := []TagCount{{env("dev"), 2}, {env("prod"), 1}, {Tag{"team", "a"}, 1}}
		if !slices.Equal(got.Tags, want) || got.TagsTruncated {
			t.Errorf("tags = %v (truncated %v), want %v", got.Tags, got.TagsTruncated, want)
		}
	})
	t.Run("a state chosen narrows the tags", func(t *testing.T) {
		got := facetCountsOf(tally, ObjectFacets{States: []statemachine.State{statemachine.StateAvailable}})
		want := []TagCount{{env("dev"), 1}, {env("prod"), 1}}
		if !slices.Equal(got.Tags, want) {
			t.Errorf("tags = %v, want %v", got.Tags, want)
		}
		assertCounts(t, got,
			map[statemachine.State]int64{statemachine.StateAvailable: 3, statemachine.StatePending: 1},
			map[ContentCategory]int64{CategoryImage: 2, CategoryText: 1})
	})
}

// A key keeps its most frequent values up to the limit, and says it had more.
func TestTopTagCounts(t *testing.T) {
	counts := map[Tag]int64{
		{"k", "rare"}: 1, {"k", "common"}: 5, {"k", "also"}: 1, {"j", "x"}: 2,
	}
	const limit = 2
	got, truncated := topTagCounts(counts, limit)
	want := []TagCount{{Tag{"j", "x"}, 2}, {Tag{"k", "common"}, 5}, {Tag{"k", "also"}, 1}}
	if !slices.Equal(got, want) || !truncated {
		t.Errorf("topTagCounts = %v, %v; want %v, true", got, truncated, want)
	}
	if _, truncated := topTagCounts(counts, len(counts)); truncated {
		t.Error("a limit every key fits in reported truncation")
	}
}

// tallyRepo records the tally it was asked for and answers a fixed one.
type tallyRepo struct {
	listRepo
	got   TallyObjectsArgs
	tally ObjectTally
}

func (r *tallyRepo) TallyObjects(_ context.Context, args TallyObjectsArgs) (ObjectTally, error) {
	r.got = args
	return r.tally, nil
}

func TestCountObjectFacets(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
	tally := ObjectTally{Exact: true, Counts: map[ObjectTallyKey]int64{
		{State: statemachine.StateAvailable, ContentType: "image/png"}: 1,
	}}
	newHandler := func(perObject bool) (*Handler, *tallyRepo) {
		repo := &tallyRepo{tally: tally}
		return &Handler{repo: repo, policy: perRowAuthorizer{perObject: perObject}, filter: cel.NewEvaluator()}, repo
	}

	t.Run("counts over the request's filter, prefix and level", func(t *testing.T) {
		h, repo := newHandler(false)
		in := ListObjectsInput{Collection: "docs", Filter: `key.contains("x")`, Prefix: "a/", Delimiter: "/"}
		got, err := h.CountObjectFacets(ctx, in)
		if err != nil {
			t.Fatalf("CountObjectFacets: %v", err)
		}
		if got == nil || got.States[statemachine.StateAvailable] != 1 {
			t.Fatalf("counts = %+v, want the tally's", got)
		}
		if repo.got.Filter != in.Filter || repo.got.Prefix != in.Prefix || repo.got.Delimiter != in.Delimiter {
			t.Errorf("tallied %+v, want the request's filter, prefix and delimiter", repo.got)
		}
		if repo.got.IncludeDeleted {
			t.Error("a listing that chose no trashed state tallied trashed objects")
		}
	})
	t.Run("the tag choices are tallied by, not narrowed by", func(t *testing.T) {
		h, repo := newHandler(false)
		tags := []Tag{{Key: "env", Value: "prod"}}
		in := ListObjectsInput{Collection: "docs", Filter: `key.contains("x")`, Facets: ObjectFacets{Tags: tags}}
		if _, err := h.CountObjectFacets(ctx, in); err != nil {
			t.Fatalf("CountObjectFacets: %v", err)
		}
		if !slices.Equal(repo.got.Tags, tags) || repo.got.Filter != in.Filter {
			t.Errorf("tallied %+v, want the tag choices beside the request's own filter", repo.got)
		}
	})
	t.Run("a chosen trashed state tallies trashed objects", func(t *testing.T) {
		h, repo := newHandler(false)
		in := ListObjectsInput{Collection: "docs", Facets: ObjectFacets{States: []statemachine.State{statemachine.StateDeleted}}}
		if _, err := h.CountObjectFacets(ctx, in); err != nil {
			t.Fatalf("CountObjectFacets: %v", err)
		}
		if !repo.got.IncludeDeleted {
			t.Error("the trashed state was chosen but not tallied")
		}
	})
	t.Run("no counts when the policies decide object by object", func(t *testing.T) {
		h, _ := newHandler(true)
		got, err := h.CountObjectFacets(ctx, ListObjectsInput{Collection: "docs"})
		if err != nil {
			t.Fatalf("CountObjectFacets: %v", err)
		}
		if got != nil {
			t.Fatalf("counts = %+v, want none: they would count objects the listing hides", got)
		}
	})
}

func assertCounts(t *testing.T, got *ObjectFacetCounts, states map[statemachine.State]int64, cats map[ContentCategory]int64) {
	t.Helper()
	if len(got.States) != len(states) {
		t.Errorf("states = %v, want %v", got.States, states)
	}
	for s, n := range states {
		if got.States[s] != n {
			t.Errorf("state %s = %d, want %d", s, got.States[s], n)
		}
	}
	if len(got.Categories) != len(cats) {
		t.Errorf("categories = %v, want %v", got.Categories, cats)
	}
	for c, n := range cats {
		if got.Categories[c] != n {
			t.Errorf("category %d = %d, want %d", c, got.Categories[c], n)
		}
	}
}

// matchesObject is whether the listing keeps o, as the store decides it
// (rowMatches): an evaluation error, such as a tag key o lacks, keeps it out.
func matchesObject(t *testing.T, prog cel.Program, o Object) bool {
	t.Helper()
	ok, err := cel.Match(prog, cel.ObjectVars(cel.ObjectRow{Key: o.Key, State: string(o.State), ContentType: o.ContentType, Tags: o.Tags}))
	return err == nil && ok
}

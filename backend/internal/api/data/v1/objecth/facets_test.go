package objecth

import (
	"context"
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
		{Key: "a.png", State: statemachine.StateAvailable, ContentType: "image/png"},
		{Key: "b.pdf", State: statemachine.StatePending, ContentType: "application/pdf"},
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

// matchesObject evaluates prog against o as the store does.
func matchesObject(t *testing.T, prog cel.Program, o Object) bool {
	t.Helper()
	ok, err := cel.Match(prog, cel.ObjectVars(cel.ObjectRow{Key: o.Key, State: string(o.State), ContentType: o.ContentType}))
	if err != nil {
		t.Fatalf("match %s: %v", o.Key, err)
	}
	return ok
}

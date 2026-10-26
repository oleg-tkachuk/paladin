package data

import (
	"context"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// facetHandler lists nothing, answers counts, and keeps what it was asked.
type facetHandler struct {
	folderHandler
	counted   int
	countedIn objecth.ListObjectsInput
	counts    *objecth.ObjectFacetCounts
}

func (f *facetHandler) CountObjectFacets(_ context.Context, in objecth.ListObjectsInput) (*objecth.ObjectFacetCounts, error) {
	f.counted++
	f.countedIn = in
	return f.counts, nil
}

func TestListObjects_Facets(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"
	facets := &pb.ObjectFacets{
		States:            []pb.ObjectState{pb.ObjectState_OBJECT_STATE_PENDING},
		ContentCategories: []pb.ObjectContentCategory{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_PDF, pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_OTHER},
	}
	counts := &objecth.ObjectFacetCounts{
		States:     map[statemachine.State]int64{statemachine.StateAvailable: 4, statemachine.StatePending: 1},
		Categories: map[objecth.ContentCategory]int64{objecth.CategoryPDF: 2},
		Exact:      true,
	}

	t.Run("the facets reach the listing and the count", func(t *testing.T) {
		h := &facetHandler{counts: counts}
		out, err := (&ObjectServer{H: h}).ListObjects(ctx, &pb.ListObjectsRequest{Parent: parent, Facets: facets, CountFacets: true})
		if err != nil {
			t.Fatal(err)
		}
		wantStates := []statemachine.State{statemachine.StatePending}
		wantCats := []objecth.ContentCategory{objecth.CategoryPDF, objecth.CategoryOther}
		if !slices.Equal(h.listed.Facets.States, wantStates) || !slices.Equal(h.listed.Facets.Categories, wantCats) {
			t.Errorf("listed with facets %+v, want %v %v", h.listed.Facets, wantStates, wantCats)
		}
		if !slices.Equal(h.countedIn.Facets.Categories, wantCats) {
			t.Errorf("counted with facets %+v", h.countedIn.Facets)
		}
		want := []*commonpb.FacetCount{
			{Facet: facetContentCategory, Value: "OBJECT_CONTENT_CATEGORY_PDF", Count: 2},
			{Facet: facetState, Value: "OBJECT_STATE_AVAILABLE", Count: 4},
			{Facet: facetState, Value: "OBJECT_STATE_PENDING", Count: 1},
		}
		got := out.GetFacetCounts()
		if len(got) != len(want) {
			t.Fatalf("facet counts = %v, want %v", got, want)
		}
		for i := range want {
			if got[i].GetFacet() != want[i].GetFacet() || got[i].GetValue() != want[i].GetValue() || got[i].GetCount() != want[i].GetCount() {
				t.Errorf("count %d = %v, want %v", i, got[i], want[i])
			}
		}
		if !out.GetFacetCountsExact() {
			t.Error("exact counts reported as inexact")
		}
	})

	t.Run("no counts unless asked", func(t *testing.T) {
		h := &facetHandler{counts: counts}
		out, err := (&ObjectServer{H: h}).ListObjects(ctx, &pb.ListObjectsRequest{Parent: parent, Facets: facets})
		if err != nil || h.counted != 0 || len(out.GetFacetCounts()) != 0 {
			t.Errorf("counted %d times, counts %v, err %v", h.counted, out.GetFacetCounts(), err)
		}
	})

	t.Run("no counts on a later page", func(t *testing.T) {
		h := &facetHandler{counts: counts}
		out, err := (&ObjectServer{H: h}).ListObjects(ctx, &pb.ListObjectsRequest{
			Parent: parent, CountFacets: true, Page: &commonpb.PageRequest{PageToken: "next"},
		})
		if err != nil || h.counted != 0 || len(out.GetFacetCounts()) != 0 {
			t.Errorf("counted %d times, counts %v, err %v", h.counted, out.GetFacetCounts(), err)
		}
	})

	t.Run("no counts when the handler withholds them", func(t *testing.T) {
		h := &facetHandler{}
		out, err := (&ObjectServer{H: h}).ListObjects(ctx, &pb.ListObjectsRequest{Parent: parent, CountFacets: true})
		if err != nil || h.counted != 1 || len(out.GetFacetCounts()) != 0 || out.GetFacetCountsExact() {
			t.Errorf("counted %d times, counts %v exact %v, err %v", h.counted, out.GetFacetCounts(), out.GetFacetCountsExact(), err)
		}
	})
}

// Every wire category maps to a domain one and back, so a category added to
// the proto without a mapping fails here rather than vanishing from a filter.
func TestContentCategories_CoverTheEnum(t *testing.T) {
	for v, name := range pb.ObjectContentCategory_name {
		c := pb.ObjectContentCategory(v)
		if c == pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_UNSPECIFIED {
			continue
		}
		got := objectFacetsFromProto(&pb.ObjectFacets{ContentCategories: []pb.ObjectContentCategory{c}})
		if len(got.Categories) != 1 {
			t.Errorf("%s maps to %v", name, got.Categories)
		}
	}
}

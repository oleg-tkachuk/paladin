package data

import (
	"sort"

	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// The facet names ListObjectsResponse.facet_counts carries, as the API
// documents them.
const (
	facetState           = "state"
	facetContentCategory = "content_category"
)

// objectStatesFromProto maps the wire states onto the state machine's;
// validation has already refused UNSPECIFIED and unknown values.
var objectStatesFromProto = map[pb.ObjectState]statemachine.State{
	pb.ObjectState_OBJECT_STATE_PENDING:   statemachine.StatePending,
	pb.ObjectState_OBJECT_STATE_AVAILABLE: statemachine.StateAvailable,
	pb.ObjectState_OBJECT_STATE_DELETED:   statemachine.StateDeleted,
}

// contentCategories pairs the wire categories with the domain's, both ways.
var contentCategories = []struct {
	wire   pb.ObjectContentCategory
	domain objecth.ContentCategory
}{
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_IMAGE, objecth.CategoryImage},
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_VIDEO, objecth.CategoryVideo},
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_AUDIO, objecth.CategoryAudio},
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_TEXT, objecth.CategoryText},
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_PDF, objecth.CategoryPDF},
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_JSON, objecth.CategoryJSON},
	{pb.ObjectContentCategory_OBJECT_CONTENT_CATEGORY_OTHER, objecth.CategoryOther},
}

func objectFacetsFromProto(f *pb.ObjectFacets) objecth.ObjectFacets {
	var out objecth.ObjectFacets
	for _, s := range f.GetStates() {
		if v, ok := objectStatesFromProto[s]; ok {
			out.States = append(out.States, v)
		}
	}
	for _, c := range f.GetContentCategories() {
		for _, cc := range contentCategories {
			if cc.wire == c {
				out.Categories = append(out.Categories, cc.domain)
			}
		}
	}
	return out
}

// objectFacetCountsToProto lists the counts as FacetCounts, each facet's
// values in name order so a response is stable.
func objectFacetCountsToProto(c *objecth.ObjectFacetCounts) []*commonpb.FacetCount {
	var out []*commonpb.FacetCount
	for state, n := range c.States {
		out = append(out, &commonpb.FacetCount{Facet: facetState, Value: objectStateProto(state).String(), Count: n})
	}
	for _, cc := range contentCategories {
		if n, ok := c.Categories[cc.domain]; ok {
			out = append(out, &commonpb.FacetCount{Facet: facetContentCategory, Value: cc.wire.String(), Count: n})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].GetFacet() != out[j].GetFacet() {
			return out[i].GetFacet() < out[j].GetFacet()
		}
		return out[i].GetValue() < out[j].GetValue()
	})
	return out
}

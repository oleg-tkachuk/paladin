package objecth

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// ContentCategory groups content types the way the console filters them. The
// server owns the mapping because it counts by it.
type ContentCategory int

const (
	CategoryImage ContentCategory = iota + 1
	CategoryVideo
	CategoryAudio
	CategoryText
	CategoryPDF
	CategoryJSON
	// CategoryOther is every content type the others do not name.
	CategoryOther
)

// contentCategoryRule is how one category recognises a content type: by
// prefix, or by the whole type.
type contentCategoryRule struct {
	category ContentCategory
	prefix   string
	exact    string
}

// contentCategoryRules are the named categories, in the order they are tried.
// CategoryOther is what none of them matches.
var contentCategoryRules = []contentCategoryRule{
	{category: CategoryImage, prefix: "image/"},
	{category: CategoryVideo, prefix: "video/"},
	{category: CategoryAudio, prefix: "audio/"},
	{category: CategoryText, prefix: "text/"},
	{category: CategoryPDF, exact: "application/pdf"},
	{category: CategoryJSON, exact: "application/json"},
}

// CategoryOf is the category a content type falls in.
func CategoryOf(contentType string) ContentCategory {
	for _, r := range contentCategoryRules {
		if r.matches(contentType) {
			return r.category
		}
	}
	return CategoryOther
}

func (r contentCategoryRule) matches(contentType string) bool {
	if r.exact != "" {
		return contentType == r.exact
	}
	return strings.HasPrefix(contentType, r.prefix)
}

// celClause is the rule as a CEL condition on content_type, in the forms the
// SQL pushdown recognises (== and startsWith).
func (r contentCategoryRule) celClause() string {
	if r.exact != "" {
		return objectContentTypeField + " == " + strconv.Quote(r.exact)
	}
	return objectContentTypeField + ".startsWith(" + strconv.Quote(r.prefix) + ")"
}

// CEL field names of ObjectSchema the facets narrow on.
const (
	objectStateField       = "state"
	objectContentTypeField = "content_type"
	objectTagsField        = "tags"
)

// CEL operators the facet clauses are joined with.
const (
	celAnd = " && "
	celOr  = " || "
	celNot = "!"
)

// ObjectFacets narrows an object listing by state, content category and tag,
// each a disjunction; an empty one narrows nothing.
type ObjectFacets struct {
	States     []statemachine.State
	Categories []ContentCategory
	Tags       []Tag
}

// Tag is one tag of an object: a key and its value.
type Tag struct {
	Key   string
	Value string
}

// tagClause is the tags facet as a CEL disjunction, "" when empty.
func tagClause(tags []Tag) string {
	parts := make([]string, 0, len(tags))
	for _, t := range tags {
		parts = append(parts, objectTagsField+"["+strconv.Quote(t.Key)+"] == "+strconv.Quote(t.Value))
	}
	return group(parts, celOr)
}

// carriesAny reports whether tags holds one of the chosen tags.
func carriesAny(tags map[string]string, chosen []Tag) bool {
	for _, c := range chosen {
		if v, ok := tags[c.Key]; ok && v == c.Value {
			return true
		}
	}
	return false
}

// stateClause is the states facet as a CEL disjunction, "" when empty.
func stateClause(states []statemachine.State) string {
	parts := make([]string, 0, len(states))
	for _, s := range states {
		parts = append(parts, objectStateField+" == "+strconv.Quote(string(s)))
	}
	return group(parts, celOr)
}

// categoryClause is the categories facet as a CEL disjunction, "" when empty.
// CategoryOther is the negation of every named rule: SQL cannot push that
// down, so it narrows only through the compiled program.
func categoryClause(categories []ContentCategory) string {
	parts := make([]string, 0, len(categories))
	for _, c := range categories {
		if c == CategoryOther {
			named := make([]string, 0, len(contentCategoryRules))
			for _, r := range contentCategoryRules {
				named = append(named, r.celClause())
			}
			parts = append(parts, celNot+"("+group(named, celOr)+")")
			continue
		}
		for _, r := range contentCategoryRules {
			if r.category == c {
				parts = append(parts, r.celClause())
			}
		}
	}
	return group(parts, celOr)
}

// withFacets is filter with the facets as further conjuncts.
func withFacets(filter string, f ObjectFacets) string {
	return group(nonEmpty(filter, stateClause(f.States), categoryClause(f.Categories), tagClause(f.Tags)), celAnd)
}

// group joins parts with op, each in parentheses; one part is returned as is
// and none as "".
func group(parts []string, op string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	wrapped := make([]string, len(parts))
	for i, p := range parts {
		wrapped[i] = "(" + p + ")"
	}
	return strings.Join(wrapped, op)
}

func nonEmpty(parts ...string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ObjectTally is how many objects of a listing have each state and content
// type together, and each of those with each tag: enough to count each facet
// without its own choices.
type ObjectTally struct {
	Counts map[ObjectTallyKey]int64
	// TagCounts counts each tag an object carries, by the object's state and
	// content type.
	TagCounts map[ObjectTagTallyKey]int64
	// Exact is false when the scan stopped at its cap.
	Exact bool
}

// ObjectTallyKey is one state and content type pair, and whether the objects
// carry one of the tags the listing chose (TallyObjectsArgs.Tags); false
// when it chose none.
type ObjectTallyKey struct {
	State       statemachine.State
	ContentType string
	Tagged      bool
}

// ObjectTagTallyKey is one tag on objects of one state and content type.
type ObjectTagTallyKey struct {
	State       statemachine.State
	ContentType string
	Tag         Tag
}

// Tally counts one object into t: its state and content type, Tagged by
// whether its tags hold one of chosen, and each of its tags.
func (t *ObjectTally) Tally(state statemachine.State, contentType string, tags map[string]string, chosen []Tag) {
	t.Counts[ObjectTallyKey{State: state, ContentType: contentType, Tagged: carriesAny(tags, chosen)}]++
	for k, v := range tags {
		t.TagCounts[ObjectTagTallyKey{State: state, ContentType: contentType, Tag: Tag{Key: k, Value: v}}]++
	}
}

// NewObjectTally is an empty tally.
func NewObjectTally() ObjectTally {
	return ObjectTally{Counts: map[ObjectTallyKey]int64{}, TagCounts: map[ObjectTagTallyKey]int64{}}
}

// TallyObjectsArgs is a listing to tally: ListObjectsArgs without the order
// and the page.
type TallyObjectsArgs struct {
	TenantID    uuid.UUID
	Collection  string
	CompiledCEL cel.Program
	Filter      string
	Prefix      string
	Delimiter   string
	// IncludeDeleted keeps trashed objects in the scan, as a listing that
	// names a state does.
	IncludeDeleted bool
	// Tags are the listing's tag choices, which the tally marks each object
	// by (ObjectTallyKey.Tagged) rather than narrows by.
	Tags []Tag
}

// ObjectFacetCounts is the answer to count_facets.
type ObjectFacetCounts struct {
	States     map[statemachine.State]int64
	Categories map[ContentCategory]int64
	// Tags holds, for each key, at most distinctTagValueLimit of its values,
	// the most frequent first, then by value.
	Tags []TagCount
	// TagsTruncated is true when a key carried more values than Tags holds.
	TagsTruncated bool
	Exact         bool
}

// TagCount is how many objects of a listing carry one tag.
type TagCount struct {
	Tag   Tag
	Count int64
}

// CountObjectFacets counts the listing in's facets: each state over the
// objects its categories admit, and each category over those its states
// admit — every constraint of in but the facet's own choices. It returns nil
// when the collection's policies decide object by object, since the counts
// would then include objects the listing does not return.
func (h *Handler) CountObjectFacets(ctx context.Context, in ListObjectsInput) (*ObjectFacetCounts, error) {
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, capabilityCollectionURI(tenantID, in.Collection)); err != nil {
		return nil, err
	}
	backendID, bucket, _ := h.repo.LookupBucket(ctx, tenantID, in.Collection, false) // count (read)
	if err := h.authorize(ctx, principal, tenantID, &cedar.Resource{
		TenantID: tenantID, Collection: in.Collection,
		BackendID: backendID, BucketName: bucket,
	}, cedar.ActionGetObject, 0, ""); err != nil {
		return nil, err
	}
	if pe, ok := h.policy.(cedar.PerObjectEvaluator); ok {
		perRow, err := pe.NeedsPerObjectEval(ctx, tenantID, in.Collection)
		if err != nil {
			return nil, apiutil.MapError(fmt.Errorf("authz: %w", err))
		}
		if perRow {
			return nil, nil
		}
	}
	prog, err := h.filter.Compile(cel.ObjectSchema, in.Filter)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	tally, err := h.repo.TallyObjects(ctx, TallyObjectsArgs{
		TenantID:       tenantID,
		Collection:     in.Collection,
		CompiledCEL:    prog,
		Filter:         in.Filter,
		Prefix:         in.Prefix,
		Delimiter:      in.Delimiter,
		IncludeDeleted: slices.Contains(in.Facets.States, statemachine.StateDeleted),
		Tags:           in.Facets.Tags,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return facetCountsOf(tally, in.Facets), nil
}

// facetCountsOf folds a tally into facet counts: a state counted over the
// objects the chosen categories and tags admit, a category over those the
// chosen states and tags admit, a tag over those the chosen states and
// categories admit.
func facetCountsOf(t ObjectTally, f ObjectFacets) *ObjectFacetCounts {
	out := &ObjectFacetCounts{
		States:     map[statemachine.State]int64{},
		Categories: map[ContentCategory]int64{},
		Exact:      t.Exact,
	}
	statesAdmit := func(s statemachine.State) bool { return len(f.States) == 0 || slices.Contains(f.States, s) }
	categoriesAdmit := func(c ContentCategory) bool { return len(f.Categories) == 0 || slices.Contains(f.Categories, c) }
	for k, n := range t.Counts {
		if len(f.Tags) > 0 && !k.Tagged {
			continue
		}
		category := CategoryOf(k.ContentType)
		if categoriesAdmit(category) {
			out.States[k.State] += n
		}
		if statesAdmit(k.State) {
			out.Categories[category] += n
		}
	}
	tags := map[Tag]int64{}
	for k, n := range t.TagCounts {
		if statesAdmit(k.State) && categoriesAdmit(CategoryOf(k.ContentType)) {
			tags[k.Tag] += n
		}
	}
	out.Tags, out.TagsTruncated = topTagCounts(tags, distinctTagValueLimit)
	return out
}

// topTagCounts lists counts by key, each key's values the most frequent
// first, then by value, at most limit of them; truncated says a key had more.
func topTagCounts(counts map[Tag]int64, limit int) (out []TagCount, truncated bool) {
	byKey := map[string][]TagCount{}
	for tag, n := range counts {
		byKey[tag.Key] = append(byKey[tag.Key], TagCount{Tag: tag, Count: n})
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		values := byKey[k]
		slices.SortFunc(values, func(a, b TagCount) int {
			if a.Count != b.Count {
				return cmp.Compare(b.Count, a.Count)
			}
			return strings.Compare(a.Tag.Value, b.Tag.Value)
		})
		if len(values) > limit {
			values, truncated = values[:limit], true
		}
		out = append(out, values...)
	}
	return out, truncated
}

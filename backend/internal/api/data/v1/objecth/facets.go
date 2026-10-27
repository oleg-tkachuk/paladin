package objecth

import (
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
)

// CEL operators the facet clauses are joined with.
const (
	celAnd = " && "
	celOr  = " || "
	celNot = "!"
)

// ObjectFacets narrows an object listing by state and content category, each
// a disjunction; an empty one narrows nothing.
type ObjectFacets struct {
	States     []statemachine.State
	Categories []ContentCategory
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
	return group(nonEmpty(filter, stateClause(f.States), categoryClause(f.Categories)), celAnd)
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
// type together: enough to count each facet without its own choices.
type ObjectTally struct {
	Counts map[ObjectTallyKey]int64
	// Exact is false when the scan stopped at its cap.
	Exact bool
}

// ObjectTallyKey is one state and content type pair.
type ObjectTallyKey struct {
	State       statemachine.State
	ContentType string
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
}

// ObjectFacetCounts is the answer to count_facets.
type ObjectFacetCounts struct {
	States     map[statemachine.State]int64
	Categories map[ContentCategory]int64
	Exact      bool
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
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return facetCountsOf(tally, in.Facets), nil
}

// facetCountsOf folds a tally into facet counts: a state counted over the
// objects the chosen categories admit, a category over those the chosen
// states admit.
func facetCountsOf(t ObjectTally, f ObjectFacets) *ObjectFacetCounts {
	out := &ObjectFacetCounts{
		States:     map[statemachine.State]int64{},
		Categories: map[ContentCategory]int64{},
		Exact:      t.Exact,
	}
	for k, n := range t.Counts {
		category := CategoryOf(k.ContentType)
		if len(f.Categories) == 0 || slices.Contains(f.Categories, category) {
			out.States[k.State] += n
		}
		if len(f.States) == 0 || slices.Contains(f.States, k.State) {
			out.Categories[category] += n
		}
	}
	return out
}

// The facets a collection's object listing narrows and counts by on the
// server: an object's state and its content category. Pure, so the choices,
// the request they make and the counts the answer gives are tested once.
//
// The facet ids are the URL parameters the page's single-choice filters used,
// and the choices keep the values those filters wrote — the state's own name,
// the category's in lower case — so a link or a saved view from before reads
// as the same choice, now one of several.
import type { FacetCount } from "@/gen/paladin/common/v1/facet_pb";
import {
  ObjectContentCategory,
  ObjectContentCategorySchema,
} from "@/gen/paladin/data/v1/object_service_pb";
import {
  ObjectState,
  ObjectStateSchema,
  type Object$,
} from "@/gen/paladin/data/v1/types_pb";
import {
  groupFacetCounts,
  type Facet,
  type FacetCounts,
  type FacetOption,
  type FacetSelection,
} from "@/lib/table/facets";

/** The state facet's id, and the URL parameter its choices are kept under. */
export const OBJECT_FACET_STATUS = "status";
/** The content-category facet's id, and its URL parameter. */
export const OBJECT_FACET_TYPE = "type";

// The facets as ListObjectsResponse.facet_counts names them.
const SERVER_FACET_STATE = "state";
const SERVER_FACET_CATEGORY = "content_category";

type NamedState = Exclude<ObjectState, ObjectState.UNSPECIFIED>;
type NamedCategory = Exclude<
  ObjectContentCategory,
  ObjectContentCategory.UNSPECIFIED
>;

const STATE_LABEL: Record<NamedState, string> = {
  [ObjectState.PENDING]: "Pending",
  [ObjectState.AVAILABLE]: "Available",
  [ObjectState.DELETED]: "Deleted",
};

const CATEGORY_LABEL: Record<NamedCategory, string> = {
  [ObjectContentCategory.IMAGE]: "Images",
  [ObjectContentCategory.VIDEO]: "Video",
  [ObjectContentCategory.AUDIO]: "Audio",
  [ObjectContentCategory.TEXT]: "Text",
  [ObjectContentCategory.PDF]: "PDF",
  [ObjectContentCategory.JSON]: "JSON",
  [ObjectContentCategory.OTHER]: "Other",
};

/** A state as the facet names it: the enum's own name, "AVAILABLE". */
function stateValue(state: ObjectState): string {
  return ObjectStateSchema.value[state]?.localName ?? "";
}

/** A category as the facet names it: the enum's name in lower case, "pdf". */
function categoryValue(category: ObjectContentCategory): string {
  return (
    ObjectContentCategorySchema.value[category]?.localName.toLowerCase() ?? ""
  );
}

/** States the object listing does not offer: a deleted object is the trash's,
 *  which lists it on a page of its own. */
const STATES_LISTED_ELSEWHERE: readonly ObjectState[] = [ObjectState.DELETED];

/** Each state the enum names that the listing offers, in the enum's order. */
const STATE_OPTIONS: readonly FacetOption[] = ObjectStateSchema.values
  .filter(
    (v) =>
      v.number !== ObjectState.UNSPECIFIED &&
      !STATES_LISTED_ELSEWHERE.includes(v.number as ObjectState),
  )
  .map((v) => ({
    value: v.localName,
    label: STATE_LABEL[v.number as NamedState],
  }));

/** Each content category the enum names, in its order: Other last. */
const CATEGORY_OPTIONS: readonly FacetOption[] =
  ObjectContentCategorySchema.values
    .filter((v) => v.number !== ObjectContentCategory.UNSPECIFIED)
    .map((v) => ({
      value: categoryValue(v.number as ObjectContentCategory),
      label: CATEGORY_LABEL[v.number as NamedCategory],
    }));

/** The object listing's facets: where an object stands, what it holds. */
export const OBJECT_FACETS: readonly Facet<Object$>[] = [
  {
    id: OBJECT_FACET_STATUS,
    label: "Status",
    values: (o) => [stateValue(o.state)],
    options: STATE_OPTIONS,
  },
  {
    id: OBJECT_FACET_TYPE,
    label: "Content type",
    // The server owns which category a content type falls in, and this list
    // is never narrowed in the browser, so no row answers here.
    values: () => [],
    options: CATEGORY_OPTIONS,
  },
];

/** The choices of one facet that its vocabulary holds, in its order. */
function known(options: readonly FacetOption[], values: readonly string[]) {
  return options.map((o) => o.value).filter((v) => values.includes(v));
}

/**
 * The selection a saved view names. A view saved before the filters took
 * several choices names one, as a string; a value the facet does not offer is
 * dropped, as the URL drops one.
 */
export function viewFacetSelection(filters: {
  status?: string | readonly string[];
  type?: string | readonly string[];
}): FacetSelection {
  const list = (v?: string | readonly string[]) =>
    v === undefined ? [] : typeof v === "string" ? [v] : v;
  return {
    [OBJECT_FACET_STATUS]: known(STATE_OPTIONS, list(filters.status)),
    [OBJECT_FACET_TYPE]: known(CATEGORY_OPTIONS, list(filters.type)),
  };
}

/** The facet choices as ListObjectsRequest.facets names them. */
export function objectFacetsRequest(selection: FacetSelection): {
  states: ObjectState[];
  contentCategories: ObjectContentCategory[];
} {
  const states = (selection[OBJECT_FACET_STATUS] ?? []).flatMap((value) => {
    const v = ObjectStateSchema.values.find(
      (e) => e.number !== ObjectState.UNSPECIFIED && e.localName === value,
    );
    return v ? [v.number as ObjectState] : [];
  });
  const contentCategories = (selection[OBJECT_FACET_TYPE] ?? []).flatMap(
    (value) => {
      const v = ObjectContentCategorySchema.values.find(
        (e) =>
          e.number !== ObjectContentCategory.UNSPECIFIED &&
          categoryValue(e.number as ObjectContentCategory) === value,
      );
      return v ? [v.number as ObjectContentCategory] : [];
    },
  );
  return { states, contentCategories };
}

/**
 * The server's counts as the facets name them: "state" counts the status
 * facet by the state's wire name, "OBJECT_STATE_AVAILABLE", offered as
 * "AVAILABLE"; "content_category" counts the type facet by
 * "OBJECT_CONTENT_CATEGORY_PDF", offered as "pdf". A facet the page does not
 * offer is left out.
 */
export function objectFacetCounts(counts: readonly FacetCount[]): FacetCounts {
  const named = counts.flatMap((c) => {
    if (c.facet === SERVER_FACET_STATE) {
      const v = ObjectStateSchema.values.find((e) => e.name === c.value);
      return v
        ? [{ facet: OBJECT_FACET_STATUS, value: v.localName, count: c.count }]
        : [];
    }
    if (c.facet === SERVER_FACET_CATEGORY) {
      const v = ObjectContentCategorySchema.values.find(
        (e) => e.name === c.value,
      );
      return v
        ? [
            {
              facet: OBJECT_FACET_TYPE,
              value: categoryValue(v.number as ObjectContentCategory),
              count: c.count,
            },
          ]
        : [];
    }
    return [];
  });
  return groupFacetCounts(named);
}

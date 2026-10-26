// URL-synced filter, folder and sort state for the Collection objects page,
// on the same contract as every other list (useUrlState, TABLE_PARAMS): the
// search, the sort, the tag / metadata filters, the folder and whether to list
// everything under it, and the status and content-type facets the server
// narrows and counts by (useServerFacets). Derives the CEL `filter` and the
// `facets` the data hook sends. Selection, tag-option accumulation, and the data fetch itself stay
// in the page — they depend on the loaded objects, which this hook never sees.
import { useCallback, useMemo } from "react";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";

import { type FacetControl, useServerFacets } from "@/hooks/useTableFacets";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { flagParam, textParam, useUrlState } from "@/hooks/useUrlState";
import { buildObjectFilter } from "@/lib/objectFilter";
import {
  OBJECT_FACETS,
  OBJECT_FACET_STATUS,
  OBJECT_FACET_TYPE,
  objectFacetsRequest,
} from "@/lib/objectFacets";
import { folderPrefix } from "@/lib/objectFolders";
import {
  OBJECT_ORDER_FIELDS,
  isObjectOrderField,
  type ObjectOrderField,
} from "@/lib/objectOrder";
import type {
  FacetCounting,
  FacetCounts,
  FacetSelection,
} from "@/lib/table/facets";
import { PAGE_TABLE } from "@/lib/table/urlState";

import type { SortState } from "./_view";

/**
 * The page's own query-string parameters, beside TABLE_PARAMS. The status and
 * content-type facets keep theirs under their facet ids (OBJECT_FACETS).
 */
export const OBJECT_LIST_PARAMS = {
  tag: "tag",
  meta: "meta",
  recursive: "recursive",
  folder: "prefix",
} as const;

const TAG = textParam(OBJECT_LIST_PARAMS.tag);
const META = textParam(OBJECT_LIST_PARAMS.meta);
const RECURSIVE = flagParam(OBJECT_LIST_PARAMS.recursive);
const FOLDER = textParam(OBJECT_LIST_PARAMS.folder);
const SORT_IN_URL = { url: PAGE_TABLE, columns: OBJECT_ORDER_FIELDS };

export interface ObjectListState {
  /** The status and content-type choices, by facet id. */
  facetSelection: FacetSelection;
  /** The choices as ListObjectsRequest.facets names them. */
  facets: ReturnType<typeof objectFacetsRequest>;
  /** The facets' toolbar controls, for the counts the listing gave. */
  facetControls: (
    counts: FacetCounts,
    counting: FacetCounting,
  ) => FacetControl[];
  /** Replaces every facet's choices, as a saved view does. */
  setFacetSelection: (selection: FacetSelection) => void;
  tagFilter: string | undefined;
  /** Metadata facet "key=value", URL param `meta`. */
  metaFilter: string | undefined;
  search: string;
  setSearch: (value: string) => void;
  /** List every key under the folder rather than its one level. */
  recursive: boolean;
  setRecursive: (value: boolean) => void;
  /** The folder listed: empty for the root, else ending in the delimiter. */
  prefix: string;
  sort: SortState;
  /** Derived CEL predicate over the object list (search, tag, metadata). */
  filter: string;
  handleSort: (column: string) => void;
  handleTagChange: (value: string | undefined) => void;
  handleMetaChange: (value: string | undefined) => void;
  handleSearchChange: (value: string) => void;
  handleRecursiveChange: (value: boolean) => void;
  handlePrefixChange: (value: string) => void;
}

const unset = (v: string) => v || undefined;

export function useObjectListState(): ObjectListState {
  const {
    selection: facetSelection,
    controls: facetControls,
    choose,
  } = useServerFacets(OBJECT_FACETS, PAGE_TABLE);
  const [tag, setTag] = useUrlState(TAG, "");
  const [meta, setMeta] = useUrlState(META, "");
  const [recursive, setRecursive] = useUrlState(RECURSIVE, false);
  const [folder, setFolder] = useUrlState(FOLDER, "");
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  // A sort the server cannot apply is dropped rather than sent: an old link
  // may still name a column the list once only pretended to sort by.
  const { sort, toggleSort } = useTableSort<ObjectOrderField>(
    undefined,
    SORT_IN_URL,
  );

  // Trails `search`, so a list request follows typing once it settles
  // rather than once per keystroke.
  const debouncedSearch = useDebouncedValue(search);

  const setFacetSelection = useCallback(
    (selection: FacetSelection) => {
      choose(OBJECT_FACET_STATUS, selection[OBJECT_FACET_STATUS] ?? []);
      choose(OBJECT_FACET_TYPE, selection[OBJECT_FACET_TYPE] ?? []);
    },
    [choose],
  );
  // The facets' choices come from a fixed vocabulary, which the URL reading
  // already holds them to: a value it does not offer never reaches the server.
  const facets = useMemo(
    () => objectFacetsRequest(facetSelection),
    [facetSelection],
  );
  const handleSort = (column: string) => {
    if (isObjectOrderField(column)) toggleSort(column);
  };

  // Every value here comes out of the QUERY STRING, so it is attacker-supplied
  // text headed for an expression the server compiles. buildObjectFilter
  // passes every literal through celString; its tests, and this hook's, hold
  // the escaping.
  //
  // What was here once: `state == '${status}'` with no escaping at all, and a
  // `replace(/'/g, …)` that handled the quote and not the backslash. A URL
  // ending its status in a backslash escaped the closing quote; one carrying
  // `x' || true || '` wrote its own conjunct. The blast radius is small — the
  // filter runs inside the caller's own tenant and collection — but a filter
  // nobody can predict is not a filter.
  const filter = buildObjectFilter({
    search: debouncedSearch,
    tag: unset(tag),
    meta: unset(meta),
  });

  return {
    facetSelection,
    facets,
    facetControls,
    setFacetSelection,
    tagFilter: unset(tag),
    metaFilter: unset(meta),
    search,
    setSearch,
    recursive,
    setRecursive,
    prefix: folderPrefix(folder),
    sort: { column: sort.column ?? "", direction: sort.direction },
    filter,
    handleSort,
    handleTagChange: (v) => setTag(v ?? ""),
    handleMetaChange: (v) => setMeta(v ?? ""),
    handleSearchChange: setSearch,
    handleRecursiveChange: setRecursive,
    handlePrefixChange: (v) => setFolder(folderPrefix(v)),
  };
}

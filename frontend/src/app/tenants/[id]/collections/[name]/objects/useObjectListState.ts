// URL-synced filter, folder and sort state for the Collection objects page,
// on the same contract as every other list (useUrlState, TABLE_PARAMS): the
// search, the sort, the status / tag / type / metadata facets, the folder and
// whether to list everything under it. Derives the CEL `filter` the data hook
// consumes. Selection, tag-option accumulation, and the data fetch itself stay
// in the page — they depend on the loaded objects, which this hook never sees.
import { useCallback } from "react";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";

import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { flagParam, textParam, useUrlState } from "@/hooks/useUrlState";
import { buildObjectFilter } from "@/lib/objectFilter";
import { folderPrefix } from "@/lib/objectFolders";
import {
  OBJECT_ORDER_FIELDS,
  isObjectOrderField,
  type ObjectOrderField,
} from "@/lib/objectOrder";
import { PAGE_TABLE } from "@/lib/table/urlState";

import type { SortState } from "./_view";

/** The page's own query-string parameters, beside TABLE_PARAMS. */
export const OBJECT_LIST_PARAMS = {
  status: "status",
  tag: "tag",
  type: "type",
  meta: "meta",
  recursive: "recursive",
  folder: "prefix",
} as const;

const STATUS = textParam(OBJECT_LIST_PARAMS.status);
const TAG = textParam(OBJECT_LIST_PARAMS.tag);
const TYPE = textParam(OBJECT_LIST_PARAMS.type);
const META = textParam(OBJECT_LIST_PARAMS.meta);
const RECURSIVE = flagParam(OBJECT_LIST_PARAMS.recursive);
const FOLDER = textParam(OBJECT_LIST_PARAMS.folder);
const SORT_IN_URL = { url: PAGE_TABLE, columns: OBJECT_ORDER_FIELDS };

export interface ObjectListState {
  status: string | undefined;
  setStatus: (value: string | undefined) => void;
  tagFilter: string | undefined;
  /** Content-type category (CONTENT_TYPE_OPTIONS value), URL param `type`. */
  typeFilter: string | undefined;
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
  /** Derived CEL predicate over the object list (status, search, tag, type,
   *  metadata). */
  filter: string;
  handleSort: (column: string) => void;
  handleStatusChange: (value: string | undefined) => void;
  handleTagChange: (value: string | undefined) => void;
  handleTypeChange: (value: string | undefined) => void;
  handleMetaChange: (value: string | undefined) => void;
  handleSearchChange: (value: string) => void;
  handleRecursiveChange: (value: boolean) => void;
  handlePrefixChange: (value: string) => void;
}

const unset = (v: string) => v || undefined;

export function useObjectListState(): ObjectListState {
  const [status, setStatusText] = useUrlState(STATUS, "");
  const [tag, setTag] = useUrlState(TAG, "");
  const [type, setType] = useUrlState(TYPE, "");
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

  const setStatus = useCallback(
    (value: string | undefined) => setStatusText(value ?? ""),
    [setStatusText],
  );
  const handleSort = (column: string) => {
    if (isObjectOrderField(column)) toggleSort(column);
  };

  // Every value here comes out of the QUERY STRING, so it is attacker-supplied
  // text headed for an expression the server compiles. buildObjectFilter
  // passes every literal through celString and maps the content type through
  // a fixed list; its tests, and this hook's, hold the escaping.
  //
  // What was here once: `state == '${status}'` with no escaping at all, and a
  // `replace(/'/g, …)` that handled the quote and not the backslash. A URL
  // ending its status in a backslash escaped the closing quote; one carrying
  // `x' || true || '` wrote its own conjunct. The blast radius is small — the
  // filter runs inside the caller's own tenant and collection — but a filter
  // nobody can predict is not a filter.
  const filter = buildObjectFilter({
    status: unset(status),
    search: debouncedSearch,
    tag: unset(tag),
    type: unset(type),
    meta: unset(meta),
  });

  return {
    status: unset(status),
    setStatus,
    tagFilter: unset(tag),
    typeFilter: unset(type),
    metaFilter: unset(meta),
    search,
    setSearch,
    recursive,
    setRecursive,
    prefix: folderPrefix(folder),
    sort: { column: sort.column ?? "", direction: sort.direction },
    filter,
    handleSort,
    handleStatusChange: setStatus,
    handleTagChange: (v) => setTag(v ?? ""),
    handleTypeChange: (v) => setType(v ?? ""),
    handleMetaChange: (v) => setMeta(v ?? ""),
    handleSearchChange: setSearch,
    handleRecursiveChange: setRecursive,
    handlePrefixChange: (v) => setFolder(folderPrefix(v)),
  };
}

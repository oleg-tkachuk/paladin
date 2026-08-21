// URL-synced filter + sort state for the Collection objects page, extracted
// from the page component. Owns status / search (with its 300ms debounce) /
// tag-facet / recursive / sort, keeps them mirrored into the query string, and
// derives the CEL `filter` the data hook consumes. Selection, tag-option
// accumulation, and the data fetch itself stay in the page — they depend on the
// loaded objects, which this hook never sees.
import { useCallback, useEffect, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

import { getNextSort, type SortState } from "./_view";

export interface ObjectListState {
  status: string | undefined;
  setStatus: (value: string | undefined) => void;
  tagFilter: string | undefined;
  search: string;
  setSearch: (value: string) => void;
  recursive: boolean;
  setRecursive: (value: boolean) => void;
  sort: SortState;
  /** Derived CEL predicate over the object list (status + search + tag). */
  filter: string;
  handleSort: (column: string) => void;
  handleStatusChange: (value: string | undefined) => void;
  handleTagChange: (value: string | undefined) => void;
  handleSearchChange: (value: string) => void;
  handleRecursiveChange: (value: boolean) => void;
}

export function useObjectListState(): ObjectListState {
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  const [status, setStatus] = useState<string | undefined>(
    searchParams.get("status") || undefined,
  );
  // Tag facet: a single "key=value" pair (URL param `tag`) that becomes a
  // `tags['k'] == 'v'` clause in the CEL filter. The option list is derived
  // client-side from the tags of loaded objects (in the page) — a server-side
  // distinct-tags index would broaden it to the whole tenant, tracked in
  // BACKLOG.
  const [tagFilter, setTagFilter] = useState<string | undefined>(
    searchParams.get("tag") || undefined,
  );
  const [search, setSearch] = useState(searchParams.get("search") || "");
  // Trails `search` by 300ms (see the debounce effect below); feeds
  // the CEL filter so list refetches don't fire per keystroke.
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [recursive, setRecursive] = useState(
    searchParams.get("recursive") === "true",
  );
  const [sort, setSort] = useState<SortState>({
    column: searchParams.get("sort") || "",
    direction: (searchParams.get("dir") as "asc" | "desc") || null,
  });

  const syncToUrl = useCallback(
    (params: Record<string, string | undefined>) => {
      const current = new URLSearchParams(searchParams.toString());
      for (const [key, value] of Object.entries(params)) {
        if (value) current.set(key, value);
        else current.delete(key);
      }
      const qs = current.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [searchParams, pathname, router],
  );

  const handleSort = (column: string) => {
    setSort((prev) => {
      const next = getNextSort(prev, column);
      syncToUrl({
        sort: next.column || undefined,
        dir: next.direction || undefined,
      });
      return next;
    });
  };

  const handleStatusChange = (value: string | undefined) => {
    setStatus(value);
    syncToUrl({ status: value });
  };

  const handleTagChange = (value: string | undefined) => {
    setTagFilter(value);
    syncToUrl({ tag: value });
  };

  const handleSearchChange = (value: string) => {
    setSearch(value);
  };

  // Latest syncToUrl in a ref so the debounce always calls the current
  // one (it closes over the live searchParams) WITHOUT re-arming the
  // timer on every searchParams change. Depending on syncToUrl directly
  // would reset the debounce after each sync; capturing it in the effect
  // would call a stale copy that drops a concurrent filter change made
  // during the 300ms window.
  const syncToUrlRef = useRef(syncToUrl);
  useEffect(() => {
    syncToUrlRef.current = syncToUrl;
  });

  // Debounce the expensive consequences of typing: the visible input
  // updates per keystroke, but the CEL filter (one list RPC per
  // change) and the router.replace URL sync follow 300ms after typing
  // stops. Without this every character ≥3 fired a fetch + a history
  // replacement.
  useEffect(() => {
    const id = setTimeout(() => {
      setDebouncedSearch(search);
      if (search.length > 2 || search === "") {
        syncToUrlRef.current({ search: search || undefined });
      }
    }, 300);
    return () => clearTimeout(id);
  }, [search]);

  const handleRecursiveChange = (value: boolean) => {
    setRecursive(value);
    syncToUrl({ recursive: value ? "true" : undefined });
  };

  const filterParts: string[] = [];
  if (status) filterParts.push(`state == '${status}'`);
  if (debouncedSearch.length > 2)
    filterParts.push(`key.contains('${debouncedSearch.replace(/'/g, "\\'")}')`);
  if (tagFilter) {
    // Split on the FIRST '=' only — tag values may themselves contain '='.
    const eq = tagFilter.indexOf("=");
    if (eq > 0) {
      const esc = (s: string) => s.replace(/'/g, "\\'");
      const k = esc(tagFilter.slice(0, eq));
      const v = esc(tagFilter.slice(eq + 1));
      filterParts.push(`tags['${k}'] == '${v}'`);
    }
  }
  const filter = filterParts.join(" && ");

  return {
    status,
    setStatus,
    tagFilter,
    search,
    setSearch,
    recursive,
    setRecursive,
    sort,
    filter,
    handleSort,
    handleStatusChange,
    handleTagChange,
    handleSearchChange,
    handleRecursiveChange,
  };
}

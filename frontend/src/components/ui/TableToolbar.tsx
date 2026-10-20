// The bar above a table: a search box, any further filters the page brings,
// its facets (FacetFilter) or a set of choices to pick one from, how many rows
// show, and last the
// page's own actions (refresh, create), kept apart from the filters. Controlled and presentational — the page
// owns the query and the filter, whether it filters in the browser
// (useTableView) or on the server, so every table looks and behaves alike.
import type React from "react";
import { MagnifyingGlassIcon, XMarkIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { TablePrefsMenu } from "@/components/ui/TablePrefsMenu";
import { FacetFilter } from "@/components/ui/FacetFilter";
import type { FacetControl } from "@/hooks/useTableFacets";
import { FROSTED, STICKY_BELOW_TOPBAR } from "@/lib/ui/states";

/** One choice of a toolbar filter. */
export interface ToolbarFilterOption<V extends string> {
  value: V;
  label: string;
}

interface TableToolbarProps<V extends string> {
  query: string;
  onQueryChange: (q: string) => void;
  /** Says what the search looks in, e.g. "Search by application or plane…". */
  placeholder: string;
  /** Names the search box for assistive technology. */
  searchLabel: string;
  /** Choices narrowing the rows, one of which is always selected. */
  filter?: {
    label: string;
    options: readonly ToolbarFilterOption<V>[];
    value: V;
    onChange: (v: V) => void;
  };
  /**
   * The list's facets, from useTableFacets: a menu each, its answers counted.
   * A page offers these or `filter`, not both.
   */
  facets?: readonly FacetControl[];
  /** The rows showing and the rows loaded; omitted, no count shows. */
  count?: { shown: number; total: number };
  /** Further filters, such as a select, set between the search and the rest. */
  children?: React.ReactNode;
  /** The page's actions on the list, set at the end of the bar. */
  actions?: React.ReactNode;
  className?: string;
}

export function TableToolbar<V extends string>({
  query,
  onQueryChange,
  placeholder,
  searchLabel,
  filter,
  facets,
  count,
  children,
  actions,
  className,
}: TableToolbarProps<V>) {
  return (
    // Stays in view down a long list, frosted over the rows behind it.
    <div
      className={cn(
        STICKY_BELOW_TOPBAR,
        FROSTED,
        "-mx-1 flex flex-wrap items-center gap-2 px-1 py-2",
        className,
      )}
    >
      <div className="relative min-w-60 flex-1">
        <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          aria-label={searchLabel}
          placeholder={placeholder}
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          // The browser's own clear button would sit beside the one below.
          className="pl-9 pr-8 [&::-webkit-search-cancel-button]:appearance-none"
        />
        {query ? (
          <button
            type="button"
            aria-label="Clear the search"
            onClick={() => onQueryChange("")}
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded text-muted-foreground hover:text-foreground"
          >
            <XMarkIcon className="size-4" />
          </button>
        ) : null}
      </div>
      {children}
      {facets?.map(({ id, ...facet }) => (
        <FacetFilter key={id} {...facet} />
      ))}
      {filter ? (
        <div
          role="group"
          aria-label={filter.label}
          className="flex flex-wrap gap-1"
        >
          {filter.options.map((o) => (
            <Button
              key={o.value}
              type="button"
              size="sm"
              variant={o.value === filter.value ? "secondary" : "ghost"}
              aria-pressed={o.value === filter.value}
              onClick={() => filter.onChange(o.value)}
            >
              {o.label}
            </Button>
          ))}
        </div>
      ) : null}
      {count ? (
        <span
          className="text-xs tabular-nums text-muted-foreground"
          aria-live="polite"
        >
          {count.shown === count.total
            ? `${count.total} shown`
            : `${count.shown} of ${count.total}`}
        </span>
      ) : null}
      <div className="ml-auto flex items-center gap-2">
        {/* The table's own menu, when the list is in a TableScope. */}
        <TablePrefsMenu />
        {actions}
      </div>
    </div>
  );
}

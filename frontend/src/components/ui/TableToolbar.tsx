// The bar above a table: a search box, any further filters the page brings, an
// optional set of choices to pick one from, and how many rows show. Controlled and presentational — the page
// owns the query and the filter, whether it filters in the browser
// (useTableView) or on the server, so every table looks and behaves alike.
import type React from "react";
import { MagnifyingGlassIcon, XMarkIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

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
  /** The rows showing and the rows loaded; omitted, no count shows. */
  count?: { shown: number; total: number };
  /** Further filters, such as a select, set between the search and the rest. */
  children?: React.ReactNode;
  className?: string;
}

export function TableToolbar<V extends string>({
  query,
  onQueryChange,
  placeholder,
  searchLabel,
  filter,
  count,
  children,
  className,
}: TableToolbarProps<V>) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      <div className="relative min-w-60 flex-1">
        <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          aria-label={searchLabel}
          placeholder={placeholder}
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          className="pl-9 pr-8"
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
    </div>
  );
}

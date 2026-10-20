"use client";

// The bar under a paged list, after Argo CD's: how many rows a page holds,
// which rows are on screen out of how many, and the previous and next page.
// Controlled by useTablePage. Inside a TableScope a new size is saved to the
// user's preferences for that table, so the list opens with it next time.
import { ChevronLeftIcon, ChevronRightIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import type { Pagination } from "@/hooks/useTablePage";
import { formatCount } from "@/lib/format/locale";
import { PAGE_SIZES, type PageSize } from "@/lib/table/paging";

import { useTableScope } from "./TableScope";

/** The picker's word for the size that shows every row. */
const ALL_LABEL = "All";

const sizeLabel = (s: PageSize) => (s === "all" ? ALL_LABEL : String(s));

export function TablePagination({
  page,
  pageCount,
  pageSize,
  total,
  first,
  last,
  onPage,
  onPageSize,
}: Pagination) {
  const scope = useTableScope();
  // A list the smallest page holds whole has nothing to page or resize.
  const smallest = PAGE_SIZES[0];
  if (total <= smallest) return null;

  const changeSize = (raw: string) => {
    const size = PAGE_SIZES.find((s) => String(s) === raw);
    if (size === undefined) return;
    onPageSize(size);
    void scope?.setPageSize(size);
  };

  return (
    <nav
      aria-label="Pages"
      className="flex flex-wrap items-center justify-end gap-x-4 gap-y-2 text-sm text-muted-foreground"
    >
      <div className="flex items-center gap-2">
        <span aria-hidden>Rows per page</span>
        <SelectRoot value={String(pageSize)} onValueChange={changeSize}>
          <SelectTrigger size="sm" aria-label="Rows per page">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {PAGE_SIZES.map((s) => (
              <SelectItem key={s} value={String(s)}>
                {sizeLabel(s)}
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
      </div>
      <span className="tabular-nums" aria-live="polite">
        {formatCount(first)}–{formatCount(last)} of {formatCount(total)}
      </span>
      {pageCount > 1 ? (
        <div className="flex items-center gap-1">
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label="Previous page"
            disabled={page <= 1}
            onClick={() => onPage(page - 1)}
          >
            <ChevronLeftIcon className="size-4" />
          </Button>
          <span className="px-1 tabular-nums">
            Page {page} of {pageCount}
          </span>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label="Next page"
            disabled={page >= pageCount}
            onClick={() => onPage(page + 1)}
          >
            <ChevronRightIcon className="size-4" />
          </Button>
        </div>
      ) : null}
    </nav>
  );
}

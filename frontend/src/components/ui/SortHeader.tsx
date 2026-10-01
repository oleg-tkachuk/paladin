// A sortable column header: the label, and an arrow saying whether the column
// is the one sorted on and in which direction. Presentational — the sort state
// comes in through props and a click hands the column back to the page.
import type React from "react";
import {
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
} from "@heroicons/react/24/outline";

import { TableHead } from "@/components/ui/table";
import type { SortState } from "@/hooks/useTableSort";
import { cn } from "@/lib/utils";

interface SortHeaderProps<C extends string> {
  label: string;
  column: C;
  current: SortState<C>;
  onSort: (c: C) => void;
  /** "end" for a right-aligned (numeric) column. */
  align?: "start" | "end";
}

const isSortedOn = <C extends string>(current: SortState<C>, column: C) =>
  current.column === column && current.direction !== null;

/**
 * A table header cell that sorts. The arrow shows sighted users the order;
 * aria-sort, which assistive technology reads from the cell rather than from
 * the button inside it, says the same to everyone else.
 */
export function SortableHead<C extends string>({
  className,
  after,
  ...props
}: SortHeaderProps<C> & {
  className?: string;
  /** A control that sits beside the header, inside the same cell. */
  after?: React.ReactNode;
}) {
  const ariaSort = !isSortedOn(props.current, props.column)
    ? "none"
    : props.current.direction === "asc"
      ? "ascending"
      : "descending";
  return (
    <TableHead className={className} aria-sort={ariaSort}>
      {after ? (
        <div className="flex items-center gap-1">
          <SortHeader {...props} />
          {after}
        </div>
      ) : (
        <SortHeader {...props} />
      )}
    </TableHead>
  );
}

function SortHeader<C extends string>({
  label,
  column,
  current,
  onSort,
  align = "start",
}: SortHeaderProps<C>) {
  const active = isSortedOn(current, column);
  const Icon = !active
    ? ArrowsUpDownIcon
    : current.direction === "asc"
      ? ArrowUpIcon
      : ArrowDownIcon;
  return (
    <button
      type="button"
      onClick={() => onSort(column)}
      className={cn(
        "inline-flex items-center gap-1 text-xs font-medium uppercase tracking-wider transition-colors",
        active
          ? "text-foreground"
          : "text-muted-foreground hover:text-foreground",
        align === "end" && "justify-end w-full",
      )}
    >
      {label}
      <Icon className="size-3.5 opacity-70" />
    </button>
  );
}

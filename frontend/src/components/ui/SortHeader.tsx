// A sortable column header: the label, and an arrow saying whether the column
// is the one sorted on and in which direction. Presentational — the sort state
// comes in through props and a click hands the column back to the page.
import {
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
} from "@heroicons/react/24/outline";

import type { SortState } from "@/hooks/useTableSort";
import { cn } from "@/lib/utils";

export function SortHeader<C extends string>({
  label,
  column,
  current,
  onSort,
  align = "start",
}: {
  label: string;
  column: C;
  current: SortState<C>;
  onSort: (c: C) => void;
  /** "end" for a right-aligned (numeric) column. */
  align?: "start" | "end";
}) {
  const active = current.column === column && current.direction !== null;
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

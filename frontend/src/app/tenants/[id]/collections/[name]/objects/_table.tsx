// Sortable column header for the Collection objects table, extracted from the
// page. Presentational — the active column + direction come in via props and
// clicks delegate back to the page's sort handler.
import {
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
} from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";
import type { SortState } from "./_view";

export function SortHeader({
  label,
  column,
  current,
  onSort,
  align = "start",
}: {
  label: string;
  column: string;
  current: SortState;
  onSort: (c: string) => void;
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

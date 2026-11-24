"use client";

import { SortableHead } from "@/components/ui/SortHeader";
import type { SortState } from "@/hooks/useTableSort";
import { TableCell } from "@/components/ui/table";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { formatTimestampUTC } from "@/lib/format/timestamp";
import { USAGE_SORT_COLUMNS, type UsageSortColumn } from "@/lib/collectionSort";
import { formatCount } from "@/lib/format/locale";
import { cn, formatBytes, formatDate, timestampToDate } from "@/lib/utils";
import type { TableColumns } from "@/lib/table/columns";

/** How many columns UsageHeads adds, for a full-width row's colSpan. */
export const USAGE_COLUMNS = USAGE_SORT_COLUMNS.length;

const NONE = "—";

/**
 * The usage columns, which their heads and an export both read. An export
 * writes the counts as plain numbers — the size in bytes — so a spreadsheet
 * can sum them, and the last upload as an absolute time.
 */
export const USAGE_TABLE_COLUMNS: TableColumns<Collection, UsageSortColumn> = {
  objects: {
    header: "Objects",
    text: (c) => String(c.usage?.objectCount ?? 0),
  },
  size: { header: "Size", text: (c) => String(c.usage?.totalBytes ?? 0) },
  lastUpload: {
    header: "Last upload",
    text: (c) =>
      c.usage?.lastCommittedAt
        ? formatTimestampUTC(c.usage.lastCommittedAt)
        : NONE,
  },
};

/** The usage columns' heads, hidden where the table is too narrow for them. */
export function UsageHeads<C extends string>({
  current,
  onSort,
}: {
  current: SortState<C | UsageSortColumn>;
  onSort: (column: C | UsageSortColumn) => void;
}) {
  const props = { current, onSort };
  return (
    <>
      <SortableHead
        label={USAGE_TABLE_COLUMNS.objects.header}
        column="objects"
        align="end"
        className="hidden @4xl:table-cell"
        {...props}
      />
      <SortableHead
        label={USAGE_TABLE_COLUMNS.size.header}
        column="size"
        align="end"
        className="hidden @4xl:table-cell"
        {...props}
      />
      <SortableHead
        label={USAGE_TABLE_COLUMNS.lastUpload.header}
        column="lastUpload"
        className="hidden @5xl:table-cell"
        {...props}
      />
    </>
  );
}

/** A collection's usage cells: its available objects, their size, the newest. */
export function UsageCells({ collection }: { collection: Collection }) {
  const u = collection.usage;
  const count = Number(u?.objectCount ?? 0);
  const muted = count === 0 && "text-muted-foreground";
  return (
    <>
      <TableCell numeric className={cn("hidden @4xl:table-cell", muted)}>
        {formatCount(count)}
      </TableCell>
      <TableCell numeric className={cn("hidden @4xl:table-cell", muted)}>
        {count === 0 ? NONE : formatBytes(u?.totalBytes ?? 0)}
      </TableCell>
      <TableCell
        className={cn("hidden whitespace-nowrap @5xl:table-cell", muted)}
      >
        {u?.lastCommittedAt ? (
          <time title={formatTimestampUTC(u.lastCommittedAt)}>
            {formatDate(timestampToDate(u.lastCommittedAt))}
          </time>
        ) : (
          NONE
        )}
      </TableCell>
    </>
  );
}

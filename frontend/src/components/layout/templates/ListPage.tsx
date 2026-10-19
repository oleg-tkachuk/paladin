"use client";

// The frame every list page fills: its header, the toolbar that searches
// and filters it, the table in a card, and what follows the table (Load
// more). ListBody holds what the table says before it has rows: loading,
// a failed read, or none.
import type React from "react";

import { Card } from "@/components/ui/Card";
import {
  EmptyTableRow,
  type EmptyStateProps,
} from "@/components/ui/EmptyState";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { TableBody, TableCell, TableRow } from "@/components/ui/table";

/** Gap between the parts of a page: header, toolbar, table, footer. */
export const PAGE_STACK = "space-y-6";

/** Rows a table shows while its first read is in flight. */
export const SKELETON_ROWS = 3;

export function ListPage({
  header,
  toolbar,
  footer,
  children,
}: {
  /** The page's PageHeader, or a tenant tab's own heading. */
  header: React.ReactNode;
  /** The TableToolbar: search, filter, count, actions. */
  toolbar?: React.ReactNode;
  /** After the table: Load more, a truncation note. */
  footer?: React.ReactNode;
  /** The Table. */
  children: React.ReactNode;
}) {
  return (
    <div className={PAGE_STACK}>
      {header}
      {toolbar}
      <Card className="p-0">{children}</Card>
      {footer}
    </div>
  );
}

/** A read that failed, said where the rows would have been. */
export interface ListError {
  /** The rows, plural: "Users". */
  what: string;
  reason: string;
  onRetry: () => void;
}

/**
 * A table's body in the state it is in: skeleton rows on the first read, the
 * failure in place of the rows, the empty state when there are none, or the
 * rows. A failed list never reads as an empty one.
 */
export function ListBody({
  colSpan,
  loading,
  error,
  empty,
  children,
}: {
  /** Columns a full-width row spans, hidden ones included. */
  colSpan: number;
  /** The first read is in flight and there is nothing to show yet. */
  loading: boolean;
  error: ListError | null;
  /** Set when no row is shown: why, and what makes the first. */
  empty: EmptyStateProps | null;
  children: React.ReactNode;
}) {
  return (
    <TableBody>
      {loading ? (
        Array.from({ length: SKELETON_ROWS }, (_, i) => (
          <TableRow key={`skeleton-${i}`}>
            <TableCell colSpan={colSpan} className="py-3">
              <Skeleton className="h-7 w-full" />
            </TableCell>
          </TableRow>
        ))
      ) : error ? (
        <TableRow>
          <TableCell colSpan={colSpan} className="h-32 text-center">
            <ListLoadError {...error} />
          </TableCell>
        </TableRow>
      ) : empty ? (
        <EmptyTableRow colSpan={colSpan} {...empty} />
      ) : (
        children
      )}
    </TableBody>
  );
}

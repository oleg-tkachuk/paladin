"use client";

// What a list shows when it has nothing: why (emptyReason), what the thing
// is for when there are none at all, and the action that makes the first
// one. A list that failed to load shows ListLoadError instead — an empty
// state for a failed read invites creating what may already exist.
import type React from "react";
import { PlusIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { TableCell, TableRow } from "@/components/ui/table";
import { emptyMessage, type EmptyReason } from "@/lib/emptyState";

export interface EmptyAction {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  /** Why it is disabled, as a tooltip. */
  title?: string;
}

export interface EmptyStateProps {
  icon: React.ElementType;
  /** The rows, plural and lower case: "buckets". */
  what: string;
  reason: EmptyReason;
  /** One line on what the rows are for, shown only when there are none. */
  purpose?: string;
  /** Makes the first one; offered only when there are none. */
  action?: EmptyAction;
}

export function EmptyState({
  icon: Icon,
  what,
  reason,
  purpose,
  action,
}: EmptyStateProps) {
  const none = reason === "empty";
  return (
    <div
      className="flex flex-col items-center gap-3 text-muted-foreground"
      data-empty-reason={reason}
    >
      <Icon className="size-10 opacity-40" />
      <p className="text-sm">{emptyMessage(reason, what)}</p>
      {none && purpose ? <p className="max-w-md text-xs">{purpose}</p> : null}
      {none && action ? (
        <Button
          size="sm"
          variant="outline"
          onClick={action.onClick}
          disabled={action.disabled}
          title={action.title}
        >
          <PlusIcon className="size-4" />
          {action.label}
        </Button>
      ) : null}
    </div>
  );
}

/** The same, as the one row of an empty table body. */
export function EmptyTableRow({
  colSpan,
  ...props
}: EmptyStateProps & { colSpan: number }) {
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="h-48 text-center">
        <EmptyState {...props} />
      </TableCell>
    </TableRow>
  );
}

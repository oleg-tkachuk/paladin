"use client";

// The objects table — header (select-all, sortable columns, relative-time
// toggle), the ObjectTableRow body, the empty/error states, and the load-more
// footer. Extracted from the page, which keeps the data + selection + dialog
// state and threads the row callbacks through `rowProps`.
import { EmptyState } from "@/components/ui/EmptyState";
import { emptyReason } from "@/lib/emptyState";
import type { ComponentProps } from "react";
import {
  ClockIcon,
  DocumentIcon,
  ExclamationTriangleIcon,
  FolderIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { ObjectTableRow } from "@/components/features/objects/ObjectTableRow";
import { OBJECT_COLUMN_CLASS } from "@/components/features/objects/columns";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";

import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { OBJECT_ORDER } from "@/lib/objectOrder";
import type { SortState } from "./_view";
import { folderName } from "@/lib/objectFolders";

/** The folders a level of the list shows above its objects. */
export interface FolderRows {
  /** The level listed; folder names are read relative to it. */
  prefix: string;
  /** Each folder's full prefix, as the server answers it. */
  list: readonly string[];
  /** More folders exist than the server returned. */
  truncated: boolean;
  onOpen: (prefix: string) => void;
}

/** Spans the columns after the checkbox, whichever are shown. */
const FOLDER_ROW_SPAN = 8;

// Per-row callbacks forwarded verbatim to ObjectTableRow. Derived from its own
// prop type so the contract can't drift — the page supplies the closures (they
// capture its selection + dialog setters and the loaded `objects`).
type RowProps = ComponentProps<typeof ObjectTableRow>;
export type ObjectRowCallbacks = Pick<
  RowProps,
  | "onToggleSelect"
  | "onInspect"
  | "onStartInlineEdit"
  | "onSaveInlineLabels"
  | "onCancelInlineEdit"
  | "onEditLabelsValueChange"
  | "onCopyToClipboard"
  | "onSoftDelete"
  | "onHardDelete"
  | "onCopy"
  | "onMove"
  | "onGenerateDownloadUrl"
>;

export function ObjectsTable({
  objects,
  loading,
  error,
  nextCursor,
  onLoadMore,
  onRefresh,
  selectedIds,
  allChecked,
  someChecked,
  onToggleSelectAll,
  sort,
  onSort,
  visibleColumns,
  useRelativeTime,
  onToggleRelativeTime,
  selectedInspectorKey,
  editingLabelsId,
  editLabelsValue,
  search,
  narrowed,
  rowProps,
  folders,
}: {
  objects: Object$[];
  loading: boolean;
  error: Error | null;
  nextCursor: string | undefined;
  onLoadMore: () => void;
  onRefresh: () => void;
  selectedIds: Set<string>;
  allChecked: boolean;
  someChecked: boolean;
  onToggleSelectAll: () => void;
  sort: SortState;
  onSort: (column: string) => void;
  visibleColumns: Set<string>;
  useRelativeTime: boolean;
  onToggleRelativeTime: () => void;
  selectedInspectorKey: string | null;
  editingLabelsId: string | null;
  editLabelsValue: string;
  search: string;
  /** A facet or filter narrows the listing, so an empty one is filtered. */
  narrowed: boolean;
  rowProps: ObjectRowCallbacks;
  folders?: FolderRows;
}) {
  const folderList = folders?.list ?? [];
  return (
    <Card className="p-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-12">
              <Checkbox
                checked={someChecked ? "indeterminate" : allChecked}
                onCheckedChange={onToggleSelectAll}
                aria-label="Select all rows"
              />
            </TableHead>
            {visibleColumns.has("key") && (
              <SortableHead
                label="Name / ID"
                column={OBJECT_ORDER.key}
                current={sort}
                onSort={onSort}
              />
            )}
            {/* Tags are a map, so there is no one value to sort them by. */}
            {visibleColumns.has("object_tag") && (
              <StaticHead className={OBJECT_COLUMN_CLASS.object_tag}>
                Object Tags
              </StaticHead>
            )}
            {visibleColumns.has("mime") && (
              <SortableHead
                className={OBJECT_COLUMN_CLASS.mime}
                label="MIME"
                column={OBJECT_ORDER.contentType}
                current={sort}
                onSort={onSort}
              />
            )}
            {visibleColumns.has("size") && (
              <SortableHead
                className={cn(OBJECT_COLUMN_CLASS.size, "text-right")}
                label="Size"
                column={OBJECT_ORDER.size}
                current={sort}
                onSort={onSort}
                align="end"
              />
            )}
            {visibleColumns.has("status") && (
              <SortableHead
                label="State"
                column={OBJECT_ORDER.state}
                current={sort}
                onSort={onSort}
              />
            )}
            {visibleColumns.has("created") && (
              <SortableHead
                className={OBJECT_COLUMN_CLASS.created}
                label="Created"
                column={OBJECT_ORDER.createdAt}
                current={sort}
                onSort={onSort}
                after={
                  <button
                    type="button"
                    onClick={onToggleRelativeTime}
                    className="text-muted-foreground hover:text-foreground"
                    title="Toggle timestamp format"
                  >
                    <ClockIcon className="size-3" />
                  </button>
                }
              />
            )}
            {visibleColumns.has("actions") && (
              <TableHead className="text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {folders &&
            folderList.map((f) => {
              const name = folderName(f, folders.prefix);
              return (
                <TableRow key={f}>
                  <TableCell />
                  <TableCell colSpan={FOLDER_ROW_SPAN}>
                    <button
                      type="button"
                      onClick={() => folders.onOpen(f)}
                      aria-label={`Open folder ${name}`}
                      className="flex items-center gap-2 font-medium hover:text-primary"
                    >
                      <FolderIcon className="size-5 text-muted-foreground" />
                      {name}/
                    </button>
                  </TableCell>
                </TableRow>
              );
            })}
          {folders?.truncated ? (
            <TableRow>
              <TableCell />
              <TableCell
                colSpan={FOLDER_ROW_SPAN}
                className="text-xs text-muted-foreground"
              >
                More folders exist than are shown; search to narrow them.
              </TableCell>
            </TableRow>
          ) : null}
          {objects.map((obj) => (
            <ObjectTableRow
              key={obj.objectId}
              obj={obj}
              isSelected={selectedIds.has(obj.objectId)}
              isInspected={selectedInspectorKey === obj.key}
              isEditingLabels={editingLabelsId === obj.objectId}
              editLabelsValue={editLabelsValue}
              useRelativeTime={useRelativeTime}
              visibleColumns={visibleColumns}
              {...rowProps}
            />
          ))}

          {objects.length === 0 && folderList.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={9} className="h-56 text-center">
                {error ? (
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <ExclamationTriangleIcon className="size-10 text-destructive opacity-70" />
                    <p className="text-sm font-medium text-destructive">
                      Failed to load objects
                    </p>
                    <p className="max-w-md text-xs">{error.message}</p>
                    <Button size="sm" variant="outline" onClick={onRefresh}>
                      Retry
                    </Button>
                  </div>
                ) : (
                  <EmptyState
                    icon={DocumentIcon}
                    what="objects"
                    reason={emptyReason({ filtered: !!search || narrowed })}
                    purpose="Drop files on the list, or use Upload, to add the first."
                  />
                )}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      {nextCursor && (
        <div className="border-t p-3 text-center">
          <Button
            size="sm"
            variant="ghost"
            onClick={onLoadMore}
            disabled={loading}
            className="text-muted-foreground"
          >
            {loading ? "Syncing…" : "Load more objects"}
          </Button>
        </div>
      )}
    </Card>
  );
}

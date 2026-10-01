"use client";

// The objects table — header (select-all, sortable columns, relative-time
// toggle), the ObjectTableRow body, the empty/error states, and the load-more
// footer. Extracted from the page, which keeps the data + selection + dialog
// state and threads the row callbacks through `rowProps`.
import type { ComponentProps } from "react";
import {
  ClockIcon,
  DocumentIcon,
  ExclamationTriangleIcon,
  MagnifyingGlassIcon,
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
import { ObjectTableRow } from "@/components/features/objects/ObjectTableRow";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";

import { SortableHead } from "@/components/ui/SortHeader";
import type { SortState } from "./_view";

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
  status,
  rowProps,
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
  status: string | undefined;
  rowProps: ObjectRowCallbacks;
}) {
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
                column="key"
                current={sort}
                onSort={onSort}
              />
            )}
            {visibleColumns.has("object_tag") && (
              <SortableHead
                label="Object Tags"
                column="object_tag"
                current={sort}
                onSort={onSort}
              />
            )}
            {visibleColumns.has("mime") && (
              <SortableHead
                className="hidden lg:table-cell"
                label="MIME"
                column="content_type"
                current={sort}
                onSort={onSort}
              />
            )}
            {visibleColumns.has("size") && (
              <SortableHead
                className="hidden md:table-cell text-right"
                label="Size"
                column="size_bytes"
                current={sort}
                onSort={onSort}
                align="end"
              />
            )}
            {visibleColumns.has("status") && (
              <SortableHead
                label="State"
                column="status"
                current={sort}
                onSort={onSort}
              />
            )}
            {visibleColumns.has("created") && (
              <SortableHead
                className="hidden sm:table-cell"
                label="Created"
                column="created_at"
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

          {objects.length === 0 && !loading && (
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
                ) : search || status ? (
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <MagnifyingGlassIcon className="size-10 opacity-40" />
                    <p className="text-sm">No matches found.</p>
                    <p className="text-xs">
                      Try adjusting your search or filters.
                    </p>
                  </div>
                ) : (
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <DocumentIcon className="size-10 opacity-40" />
                    <p className="text-sm">No objects yet.</p>
                    <p className="text-xs">
                      Upload your first object to get started.
                    </p>
                  </div>
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

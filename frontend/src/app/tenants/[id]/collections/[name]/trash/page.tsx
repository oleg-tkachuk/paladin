"use client";

// Soft-deleted objects under this Collection — moved from the
// legacy flat /trash route. Collection scope is fixed by URL via
// the parent layout's context, so the picker is gone; everything
// else (search, bulk restore, bulk purge, single purge) carries
// over verbatim. Every restore and purge is confirmed first.

import { CopyableId } from "@/components/ui/CopyableId";
import React, { useEffect, useState } from "react";
import {
  ArrowUturnLeftIcon,
  ClockIcon,
  DocumentIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useObjects } from "@/hooks/useObjects";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import {
  OBJECT_ORDER,
  OBJECT_ORDER_FIELDS,
  type ObjectOrderField,
} from "@/lib/objectOrder";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import { buildObjectFilter } from "@/lib/objectFilter";
import { ObjectTagBadge } from "@/components/features/ObjectTagBadge";
import { cn, formatDate, timestampToDate } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { Button } from "@/components/ui/button";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { Label } from "@/components/ui/label";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import {
  TooltipRoot as Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/Tooltip";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ConfirmModal, type Affected } from "@/components/ui/ConfirmModal";
import { SelectionBar } from "@/components/ui/SelectionBar";
import { Skeleton } from "@/components/ui/Skeleton";
import { openableRow } from "@/components/ui/RecordDialog";
import { useDetailPanel } from "@/hooks/useDetailPanel";

import { useCollection } from "../collection-context";
import { useTenantChangesBlocked } from "../../../tenant-context";
import { formatDateTime } from "@/lib/format/locale";
import { Bytes } from "@/components/ui/Bytes";

import {
  DEFAULT_CONTENT_TYPE,
  TrashedObjectPanel,
  objectBaseName,
  useTrashedObject,
} from "./TrashedObjectPanel";

/** The state a trashed object is in. */
const TRASHED_STATE = "DELETED";
/** How long the search waits after a keystroke before asking the server. */
const SEARCH_DEBOUNCE_MS = 300;

export default function CollectionTrashPage() {
  const { collection: collectionResource } = useCollection();
  const changesBlocked = useTenantChangesBlocked();
  const collection = collectionResource.collection;

  // The search is the server's, over every trashed object, and the list
  // pages on: a search over the first fifty rows missed the rest, and the
  // rest were not listed at all.
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [search]);

  // Sorted on the server: the trash loads a page at a time, so a sort in the
  // browser would order only what has loaded. A new sort starts the list over.
  const { sort, toggleSort } = useTableSort<ObjectOrderField>(undefined, {
    url: PAGE_TABLE,
    columns: OBJECT_ORDER_FIELDS,
  });
  const sortProps = { current: sort, onSort: toggleSort };

  const {
    objects,
    loading,
    loadMore,
    restoreObject,
    purgeObject,
    bulkDeleteObjects,
    bulkRestoreObjects,
  } = useObjects({
    collection,
    filter: buildObjectFilter({
      status: TRASHED_STATE,
      search: debouncedSearch,
    }),
    orderBy: sort.column ?? undefined,
    sortDirection: sort.direction,
  });
  const [useRelativeTime, setUseRelativeTime] = useState(true);
  const [busyId, setBusyId] = useState<string | null>(null);
  // The open object is in the URL, so its address links to it. The row
  // clicked stays at hand for one the list no longer holds, and a linked one
  // on a page not loaded yet is read by id.
  const detail = useDetailPanel();
  const [clicked, setClicked] = useState<Object$ | null>(null);
  const listed =
    objects.find((o) => o.objectId === detail.openId) ??
    (clicked && clicked.objectId === detail.openId ? clicked : null);
  const opened = useTrashedObject(
    collectionResource.name,
    detail.openId,
    listed,
  );
  // The action awaiting the operator's confirmation: restore and purge, of one
  // row or of the selection, are all asked first.
  const [pending, setPending] = useState<{
    kind: "restore" | "purge";
    ids: string[];
    // A selection goes through the batch calls, even when it holds one row.
    selection: boolean;
  } | null>(null);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const filtered = objects;

  const allSelected = selected.size > 0 && selected.size === filtered.length;
  const partiallySelected = selected.size > 0 && !allSelected;

  const toggleAll = () => {
    if (allSelected || selected.size > 0) {
      setSelected(new Set());
    } else {
      setSelected(new Set(filtered.map((o) => o.objectId)));
    }
  };

  const toggleOne = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };

  // The panel's object counts too: it may be on a page not loaded.
  const known = (id: string) =>
    objects.find((o) => o.objectId === id) ??
    (opened.object?.objectId === id ? opened.object : undefined);
  const itemsFor = (ids: string[]) =>
    ids.map(known).filter((o): o is NonNullable<typeof o> => !!o);

  const runPending = async () => {
    if (!pending) return;
    const { kind, ids, selection } = pending;
    const items = itemsFor(ids);
    if (items.length === 0) return;
    const single = !selection;
    if (single) setBusyId(ids[0]);
    else setBulkBusy(true);
    try {
      if (single) {
        if (kind === "restore") await restoreObject(items[0]);
        else await purgeObject(items[0]);
        // The object has left the trash, so its panel has nothing to show.
        if (ids[0] === detail.openId) detail.close();
      } else {
        const refs = items.map((o) => ({
          name: o.name,
          collection: o.collection,
        }));
        if (kind === "restore") await bulkRestoreObjects(refs);
        else await bulkDeleteObjects(refs, { permanent: true });
        setSelected(new Set());
      }
    } finally {
      setBusyId(null);
      setBulkBusy(false);
    }
  };

  const pendingCount = pending?.ids.length ?? 0;
  const pendingName =
    pendingCount === 1
      ? (itemsFor(pending!.ids)[0]?.key ?? pending!.ids[0])
      : null;
  // A selection is listed by key, so the operator reads which objects it
  // holds rather than how many; one row is named in the message already.
  const pendingAffected: Affected | undefined =
    pending && pendingCount > 1
      ? {
          heading: `${pendingCount} objects`,
          items: itemsFor(pending.ids).map((o) => o.key),
          total: pendingCount,
        }
      : undefined;
  const pendingSubject = pendingName ? (
    <span className="font-mono text-foreground break-all">{pendingName}</span>
  ) : (
    <>
      {pendingCount} {pendingCount === 1 ? "object" : "objects"}
    </>
  );

  return (
    <div className="space-y-4">
      {/* Scope strip — fixed by URL. Mirrors the strip on the
          Objects tab so operators see the same anchor either way. */}
      <div className="flex items-center justify-between rounded-lg border bg-card/40 p-3">
        <div className="space-y-0.5">
          <Label className={T.label}>Collection</Label>
          <div className="flex items-center gap-2">
            <Badge variant="info" className={T.code}>
              {collection}
            </Badge>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          Listing soft-deleted objects in this namespace.
        </p>
      </div>

      <TableToolbar
        query={search}
        onQueryChange={setSearch}
        searchLabel="Search the trash"
        placeholder="Search by key…"
      >
        <Badge variant="secondary" className="font-mono tabular-nums">
          {objects.length}
          {loadMore ? "+" : ""} deleted
        </Badge>
      </TableToolbar>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">
                <Checkbox
                  checked={partiallySelected ? "indeterminate" : allSelected}
                  onCheckedChange={toggleAll}
                  aria-label="Select all"
                />
              </TableHead>
              <SortableHead
                label="Name"
                column={OBJECT_ORDER.key}
                {...sortProps}
              />
              <StaticHead className="hidden @md:table-cell">Tag</StaticHead>
              <SortableHead
                label="MIME"
                column={OBJECT_ORDER.contentType}
                className="hidden @2xl:table-cell"
                {...sortProps}
              />
              <SortableHead
                label="Size"
                column={OBJECT_ORDER.size}
                className="hidden @md:table-cell text-right"
                align="end"
                {...sortProps}
              />
              <SortableHead
                label="Deleted at"
                column={OBJECT_ORDER.terminatedAt}
                className="hidden @xs:table-cell"
                {...sortProps}
                after={
                  <button
                    type="button"
                    aria-label="Toggle relative time"
                    onClick={() => setUseRelativeTime((v) => !v)}
                    className="text-muted-foreground hover:text-foreground"
                  >
                    <ClockIcon className="size-3" />
                  </button>
                }
              />
              <TableHead numeric className="hidden @2xl:table-cell">
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span className="cursor-help border-b border-dotted border-muted-foreground/50">
                      Version
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>
                    The OCC guard sent with Restore. If it has moved on since
                    this list loaded, the restore is refused rather than
                    overwriting a concurrent change.
                  </TooltipContent>
                </Tooltip>
              </TableHead>
              <TableHead className="text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && objects.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={8} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={8} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <TrashIcon className="size-10 opacity-40" />
                    <p className="text-sm">Trash is empty.</p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((obj) => {
                const row = openableRow(() => {
                  setClicked(obj);
                  detail.open(obj.objectId);
                });
                return (
                  <TableRow
                    key={obj.objectId}
                    {...row}
                    {...detail.rowProps(obj.objectId)}
                    className={cn(row.className, "group")}
                    data-state={
                      selected.has(obj.objectId) ? "selected" : undefined
                    }
                  >
                    <TableCell>
                      <Checkbox
                        checked={selected.has(obj.objectId)}
                        onCheckedChange={() => toggleOne(obj.objectId)}
                        aria-label={`Select ${obj.key}`}
                      />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-3">
                        <div className="flex size-8 items-center justify-center rounded-md bg-destructive/15 text-destructive ring-1 ring-destructive/30">
                          <DocumentIcon className="size-4" />
                        </div>
                        <div className="min-w-0">
                          <div className="truncate font-medium">
                            {objectBaseName(obj.key)}
                          </div>
                          <CopyableId
                            value={obj.objectId}
                            label="object ID"
                            className="text-muted-foreground"
                          />
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="hidden @md:table-cell">
                      <ObjectTagBadge
                        objectTag={obj.tags?.object_tag}
                        linked={!!obj.tags?.object_tag}
                      />
                    </TableCell>
                    <TableCell className="hidden @2xl:table-cell text-xs text-muted-foreground font-mono">
                      {obj.contentType || DEFAULT_CONTENT_TYPE}
                    </TableCell>
                    <TableCell
                      numeric
                      className="hidden @md:table-cell text-xs font-mono"
                    >
                      <Bytes value={obj.sizeBytes} />
                    </TableCell>
                    <TableCell className="hidden @xs:table-cell text-xs text-muted-foreground whitespace-nowrap">
                      {obj.terminatedAt
                        ? useRelativeTime
                          ? formatDate(timestampToDate(obj.terminatedAt))
                          : formatDateTime(timestampToDate(obj.terminatedAt))
                        : "—"}
                    </TableCell>
                    <TableCell
                      numeric
                      className="hidden @2xl:table-cell text-xs font-mono text-muted-foreground"
                    >
                      {obj.resourceVersion || "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1.5">
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={Boolean(changesBlocked) || !!busyId}
                              title={changesBlocked ?? undefined}
                              onClick={() =>
                                setPending({
                                  kind: "restore",
                                  ids: [obj.objectId],
                                  selection: false,
                                })
                              }
                            >
                              <ArrowUturnLeftIcon className="size-3.5" />
                              Restore
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>
                            Move back to the active list.
                          </TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-8 text-muted-foreground hover:text-destructive hover:bg-destructive/10"
                              disabled={Boolean(changesBlocked) || !!busyId}
                              title={changesBlocked ?? undefined}
                              onClick={() =>
                                setPending({
                                  kind: "purge",
                                  ids: [obj.objectId],
                                  selection: false,
                                })
                              }
                              aria-label={`Purge ${obj.key} from storage`}
                            >
                              <TrashIcon className="size-4" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>
                            Permanently purge from storage.
                          </TooltipContent>
                        </Tooltip>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {loadMore && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            size="sm"
            onClick={() => loadMore()}
            disabled={loading}
          >
            Load more
          </Button>
        </div>
      )}

      <SelectionBar
        count={selected.size}
        onClear={() => setSelected(new Set())}
      >
        <Button
          size="sm"
          variant="ghost"
          disabled={Boolean(changesBlocked) || bulkBusy}
          title={changesBlocked ?? undefined}
          onClick={() =>
            setPending({
              kind: "restore",
              ids: Array.from(selected),
              selection: true,
            })
          }
        >
          <ArrowUturnLeftIcon />
          Restore
        </Button>
        <Button
          size="sm"
          variant="ghost"
          className="text-destructive hover:bg-destructive/10 hover:text-destructive"
          disabled={Boolean(changesBlocked) || bulkBusy}
          title={changesBlocked ?? undefined}
          onClick={() =>
            setPending({
              kind: "purge",
              ids: Array.from(selected),
              selection: true,
            })
          }
        >
          <TrashIcon />
          Purge
        </Button>
      </SelectionBar>

      <TrashedObjectPanel
        id={detail.openId}
        object={opened.object}
        loading={opened.loading}
        failed={opened.failed}
        actionsDisabled={Boolean(changesBlocked) || !!busyId}
        disabledReason={changesBlocked ?? undefined}
        onClose={detail.close}
        onRestore={(o) =>
          setPending({ kind: "restore", ids: [o.objectId], selection: false })
        }
        onPurge={(o) =>
          setPending({ kind: "purge", ids: [o.objectId], selection: false })
        }
      />

      <ConfirmModal
        isOpen={pending?.kind === "restore"}
        onClose={() => setPending(null)}
        onConfirm={runPending}
        loading={!!busyId || bulkBusy}
        type="warning"
        title={
          pendingCount === 1
            ? "Restore this object?"
            : `Restore ${pendingCount} objects?`
        }
        message={
          <p>
            {pendingSubject} {pendingCount === 1 ? "returns" : "return"} to the
            collection&apos;s active objects and can be read again.
          </p>
        }
        warning="A restore is refused for an object that changed after this list loaded; refresh and try again."
        affected={pendingAffected}
        confirmText={
          pendingCount === 1
            ? "Restore object"
            : `Restore ${pendingCount} objects`
        }
      />

      <ConfirmModal
        isOpen={pending?.kind === "purge"}
        onClose={() => setPending(null)}
        onConfirm={runPending}
        loading={!!busyId || bulkBusy}
        type="danger"
        title={
          pendingCount === 1
            ? "Permanently purge this object?"
            : `Permanently purge ${pendingCount} objects?`
        }
        message={
          <p>
            {pendingSubject} {pendingCount === 1 ? "is" : "are"} removed from
            the storage backend.
          </p>
        }
        warning="This cannot be undone: a purged object cannot be restored."
        affected={pendingAffected}
        // A purge destroys the bytes, so it is typed out; as the verb, since
        // a key can run to a long path and a selection has no one name.
        typeToConfirm="purge"
        confirmText={
          pendingCount === 1
            ? "Purge permanently"
            : `Purge ${pendingCount} objects`
        }
      />
    </div>
  );
}

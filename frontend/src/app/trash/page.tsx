"use client";

import React, { useEffect, useMemo, useState } from "react";
import {
  ArrowUturnLeftIcon,
  ClockIcon,
  DocumentIcon,
  ExclamationTriangleIcon,
  MagnifyingGlassIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useObjects } from "@/hooks/useObjects";
import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useScope } from "@/context/ScopeContext";
import { ObjectTagBadge } from "@/components/features/ObjectTagBadge";
import { cn, formatBytes, formatDate, timestampToDate } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { DEFAULT_OBJECT_KEY } from "@/constants";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
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
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Skeleton } from "@/components/ui/Skeleton";

export default function TrashPage() {
  // Trash is scoped to one ObjectKey at a time, same as /objects.
  // The ListObjects RPC requires `object_key`; without scoping, deleted
  // objects in any namespace other than `default` would be invisible.
  const { objectKey, setObjectKey } = useScope();
  const { objectKeys, fetchObjectKeys } = useObjectKeys();
  useEffect(() => {
    fetchObjectKeys();
  }, [fetchObjectKeys]);

  const {
    objects,
    loading,
    restoreObject,
    purgeObject,
    bulkDeleteObjects,
    bulkRestoreObjects,
  } = useObjects({
    objectKey,
    filter: "state == 'DELETED'",
    limit: 50,
  });

  const [search, setSearch] = useState("");
  const [useRelativeTime, setUseRelativeTime] = useState(true);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return objects;
    return objects.filter(
      (o) =>
        o.key.toLowerCase().includes(q) || o.objectId.toLowerCase().includes(q),
    );
  }, [objects, search]);

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

  const handleRestore = async (id: string) => {
    const obj = objects.find((o) => o.objectId === id);
    if (!obj) return;
    setBusyId(id);
    try {
      await restoreObject(obj);
    } finally {
      setBusyId(null);
    }
  };

  const handlePurge = async () => {
    if (!confirmDelete) return;
    const obj = objects.find((o) => o.objectId === confirmDelete);
    if (!obj) return;
    setBusyId(confirmDelete);
    try {
      await purgeObject(obj);
      setConfirmDelete(null);
    } finally {
      setBusyId(null);
    }
  };

  const itemsForBulk = () =>
    Array.from(selected)
      .map((id) => objects.find((o) => o.objectId === id))
      .filter((o): o is NonNullable<typeof o> => !!o)
      .map((o) => ({ name: o.name, objectKey: o.objectKey }));

  const handleBulkRestore = async () => {
    setBulkBusy(true);
    try {
      await bulkRestoreObjects(itemsForBulk());
      setSelected(new Set());
    } finally {
      setBulkBusy(false);
    }
  };

  const handleBulkDelete = async () => {
    setBulkBusy(true);
    setConfirmBulkDelete(false);
    try {
      await bulkDeleteObjects(itemsForBulk(), { permanent: true });
      setSelected(new Set());
    } finally {
      setBulkBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Trash"
        description="Soft-deleted objects awaiting restore or permanent purge."
        showDefaultActions={false}
      />

      {/* ObjectKey scope picker — Trash, like /objects, lists deleted
          rows from one ObjectKey at a time. Without this every namespace
          other than `default` would appear empty. */}
      <div className="flex flex-wrap items-end gap-3 rounded-lg border bg-card/40 p-3">
        <div className="space-y-1.5">
          <Label htmlFor="trash-key-picker" className={T.label}>
            Object Key
          </Label>
          <SelectRoot value={objectKey} onValueChange={setObjectKey}>
            <SelectTrigger id="trash-key-picker" className="w-[260px]">
              <SelectValue placeholder="Select an object key…" />
            </SelectTrigger>
            <SelectContent>
              {!objectKeys.some((k) => k.objectKey === DEFAULT_OBJECT_KEY) && (
                <SelectItem value={DEFAULT_OBJECT_KEY}>
                  <span className="font-mono">{DEFAULT_OBJECT_KEY}</span>
                </SelectItem>
              )}
              {objectKeys
                .slice()
                .sort((a, b) => a.objectKey.localeCompare(b.objectKey))
                .map((k) => (
                  <SelectItem key={k.objectKey} value={k.objectKey}>
                    <span className="font-mono">{k.objectKey}</span>
                    {k.displayName && k.displayName !== k.objectKey && (
                      <span className="text-muted-foreground">
                        {" "}
                        — {k.displayName}
                      </span>
                    )}
                  </SelectItem>
                ))}
              {/* Surface the active scope even if the list hasn't loaded
                  it yet (e.g. implicit key from a tag-based upload). */}
              {objectKey &&
                objectKey !== DEFAULT_OBJECT_KEY &&
                !objectKeys.some((k) => k.objectKey === objectKey) && (
                  <SelectItem value={objectKey}>
                    <span className="font-mono">{objectKey}</span>
                    <span className="text-muted-foreground"> (current)</span>
                  </SelectItem>
                )}
            </SelectContent>
          </SelectRoot>
        </div>
        <p className="pb-1 text-xs text-muted-foreground">
          Listing deleted objects inside this namespace.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search by key or object ID…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Badge variant="secondary" className="font-mono tabular-nums">
          {objects.length} deleted
        </Badge>
      </div>

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
              <TableHead>Name</TableHead>
              <TableHead className="hidden md:table-cell">Object Key</TableHead>
              <TableHead className="hidden md:table-cell">Tag</TableHead>
              <TableHead className="hidden lg:table-cell">MIME</TableHead>
              <TableHead className="hidden md:table-cell text-right">
                Size
              </TableHead>
              <TableHead className="hidden sm:table-cell">
                <button
                  type="button"
                  onClick={() => setUseRelativeTime((v) => !v)}
                  className="inline-flex items-center gap-1 text-xs font-medium uppercase tracking-wider text-muted-foreground hover:text-foreground"
                >
                  Deleted at
                  <ClockIcon className="size-3" />
                </button>
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
              filtered.map((obj) => (
                <TableRow
                  key={obj.objectId}
                  className="group"
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
                          {obj.key.split("/").pop()}
                        </div>
                        <div
                          className={cn(
                            T.codeSmall,
                            "truncate text-muted-foreground",
                          )}
                        >
                          {obj.objectId}
                        </div>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <span
                      className={cn(
                        T.code,
                        "inline-flex items-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30 px-2 py-0.5",
                      )}
                    >
                      {obj.objectKey || "—"}
                    </span>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <ObjectTagBadge
                      objectTag={obj.tags?.object_tag}
                      linked={!!obj.tags?.object_tag}
                    />
                  </TableCell>
                  <TableCell className="hidden lg:table-cell text-xs text-muted-foreground font-mono">
                    {obj.contentType || "binary/octet-stream"}
                  </TableCell>
                  <TableCell className="hidden md:table-cell text-right text-xs font-mono tabular-nums text-muted-foreground">
                    {formatBytes(Number(obj.sizeBytes))}
                  </TableCell>
                  <TableCell className="hidden sm:table-cell text-xs text-muted-foreground whitespace-nowrap">
                    {obj.terminatedAt
                      ? useRelativeTime
                        ? formatDate(timestampToDate(obj.terminatedAt))
                        : timestampToDate(obj.terminatedAt).toLocaleString()
                      : "—"}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1.5">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={!!busyId}
                            onClick={() => handleRestore(obj.objectId)}
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
                            disabled={!!busyId}
                            onClick={() => setConfirmDelete(obj.objectId)}
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
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      {/* Bulk action bar */}
      {selected.size > 0 && (
        <div className="fixed bottom-6 left-1/2 z-50 -translate-x-1/2">
          <div className="flex items-center gap-3 rounded-lg border bg-card px-3 py-2 shadow-lg">
            <Badge variant="secondary" className="font-mono tabular-nums">
              {selected.size} selected
            </Badge>
            <Button
              size="sm"
              variant="outline"
              disabled={bulkBusy}
              onClick={handleBulkRestore}
            >
              <ArrowUturnLeftIcon className="size-4" />
              Restore all
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={bulkBusy}
              onClick={() => setConfirmBulkDelete(true)}
            >
              <TrashIcon className="size-4" />
              Purge all
            </Button>
          </div>
        </div>
      )}

      {/* Single permanent delete */}
      <AlertDialog
        open={!!confirmDelete}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <ExclamationTriangleIcon className="size-5 text-destructive" />
              Permanently purge this object?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Removes object{" "}
              <span className="font-mono text-foreground">{confirmDelete}</span>{" "}
              from the storage backend. The action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handlePurge}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Purge permanently
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Bulk permanent delete */}
      <AlertDialog open={confirmBulkDelete} onOpenChange={setConfirmBulkDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <ExclamationTriangleIcon className="size-5 text-destructive" />
              Permanently purge {selected.size} objects?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Each selected row will be removed from the storage backend. This
              action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleBulkDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Purge {selected.size} objects
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

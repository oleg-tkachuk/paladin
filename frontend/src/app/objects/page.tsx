"use client";

import React, { useCallback, useEffect, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

import {
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  ClockIcon,
  DocumentIcon,
  ExclamationTriangleIcon,
  MagnifyingGlassIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useActions } from "@/context/ActionsContext";
import { useScope } from "@/context/ScopeContext";
import { useObjects } from "@/hooks/useObjects";
import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useNotification } from "@/components/ui/Notification";
import { DEFAULT_OBJECT_KEY, STORAGE_KEYS } from "@/constants";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { ObjectInspector } from "@/components/features/ObjectInspector";
import { ObjectsFilterBar } from "@/components/features/objects/ObjectsFilterBar";
import { BulkActionsToolbar } from "@/components/features/objects/BulkActionsToolbar";
import { SaveViewModal } from "@/components/features/objects/SaveViewModal";
import { BulkEditModal } from "@/components/features/objects/BulkEditModal";
import { ObjectTableRow } from "@/components/features/objects/ObjectTableRow";

import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

interface ViewFilters {
  search?: string;
  status?: string;
  recursive?: boolean;
}
interface SavedView {
  name: string;
  filters: ViewFilters;
}

type SortDirection = "asc" | "desc" | null;
interface SortState {
  column: string;
  direction: SortDirection;
}
function getNextSort(prev: SortState, column: string): SortState {
  if (prev.column !== column) return { column, direction: "asc" };
  if (prev.direction === "asc") return { column, direction: "desc" };
  if (prev.direction === "desc") return { column: "", direction: null };
  return { column, direction: "asc" };
}

function SortHeader({
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

function ObjectsPageContent() {
  // (scope is read where needed below via useScope)
  const searchParams = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();

  // ObjectKey scope — single source of truth in ScopeContext (which
  // persists to localStorage). The URL `?objectKey=…` is honored on
  // first load so shareable links keep working.
  const { objectKey, setObjectKey: setScopeObjectKey } = useScope();
  useEffect(() => {
    const fromUrl = searchParams.get("objectKey");
    if (fromUrl && fromUrl !== objectKey) {
      setScopeObjectKey(fromUrl);
    }
    // run once per URL navigation
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  // Available ObjectKeys for the picker. Auto-loaded from the backend so
  // implicit keys created by tag-based uploads (e.g. "logs", "photos")
  // appear alongside explicitly-provisioned ones.
  const { objectKeys, fetchObjectKeys } = useObjectKeys();
  useEffect(() => {
    fetchObjectKeys();
  }, [fetchObjectKeys]);

  const [status, setStatus] = useState<string | undefined>(
    searchParams.get("status") || undefined,
  );
  const [search, setSearch] = useState(searchParams.get("search") || "");
  const [recursive, setRecursive] = useState(
    searchParams.get("recursive") === "true",
  );
  const [selectedInspectorKey, setSelectedInspectorKey] = useState<
    string | null
  >(null);
  const [sort, setSort] = useState<SortState>({
    column: searchParams.get("sort") || "",
    direction: (searchParams.get("dir") as "asc" | "desc") || null,
  });

  const syncToUrl = useCallback(
    (params: Record<string, string | undefined>) => {
      const current = new URLSearchParams(searchParams.toString());
      for (const [key, value] of Object.entries(params)) {
        if (value) current.set(key, value);
        else current.delete(key);
      }
      const qs = current.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [searchParams, pathname, router],
  );

  const handleSort = (column: string) => {
    setSort((prev) => {
      const next = getNextSort(prev, column);
      syncToUrl({
        sort: next.column || undefined,
        dir: next.direction || undefined,
      });
      return next;
    });
  };

  const handleStatusChange = (value: string | undefined) => {
    setStatus(value);
    syncToUrl({ status: value });
  };

  const handleSearchChange = (value: string) => {
    setSearch(value);
    if (value.length > 2 || value === "") {
      syncToUrl({ search: value || undefined });
    }
  };

  const handleRecursiveChange = (value: boolean) => {
    setRecursive(value);
    syncToUrl({ recursive: value ? "true" : undefined });
  };

  const filterParts: string[] = [];
  if (status) filterParts.push(`state == '${status}'`);
  if (search.length > 2)
    filterParts.push(`key.contains('${search.replace(/'/g, "\\'")}')`);
  const filter = filterParts.join(" && ");

  const {
    objects,
    loading,
    error,
    refresh,
    loadMore,
    nextCursor,
    softDeleteObject,
    purgeObject,
    bulkDeleteObjects,
    bulkRestoreObjects,
    bulkPatchObjects,
    copyObject,
    generateDownloadUrl,
  } = useObjects({
    objectKey,
    filter,
    orderBy: sort.column || undefined,
    sortDirection: sort.direction,
  });

  const handleObjectKeyChange = useCallback(
    (next: string) => {
      setScopeObjectKey(next);
      syncToUrl({ objectKey: next });
    },
    [setScopeObjectKey, syncToUrl],
  );

  const { showNotification } = useNotification();

  // ─── confirmation dialogs ──────────────────────────────────────────────
  const [confirm, setConfirm] = useState<{
    open: boolean;
    title: string;
    message: string;
    type: "danger" | "warning";
    confirmText: string;
    onConfirm: () => Promise<void>;
  }>({
    open: false,
    title: "",
    message: "",
    type: "warning",
    confirmText: "Confirm",
    onConfirm: async () => {},
  });

  const handleSoftDelete = async (obj: {
    objectId: string;
    objectKey: string;
    key: string;
  }) => {
    setConfirm({
      open: true,
      type: "warning",
      title: "Move to Trash",
      message: `Move "${obj.key.split("/").pop()}" to the trash bin? You can restore it later from the Trash page.`,
      confirmText: "Move to Trash",
      onConfirm: async () => {
        const full = objects.find((o) => o.objectId === obj.objectId);
        if (full) await softDeleteObject(full);
      },
    });
  };

  const handleHardDelete = async (obj: {
    objectId: string;
    objectKey: string;
    key: string;
  }) => {
    setConfirm({
      open: true,
      type: "danger",
      title: "Permanently delete object?",
      message: `This will PERMANENTLY delete "${obj.key.split("/").pop()}". This action cannot be undone.`,
      confirmText: "Purge permanently",
      onConfirm: async () => {
        const full = objects.find((o) => o.objectId === obj.objectId);
        if (full) await purgeObject(full);
      },
    });
  };

  const copyToClipboard = async (
    textOrPromise: string | Promise<string>,
    label: string,
  ) => {
    try {
      if (
        typeof textOrPromise !== "string" &&
        navigator.clipboard &&
        window.ClipboardItem
      ) {
        try {
          const textPromise = textOrPromise.then(
            (text) => new Blob([text], { type: "text/plain" }),
          );
          const item = new window.ClipboardItem({
            "text/plain": textPromise,
          });
          await navigator.clipboard.write([item]);
          showNotification({
            type: "success",
            title: "Copied",
            message: `${label} copied to clipboard.`,
          });
          return;
        } catch (e) {
          console.warn("ClipboardItem with Promise failed, falling back", e);
        }
      }
      const text = await textOrPromise;
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
      } else {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.left = "-9999px";
        document.body.appendChild(ta);
        ta.focus();
        ta.select();
        try {
          document.execCommand("copy");
        } finally {
          ta.remove();
        }
      }
      showNotification({
        type: "success",
        title: "Copied",
        message: `${label} copied to clipboard.`,
      });
    } catch (err) {
      console.error("Failed to copy", err);
      showNotification({
        type: "error",
        title: "Copy failed",
        message: "Could not copy to clipboard.",
      });
    }
  };

  // ─── selection ──────────────────────────────────────────────────────────
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [isBulkDeleting, setIsBulkDeleting] = useState(false);
  const [isBulkEditing, setIsBulkEditing] = useState(false);
  const [bulkLabels, setBulkLabels] = useState("");

  const { registerAction, unregisterAction, executeAction } = useActions();

  const [visibleColumns, setVisibleColumns] = useState<Set<string>>(
    new Set([
      "key",
      "object_key",
      "object_tag",
      "mime",
      "size",
      "status",
      "created",
      "actions",
    ]),
  );
  const toggleColumn = (col: string) => {
    const next = new Set(visibleColumns);
    if (next.has(col)) next.delete(col);
    else next.add(col);
    setVisibleColumns(next);
  };

  useEffect(() => {
    registerAction({
      id: "objects.refresh",
      label: "Sync View",
      description: "Refresh objects from backend",
      category: "operation",
      perform: async () => {
        await refresh();
      },
    });
    registerAction({
      id: "objects.save_view",
      label: "Save View bookmark",
      description: "Persist current filter configuration",
      category: "operation",
      perform: () => setIsSavingView(true),
    });
    if (selectedIds.size > 0) {
      registerAction({
        id: "objects.bulk_delete",
        label: `Trash ${selectedIds.size} selected objects`,
        description: "Move selected objects to trash bin",
        category: "operation",
        perform: async () => {
          await handleBulkDelete();
        },
      });
      registerAction({
        id: "objects.bulk_labels",
        label: `Patch tags for ${selectedIds.size} objects`,
        description: "Bulk update metadata tags",
        category: "operation",
        perform: () => setIsBulkEditing(true),
      });
    }
    return () => {
      unregisterAction("objects.refresh");
      unregisterAction("objects.save_view");
      unregisterAction("objects.bulk_delete");
      unregisterAction("objects.bulk_labels");
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedIds.size, refresh, registerAction, unregisterAction]);

  const toggleSelect = (
    id: string,
    e?: React.MouseEvent,
    objectKey?: string,
  ) => {
    if (e) {
      if (e.metaKey || e.ctrlKey) {
        const next = new Set(selectedIds);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        setSelectedIds(next);
      } else {
        setSelectedInspectorKey(objectKey ?? null);
      }
    } else {
      const next = new Set(selectedIds);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      setSelectedIds(next);
    }
  };

  const toggleSelectAll = () => {
    if (selectedIds.size === objects.length) setSelectedIds(new Set());
    else setSelectedIds(new Set(objects.map((o) => o.objectId)));
  };

  const handleBulkDelete = async () => {
    const selectedItems = objects.filter((o) => selectedIds.has(o.objectId));
    const deletePayload = selectedItems.map((o) => ({
      name: o.name,
      objectKey: o.objectKey,
    }));
    const ids = selectedItems.map((o) => o.objectId);
    try {
      setIsBulkDeleting(true);
      await bulkDeleteObjects(deletePayload);
      setSelectedIds(new Set());
      registerAction({
        id: "objects.bulk_restore_last",
        label: "Undo: Restore Objects",
        category: "operation",
        perform: async () => {
          await bulkRestoreObjects(deletePayload);
          await refresh();
        },
      });
      showNotification({
        type: "success",
        title: "Moved to Trash",
        message: `${ids.length} objects moved to trash.`,
        action: {
          label: "Undo",
          onClick: () => executeAction("objects.bulk_restore_last"),
        },
      });
    } finally {
      setIsBulkDeleting(false);
    }
  };

  const handleBulkPatch = async () => {
    if (selectedIds.size === 0) return;
    setIsBulkDeleting(true);
    try {
      const labels: Record<string, string> = {};
      bulkLabels.split(",").forEach((pair) => {
        const [k, v] = pair.split(":").map((s) => s.trim());
        if (k && v) labels[k] = v;
      });
      await bulkPatchObjects(
        Array.from(selectedIds).map((id) => ({ objectId: id, tags: labels })),
      );
      setSelectedIds(new Set());
      setIsBulkEditing(false);
      setBulkLabels("");
    } finally {
      setIsBulkDeleting(false);
    }
  };

  const [editingLabelsId, setEditingLabelsId] = useState<string | null>(null);
  const [editLabelsValue, setEditLabelsValue] = useState("");
  const [useRelativeTime, setUseRelativeTime] = useState(true);

  const [savedViews, setSavedViews] = useState<SavedView[]>([]);
  const [isSavingView, setIsSavingView] = useState(false);
  const [newViewName, setNewViewName] = useState("");

  const [copyMove, setCopyMove] = useState<{
    open: boolean;
    type: "copy" | "move";
    obj: { key: string; objectKey: string } | null;
    destKey: string;
  }>({
    open: false,
    type: "copy",
    obj: null,
    destKey: "",
  });

  const handleCopyMove = async () => {
    if (!copyMove.obj || !copyMove.destKey) return;
    const source = objects.find(
      (o) =>
        o.key === copyMove.obj?.key && o.objectKey === copyMove.obj.objectKey,
    );
    if (!source) return;
    await copyObject(source.name, copyMove.destKey, copyMove.obj.objectKey);
    if (copyMove.type === "move") await softDeleteObject(source);
    setCopyMove((prev) => ({ ...prev, open: false }));
  };

  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEYS.savedViews);
    if (stored) setSavedViews(JSON.parse(stored));
  }, []);

  const saveView = () => {
    if (!newViewName) return;
    const newView: SavedView = {
      name: newViewName,
      filters: { search, status, recursive },
    };
    const updated = [...savedViews, newView];
    setSavedViews(updated);
    localStorage.setItem(STORAGE_KEYS.savedViews, JSON.stringify(updated));
    setIsSavingView(false);
    setNewViewName("");
  };

  const applyView = (view: SavedView) => {
    setSearch(view.filters.search || "");
    setStatus(view.filters.status);
    setRecursive(view.filters.recursive ?? false);
  };

  const deleteView = (name: string) => {
    const updated = savedViews.filter((v) => v.name !== name);
    setSavedViews(updated);
    localStorage.setItem(STORAGE_KEYS.savedViews, JSON.stringify(updated));
  };

  const handleStartInlineEdit = (obj: {
    objectId: string;
    tags?: Record<string, string>;
  }) => {
    setEditingLabelsId(obj.objectId);
    const labelsStr = Object.entries(obj.tags || {})
      .map(([k, v]) => `${k}:${v}`)
      .join(", ");
    setEditLabelsValue(labelsStr);
  };

  const handleSaveInlineLabels = async (id: string) => {
    const labels: Record<string, string> = {};
    editLabelsValue.split(",").forEach((pair) => {
      const [k, v] = pair.split(":").map((s) => s.trim());
      if (k && v) labels[k] = v;
    });
    await bulkPatchObjects([{ objectId: id, tags: labels }]);
    setEditingLabelsId(null);
  };

  const allChecked = objects.length > 0 && selectedIds.size === objects.length;
  const someChecked = selectedIds.size > 0 && !allChecked;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Objects"
        description="Browse, inspect, and manage stored objects across keys."
        showDefaultActions={false}
      />

      <ObjectInspector
        objectKey={selectedInspectorKey}
        // Lookup must scope to the same ObjectKey the row was listed under,
        // not the global ScopeContext default — they're identical here, but
        // passing it explicitly makes the dependency obvious to readers and
        // robust to future Inspector calls from non-/objects surfaces.
        parentObjectKey={objectKey}
        onClose={() => setSelectedInspectorKey(null)}
      />
      <BulkActionsToolbar
        selectedCount={selectedIds.size}
        onEditLabels={() => setIsBulkEditing(true)}
        onBulkDelete={handleBulkDelete}
        onClearSelection={() => setSelectedIds(new Set())}
        isProcessing={isBulkDeleting}
      />
      <SaveViewModal
        isOpen={isSavingView}
        viewName={newViewName}
        onViewNameChange={setNewViewName}
        onSave={saveView}
        onClose={() => setIsSavingView(false)}
      />
      <BulkEditModal
        isOpen={isBulkEditing}
        selectedCount={selectedIds.size}
        labels={bulkLabels}
        onLabelsChange={setBulkLabels}
        onSubmit={handleBulkPatch}
        onClose={() => setIsBulkEditing(false)}
        isProcessing={isBulkDeleting}
      />
      {/* Object Key scope picker — required because ListObjects RPC takes
          one object_key. Uploads via /upload write to whichever ObjectTag
          slug the user picked (the upload flow uses tag.slug AS the
          object_key); this dropdown is how those rows become visible. */}
      <div className="flex flex-wrap items-end gap-3 rounded-lg border bg-card/40 p-3">
        <div className="space-y-1.5">
          <Label htmlFor="objects-key-picker" className={T.label}>
            Object Key
          </Label>
          <SelectRoot value={objectKey} onValueChange={handleObjectKeyChange}>
            <SelectTrigger id="objects-key-picker" className="w-[260px]">
              <SelectValue placeholder="Select an object key…" />
            </SelectTrigger>
            <SelectContent>
              {/* Always show the bootstrap default first so it's always
                  reachable even if the backend hasn't returned it yet. */}
              {!objectKeys.some((k) => k.objectKey === DEFAULT_OBJECT_KEY) && (
                <SelectItem value={DEFAULT_OBJECT_KEY}>
                  <span className="font-mono">default</span>
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
              {/* Surface the current selection even if it's not in the
                  loaded list yet (e.g. immediately after upload to a new
                  implicit key, before useObjectKeys refetches). */}
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
          Listing namespace inside the current tenant.
        </p>
      </div>

      <ObjectsFilterBar
        search={search}
        onSearchChange={handleSearchChange}
        recursive={recursive}
        onRecursiveChange={handleRecursiveChange}
        status={status}
        onStatusChange={handleStatusChange}
        visibleColumns={visibleColumns}
        onToggleColumn={toggleColumn}
        savedViews={savedViews}
        onApplyView={applyView}
        onDeleteView={deleteView}
        onSaveView={() => setIsSavingView(true)}
        loading={loading}
        onRefresh={refresh}
      />

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">
                <Checkbox
                  checked={someChecked ? "indeterminate" : allChecked}
                  onCheckedChange={toggleSelectAll}
                  aria-label="Select all rows"
                />
              </TableHead>
              {visibleColumns.has("key") && (
                <TableHead>
                  <SortHeader
                    label="Name / ID"
                    column="key"
                    current={sort}
                    onSort={handleSort}
                  />
                </TableHead>
              )}
              {visibleColumns.has("object_key") && (
                <TableHead className="hidden md:table-cell">
                  <SortHeader
                    label="Object Key"
                    column="object_key"
                    current={sort}
                    onSort={handleSort}
                  />
                </TableHead>
              )}
              {visibleColumns.has("object_tag") && (
                <TableHead>
                  <SortHeader
                    label="Object Tags"
                    column="object_tag"
                    current={sort}
                    onSort={handleSort}
                  />
                </TableHead>
              )}
              {visibleColumns.has("mime") && (
                <TableHead className="hidden lg:table-cell">
                  <SortHeader
                    label="MIME"
                    column="content_type"
                    current={sort}
                    onSort={handleSort}
                  />
                </TableHead>
              )}
              {visibleColumns.has("size") && (
                <TableHead className="hidden md:table-cell text-right">
                  <SortHeader
                    label="Size"
                    column="size_bytes"
                    current={sort}
                    onSort={handleSort}
                    align="end"
                  />
                </TableHead>
              )}
              {visibleColumns.has("status") && (
                <TableHead>
                  <SortHeader
                    label="State"
                    column="status"
                    current={sort}
                    onSort={handleSort}
                  />
                </TableHead>
              )}
              {visibleColumns.has("created") && (
                <TableHead className="hidden sm:table-cell">
                  <div className="flex items-center gap-1">
                    <SortHeader
                      label="Created"
                      column="created_at"
                      current={sort}
                      onSort={handleSort}
                    />
                    <button
                      type="button"
                      onClick={() => setUseRelativeTime((v) => !v)}
                      className="text-muted-foreground hover:text-foreground"
                      title="Toggle timestamp format"
                    >
                      <ClockIcon className="size-3" />
                    </button>
                  </div>
                </TableHead>
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
                onToggleSelect={toggleSelect}
                onInspect={setSelectedInspectorKey}
                onStartInlineEdit={handleStartInlineEdit}
                onSaveInlineLabels={handleSaveInlineLabels}
                onCancelInlineEdit={() => setEditingLabelsId(null)}
                onEditLabelsValueChange={setEditLabelsValue}
                onCopyToClipboard={copyToClipboard}
                onSoftDelete={handleSoftDelete}
                onHardDelete={handleHardDelete}
                onCopy={(o) =>
                  setCopyMove({
                    open: true,
                    type: "copy",
                    obj: o,
                    destKey: o.key + "-copy",
                  })
                }
                onMove={(o) =>
                  setCopyMove({
                    open: true,
                    type: "move",
                    obj: o,
                    destKey: o.key,
                  })
                }
                onGenerateDownloadUrl={async (key, objectKey) => {
                  const o = objects.find(
                    (x) => x.key === key && x.objectKey === objectKey,
                  );
                  if (!o) return undefined;
                  const url = await generateDownloadUrl(o.name);
                  return url ? { url: url.url } : undefined;
                }}
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
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => refresh()}
                      >
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
              onClick={() => loadMore?.()}
              disabled={loading}
              className="text-muted-foreground"
            >
              {loading ? "Syncing…" : "Load more objects"}
            </Button>
          </div>
        )}
      </Card>

      <div className="flex justify-center pb-4">
        <Badge variant="secondary" className="font-mono tabular-nums">
          {objects.length} objects
        </Badge>
      </div>

      {/* ─── Copy / Move dialog ───────────────────────────────────────── */}
      <Dialog
        open={copyMove.open}
        onOpenChange={(o) => !o && setCopyMove((p) => ({ ...p, open: false }))}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {copyMove.type === "copy" ? "Copy" : "Move"} object
            </DialogTitle>
            <DialogDescription>
              {copyMove.type === "copy"
                ? "Server-side copy. The source object remains in place."
                : "Soft-deletes the source after a successful copy. The original can be restored from Trash."}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-1.5">
              <Label>Source path</Label>
              <Input
                disabled
                value={copyMove.obj?.key || ""}
                className="font-mono text-xs text-muted-foreground"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="dest-key">Destination key</Label>
              <Input
                id="dest-key"
                autoFocus
                value={copyMove.destKey}
                onChange={(e) =>
                  setCopyMove((p) => ({ ...p, destKey: e.target.value }))
                }
                onKeyDown={(e) => e.key === "Enter" && handleCopyMove()}
                className="font-mono text-xs"
              />
              <p className={T.hint}>
                Full path including filename, within the same ObjectKey.
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setCopyMove((p) => ({ ...p, open: false }))}
            >
              Cancel
            </Button>
            <Button onClick={handleCopyMove}>
              Confirm {copyMove.type === "copy" ? "copy" : "move"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ─── Confirmation (soft + hard delete) ───────────────────────── */}
      <AlertDialog
        open={confirm.open}
        onOpenChange={(o) => !o && setConfirm((p) => ({ ...p, open: false }))}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              {confirm.type === "danger" ? (
                <ExclamationTriangleIcon className="size-5 text-destructive" />
              ) : (
                <TrashIcon className="size-5 text-chart-3" />
              )}
              {confirm.title}
            </AlertDialogTitle>
            <AlertDialogDescription className="whitespace-pre-wrap">
              {confirm.message}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={async () => {
                await confirm.onConfirm();
                setConfirm((p) => ({ ...p, open: false }));
              }}
              className={
                confirm.type === "danger"
                  ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  : ""
              }
            >
              {confirm.confirmText}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

export default function ObjectsPage() {
  return (
    <React.Suspense
      fallback={<div className="h-96 animate-pulse rounded-xl bg-card" />}
    >
      <ObjectsPageContent />
    </React.Suspense>
  );
}

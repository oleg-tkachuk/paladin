"use client";

// Live objects routed to this ObjectKey — moved from the legacy
// flat /objects route. The big difference vs the old page: the
// ObjectKey scope is fixed by the URL (parent layout already
// resolved it via context), so the in-page picker is gone and
// `?objectKey=…` URL handling is dropped. Everything else
// (filters, bulk operations, multipart upload state, copy/move
// dialog, saved views) carries over verbatim.

import React, { useEffect, useMemo, useState } from "react";

import { useActions } from "@/context/ActionsContext";
import { useObjects } from "@/hooks/useObjects";
import { useDistinctTags } from "@/hooks/useDistinctTags";
import { useNotification } from "@/components/ui/Notification";
import { STORAGE_KEYS } from "@/constants";
import { z } from "zod";
import { safeParseJson } from "@/lib/parseJson";
import { ObjectInspector } from "@/components/features/ObjectInspector";
import { ObjectsFilterBar } from "@/components/features/objects/ObjectsFilterBar";
import { BulkActionsToolbar } from "@/components/features/objects/BulkActionsToolbar";
import { SaveViewModal } from "@/components/features/objects/SaveViewModal";
import { BulkEditModal } from "@/components/features/objects/BulkEditModal";

import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";

import { useObjectKey } from "../objectkey-context";
import { SavedViewSchema, type SavedView } from "./_view";
import { useObjectListState } from "./useObjectListState";
import { CopyMoveDialog } from "./CopyMoveDialog";
import { DeleteConfirmDialog } from "./DeleteConfirmDialog";
import { ObjectsTable } from "./ObjectsTable";

function ObjectKeyObjectsContent() {
  const { objectKey: ok } = useObjectKey();
  // The "scope" of this listing — fixed by URL, supplied by context.
  const objectKey = ok.objectKey;

  // URL-synced filter + sort state and the derived CEL `filter` live in a
  // dedicated hook. Selection and tag-option accumulation stay here because
  // they depend on the loaded objects, which the hook never sees.
  const {
    status,
    setStatus,
    tagFilter,
    search,
    setSearch,
    recursive,
    setRecursive,
    sort,
    filter,
    handleSort,
    handleStatusChange,
    handleTagChange,
    handleSearchChange,
    handleRecursiveChange,
  } = useObjectListState();

  const [tagOptions, setTagOptions] = useState<string[]>([]);
  const [selectedInspectorKey, setSelectedInspectorKey] = useState<
    string | null
  >(null);

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

  // Authoritative tag-facet options from the server (whole-ObjectKey scope via
  // ListDistinctTags). Empty while loading or on error — the client-side
  // accumulation below remains the graceful fallback.
  const serverTagOptions = useDistinctTags(objectKey);

  // Build the tag-facet option list from the tags of loaded objects. Union
  // into prior state (never shrink) so applying a tag filter — which narrows
  // `objects` to the matching rows — doesn't collapse the dropdown to the one
  // selected pair. Scoped to this ObjectKey's page session; resets on remount.
  useEffect(() => {
    setTagOptions((prev) => {
      const seen = new Set(prev);
      let changed = false;
      for (const o of objects) {
        for (const [k, v] of Object.entries(o.tags ?? {})) {
          const pair = `${k}=${v}`;
          if (!seen.has(pair)) {
            seen.add(pair);
            changed = true;
          }
        }
      }
      return changed ? Array.from(seen).sort() : prev;
    });
  }, [objects]);

  // Server list is authoritative; union in any client-accumulated pairs so a
  // freshly-applied tag still shows even if the server fetch hasn't returned.
  const mergedTagOptions = useMemo(
    () => Array.from(new Set([...serverTagOptions, ...tagOptions])).sort(),
    [serverTagOptions, tagOptions],
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
      message: `Move "${obj.key.split("/").pop()}" to the trash bin? You can restore it later from the Trash tab.`,
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

  const toggleSelect = (
    id: string,
    e?: React.MouseEvent,
    objectKeyOfRow?: string,
  ) => {
    if (e) {
      if (e.metaKey || e.ctrlKey) {
        const next = new Set(selectedIds);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        setSelectedIds(next);
      } else {
        setSelectedInspectorKey(objectKeyOfRow ?? null);
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

  // Command-palette registration. Declared AFTER the state setters +
  // handlers it closes over (setIsSavingView, handleBulkDelete) so the React
  // Compiler can preserve their bindings — react-hooks/immutability flags a
  // forward reference otherwise.
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

  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEYS.savedViews);
    setSavedViews(safeParseJson(z.array(SavedViewSchema), stored) ?? []);
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
    <div className="space-y-4">
      <ObjectInspector
        objectKey={selectedInspectorKey}
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

      {/* ObjectKey scope is fixed by the URL — show it as a static
          context badge instead of a picker. The OK is already in the
          page header above (rendered by the OK detail layout); this
          local badge keeps the operator anchored when the toolbar
          gets long. */}
      <div className="flex items-center justify-between rounded-lg border bg-card/40 p-3">
        <div className="space-y-0.5">
          <Label className={T.label}>Object Key</Label>
          <div className="flex items-center gap-2">
            <Badge variant="info" className={T.code}>
              {objectKey}
            </Badge>
            {ok.bucket && (
              <span className="text-xs text-muted-foreground font-mono">
                → {ok.bucket}
              </span>
            )}
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          Listing namespace inside this tenant.
        </p>
      </div>

      <ObjectsFilterBar
        filter={{ search, status, tag: tagFilter, recursive }}
        onFilterChange={(patch) => {
          if ("search" in patch) handleSearchChange(patch.search!);
          if ("status" in patch) handleStatusChange(patch.status);
          if ("tag" in patch) handleTagChange(patch.tag);
          if ("recursive" in patch) handleRecursiveChange(patch.recursive!);
        }}
        tagOptions={mergedTagOptions}
        visibleColumns={visibleColumns}
        onToggleColumn={toggleColumn}
        savedViews={savedViews}
        onApplyView={applyView}
        onDeleteView={deleteView}
        onSaveView={() => setIsSavingView(true)}
        loading={loading}
        onRefresh={refresh}
      />

      <ObjectsTable
        objects={objects}
        loading={loading}
        error={error}
        nextCursor={nextCursor}
        onLoadMore={() => loadMore?.()}
        onRefresh={refresh}
        selectedIds={selectedIds}
        allChecked={allChecked}
        someChecked={someChecked}
        onToggleSelectAll={toggleSelectAll}
        sort={sort}
        onSort={handleSort}
        visibleColumns={visibleColumns}
        useRelativeTime={useRelativeTime}
        onToggleRelativeTime={() => setUseRelativeTime((v) => !v)}
        selectedInspectorKey={selectedInspectorKey}
        editingLabelsId={editingLabelsId}
        editLabelsValue={editLabelsValue}
        search={search}
        status={status}
        rowProps={{
          onToggleSelect: toggleSelect,
          onInspect: setSelectedInspectorKey,
          onStartInlineEdit: handleStartInlineEdit,
          onSaveInlineLabels: handleSaveInlineLabels,
          onCancelInlineEdit: () => setEditingLabelsId(null),
          onEditLabelsValueChange: setEditLabelsValue,
          onCopyToClipboard: copyToClipboard,
          onSoftDelete: handleSoftDelete,
          onHardDelete: handleHardDelete,
          onCopy: (o) =>
            setCopyMove({
              open: true,
              type: "copy",
              obj: o,
              destKey: o.key + "-copy",
            }),
          onMove: (o) =>
            setCopyMove({
              open: true,
              type: "move",
              obj: o,
              destKey: o.key,
            }),
          onGenerateDownloadUrl: async (key, objectKeyOfRow) => {
            const o = objects.find(
              (x) => x.key === key && x.objectKey === objectKeyOfRow,
            );
            if (!o) return undefined;
            const url = await generateDownloadUrl(o.name);
            return url ? { url: url.url } : undefined;
          },
        }}
      />

      <div className="flex justify-center pb-4">
        <Badge variant="secondary" className="font-mono tabular-nums">
          {objects.length} objects
        </Badge>
      </div>

      {/* ─── Copy / Move dialog ───────────────────────────────────────── */}
      <CopyMoveDialog
        open={copyMove.open}
        type={copyMove.type}
        sourceKey={copyMove.obj?.key || ""}
        destKey={copyMove.destKey}
        onDestKeyChange={(v) => setCopyMove((p) => ({ ...p, destKey: v }))}
        onConfirm={handleCopyMove}
        onClose={() => setCopyMove((p) => ({ ...p, open: false }))}
      />

      {/* ─── Confirmation (soft + hard delete) ───────────────────────── */}
      <DeleteConfirmDialog
        open={confirm.open}
        type={confirm.type}
        title={confirm.title}
        message={confirm.message}
        confirmText={confirm.confirmText}
        onConfirm={async () => {
          await confirm.onConfirm();
          setConfirm((p) => ({ ...p, open: false }));
        }}
        onOpenChange={(o) => !o && setConfirm((p) => ({ ...p, open: false }))}
      />
    </div>
  );
}

export default function ObjectKeyObjectsPage() {
  return (
    <React.Suspense
      fallback={<div className="h-96 animate-pulse rounded-xl bg-card" />}
    >
      <ObjectKeyObjectsContent />
    </React.Suspense>
  );
}

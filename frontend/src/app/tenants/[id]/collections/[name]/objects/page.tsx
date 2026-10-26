"use client";

// Live objects routed to this Collection — moved from the legacy
// flat /objects route. The big difference vs the old page: the
// Collection scope is fixed by the URL (parent layout already
// resolved it via context), so the in-page picker is gone and
// `?collection=…` URL handling is dropped. Everything else
// (filters, bulk operations, multipart upload state, copy/move
// dialog, saved views) carries over verbatim.

import { TableScope } from "@/components/ui/TableScope";
import { useTablePrefs } from "@/hooks/useTablePrefs";
import { COLUMN_OPTIONS } from "@/components/features/objects/columns";
import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import {
  NOTHING_TO_TRASH,
  splitForTrash,
  unfinishedNote,
} from "@/lib/objects/trash";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { ArrowUpTrayIcon } from "@heroicons/react/24/outline";

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

import { Badge } from "@/components/ui/badge";

import { useCollection } from "../collection-context";
import {
  OBJECT_FACET_STATUS,
  OBJECT_FACET_TYPE,
  objectFacetCounts,
  viewFacetSelection,
} from "@/lib/objectFacets";
import { activeFacets, serverCounting } from "@/lib/table/facets";

import { SavedViewSchema, type SavedView } from "./_view";
import { useObjectListState } from "./useObjectListState";
import { CopyMoveDialog } from "./CopyMoveDialog";
import { BulkCopyDialog } from "./BulkCopyDialog";
import { FolderTrail } from "./FolderTrail";
import { LiveToggle } from "@/components/ui/LiveToggle";
import { liveState } from "@/lib/live";
import { FOLDER_DELIMITER } from "@/lib/objectFolders";
import { ConfirmModal, type Affected } from "@/components/ui/ConfirmModal";
import { ObjectsTable } from "./ObjectsTable";
import { Button } from "@/components/ui/button";
import { UploadQueue } from "@/components/features/upload/UploadQueue";
import { useUpload } from "@/hooks/useUpload";
import { useUndoTrash } from "@/hooks/useUndoTrash";
import { useFileDrop } from "@/hooks/useFileDrop";
import { useTenantChangesBlocked } from "../../../tenant-context";

/** The object table's id in the user's table preferences. */
const OBJECTS_TABLE_ID = "objects";

/** How often the list asks again while Live. */
const OBJECTS_LIVE_POLL_MS = 15_000;

const STAYS_RESTORABLE =
  "It stays restorable from the Trash tab until it is purged.";
const STAY_RESTORABLE =
  "They stay restorable from the Trash tab until they are purged.";
const CANNOT_BE_UNDONE = "This cannot be undone.";
// What a permanent delete asks to be typed: the verb, not an object's key,
// which can run to a long path, and the same word for one object or many.
const PURGE_WORD = "purge";

// A selection, listed by key in its confirmation.
function selectionAffected(items: readonly { key: string }[]): Affected {
  return {
    heading: `${items.length} ${items.length === 1 ? "object" : "objects"}`,
    items: items.map((o) => o.key),
  };
}

function CollectionObjectsContent() {
  const { collection: collectionResource } = useCollection();
  // ADR-0027: a public collection has no trash and names its objects itself,
  // so deletes are permanent and copy/move — which name the copy — are off.
  const publicCollection =
    collectionResource.access === CollectionAccess.PUBLIC_READ;
  // The "scope" of this listing — fixed by URL, supplied by context.
  const collection = collectionResource.collection;

  // URL-synced filter + sort state and the derived CEL `filter` live in a
  // dedicated hook. Selection and tag-option accumulation stay here because
  // they depend on the loaded objects, which the hook never sees.
  const {
    facetSelection,
    facets,
    facetControls: controlsFor,
    setFacetSelection,
    tagFilter,
    metaFilter,
    search,
    setSearch,
    recursive,
    setRecursive,
    sort,
    filter,
    handleSort,
    handleTagChange,
    handleMetaChange,
    handleSearchChange,
    handleRecursiveChange,
    prefix,
    handlePrefixChange,
  } = useObjectListState();
  // One folder level unless Recursive is on, or a search looks for a key
  // anywhere under the folder.
  const delimiter = recursive || search ? "" : FOLDER_DELIMITER;
  // Off until asked for: a large collection re-read on a timer is load
  // nobody wanted.
  const [live, setLive] = useState(false);

  // Files dropped on the list, or chosen with Upload, land in this
  // collection; a trashed tenant takes no new objects.
  const changesBlocked = useTenantChangesBlocked();
  const { offerUndo } = useUndoTrash();
  const { queue, uploadFile, clearQueue } = useUpload();
  const uploadFiles = useCallback(
    (files: File[]) => {
      // Into the folder on screen, as a file manager would.
      for (const file of files) void uploadFile(file, collection, {}, prefix);
    },
    [uploadFile, collection, prefix],
  );
  const { dragging, dropProps } = useFileDrop({
    disabled: !!changesBlocked,
    onFiles: uploadFiles,
  });
  const fileInput = useRef<HTMLInputElement>(null);

  const [tagOptions, setTagOptions] = useState<string[]>([]);
  const [selectedInspectorKey, setSelectedInspectorKey] = useState<
    string | null
  >(null);

  const {
    objects,
    folders,
    foldersTruncated,
    facetCounts,
    facetCountsExact,
    loading,
    error,
    refresh,
    loadMore,
    nextCursor,
    softDeleteObject,
    purgeObject,
    bulkDeleteObjects,
    bulkRestoreObjects,
    bulkCopyObjects,
    bulkPatchObjects,
    copyObject,
    generateDownloadUrl,
  } = useObjects({
    collection,
    filter,
    // Status and content type narrow on the server, which counts them with
    // the first page.
    facets,
    countFacets: true,
    orderBy: sort.column || undefined,
    sortDirection: sort.direction,
    prefix,
    delimiter,
    refetchIntervalMs: live ? OBJECTS_LIVE_POLL_MS : undefined,
  });

  // Counted over the whole listing; lower bounds past the server's scan cap,
  // and none where the collection's policies decide object by object.
  const facetControls = useMemo(
    () =>
      controlsFor(
        objectFacetCounts(facetCounts),
        serverCounting(facetCounts, facetCountsExact),
      ),
    [controlsFor, facetCounts, facetCountsExact],
  );

  // Authoritative tag-facet options from the server (whole-Collection scope via
  // ListDistinctTags). Empty while loading or on error — the client-side
  // accumulation below remains the graceful fallback.
  const serverTagOptions = useDistinctTags(collection);

  // Build the tag-facet option list from the tags of loaded objects. Union
  // into prior state (never shrink) so applying a tag filter — which narrows
  // `objects` to the matching rows — doesn't collapse the dropdown to the one
  // selected pair. Scoped to this Collection's page session; resets on remount.
  // Render-phase adjust-on-change keyed on `objects` identity (stable between
  // fetches via useObjects' useMemo) — not a set-state-in-effect.
  const [seenObjects, setSeenObjects] = useState(objects);
  if (objects !== seenObjects) {
    setSeenObjects(objects);
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
  }

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
    warning?: string;
    confirmText: string;
    onConfirm: () => Promise<void>;
    // Typed out for a delete nothing restores; see ConfirmModal.
    typeToConfirm?: string;
    affected?: Affected;
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
    collection: string;
    key: string;
  }) => {
    setConfirm({
      open: true,
      type: "warning",
      title: "Move to Trash",
      message: `Move "${obj.key.split("/").pop()}" to the trash bin? It can no longer be read.`,
      warning: STAYS_RESTORABLE,
      confirmText: "Move to Trash",
      onConfirm: async () => {
        const full = objects.find((o) => o.objectId === obj.objectId);
        if (full) offerUndo(await softDeleteObject(full));
      },
    });
  };

  const handleHardDelete = async (obj: {
    objectId: string;
    collection: string;
    key: string;
  }) => {
    setConfirm({
      open: true,
      type: "danger",
      title: "Permanently delete object?",
      message: `This will permanently delete "${obj.key.split("/").pop()}".`,
      warning: CANNOT_BE_UNDONE,
      confirmText: "Purge permanently",
      typeToConfirm: PURGE_WORD,
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

  // The columns the user hid stay hidden, in this browser and the next:
  // they are the object table's preferences, not this page's state.
  const { prefs: tablePrefs, toggleColumn: hideOrShow } =
    useTablePrefs(OBJECTS_TABLE_ID);
  const visibleColumns = new Set(
    COLUMN_OPTIONS.map((c) => c.id).filter(
      (id) => !(tablePrefs.hidden ?? []).includes(id),
    ),
  );
  const toggleColumn = (col: string) => void hideOrShow(col);

  const toggleSelect = (
    id: string,
    e?: React.MouseEvent,
    collectionOfRow?: string,
  ) => {
    if (e) {
      if (e.metaKey || e.ctrlKey) {
        const next = new Set(selectedIds);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        setSelectedIds(next);
      } else {
        setSelectedInspectorKey(collectionOfRow ?? null);
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
      collection: o.collection,
    }));
    const ids = selectedItems.map((o) => o.objectId);
    if (publicCollection) {
      // No trash and no undo here, so it is confirmed first.
      setConfirm({
        open: true,
        type: "danger",
        title: `Permanently delete ${ids.length} objects?`,
        message:
          "This collection is public: its objects have no trash. They are deleted for good.",
        warning:
          "Their public URLs stop answering, though a CDN may serve a cached copy until it expires. This cannot be undone.",
        confirmText: "Delete permanently",
        typeToConfirm: PURGE_WORD,
        affected: selectionAffected(selectedItems),
        onConfirm: async () => {
          await bulkDeleteObjects(deletePayload, { permanent: true });
          setSelectedIds(new Set());
          showNotification({
            type: "success",
            title: "Deleted permanently",
            message: `${ids.length} objects deleted.`,
          });
        },
      });
      return;
    }
    // An upload that never completed has nothing to trash and is refused, so
    // it is left out of the delete and the dialog says so.
    const { trash, unfinished } = splitForTrash(selectedItems);
    if (trash.length === 0) {
      showNotification({
        type: "warning",
        title: NOTHING_TO_TRASH,
        message: unfinishedNote(unfinished.length),
      });
      return;
    }
    const trashPayload = trash.map((o) => ({
      name: o.name,
      collection: o.collection,
    }));
    setConfirm({
      open: true,
      type: "warning",
      title: `Move ${trash.length} objects to Trash?`,
      message:
        unfinished.length > 0
          ? `The selected objects can no longer be read. ${unfinishedNote(unfinished.length)}`
          : "The selected objects can no longer be read.",
      warning: STAY_RESTORABLE,
      confirmText: `Move ${trash.length} to Trash`,
      // Listed, not typed: the trash gives them back, and Undo is offered.
      affected: selectionAffected(trash),
      onConfirm: async () => {
        try {
          setIsBulkDeleting(true);
          await bulkDeleteObjects(trashPayload);
          setSelectedIds(new Set());
          // Undo is a restore, so it is confirmed like any other.
          registerAction({
            id: "objects.bulk_restore_last",
            label: "Undo: Restore Objects",
            category: "operation",
            perform: () => confirmBulkRestore(trashPayload),
          });
          showNotification({
            type: "success",
            title: "Moved to Trash",
            message: `${trash.length} objects moved to trash.`,
            action: {
              label: "Undo",
              onClick: () => executeAction("objects.bulk_restore_last"),
            },
          });
        } finally {
          setIsBulkDeleting(false);
        }
      },
    });
  };

  // The selection, as the objects it names; a restore is offered when every
  // one of them is in the trash.
  const selectedObjects = objects.filter((o) => selectedIds.has(o.objectId));
  const selectionTrashed =
    selectedObjects.length > 0 &&
    selectedObjects.every((o) => o.state === ObjectState.DELETED);
  const selectionItems = selectedObjects.map((o) => ({
    name: o.name,
    collection: o.collection,
  }));
  const [bulkCopyOpen, setBulkCopyOpen] = useState(false);
  const handleBulkCopy = async (destination: string, prefix: string) => {
    await bulkCopyObjects(selectionItems, destination, prefix);
    setBulkCopyOpen(false);
    setSelectedIds(new Set());
    showNotification({
      type: "success",
      title: "Copy started",
      message: `${selectionItems.length} objects are being copied to ${destination}; the operation shows its progress.`,
    });
  };

  const confirmBulkRestore = (items: { name: string; collection: string }[]) =>
    setConfirm({
      open: true,
      type: "warning",
      title: `Restore ${items.length} objects?`,
      message:
        "They return to the collection's active objects and can be read again.",
      warning:
        "A restore is refused for an object that changed since it was trashed.",
      confirmText: `Restore ${items.length} objects`,
      onConfirm: async () => {
        await bulkRestoreObjects(items);
        setSelectedIds(new Set());
        await refresh();
      },
    });

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
    obj: { key: string; collection: string } | null;
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
        o.key === copyMove.obj?.key && o.collection === copyMove.obj.collection,
    );
    if (!source) return;
    await copyObject(source.name, copyMove.destKey, copyMove.obj.collection);
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

  // Load persisted saved-views AFTER hydration. Reading localStorage during
  // render (lazy init) would diverge from the server's empty default and trip
  // a hydration mismatch, so this is the legitimate "sync with a browser-only
  // source" exception to set-state-in-effect.
  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEYS.savedViews);
    // eslint-disable-next-line react-hooks/set-state-in-effect -- post-hydration localStorage read; see comment above
    setSavedViews(safeParseJson(z.array(SavedViewSchema), stored) ?? []);
  }, []);

  const saveView = () => {
    if (!newViewName) return;
    const newView: SavedView = {
      name: newViewName,
      filters: {
        search,
        status: [...(facetSelection[OBJECT_FACET_STATUS] ?? [])],
        tag: tagFilter,
        type: [...(facetSelection[OBJECT_FACET_TYPE] ?? [])],
        meta: metaFilter,
        recursive,
      },
    };
    const updated = [...savedViews, newView];
    setSavedViews(updated);
    localStorage.setItem(STORAGE_KEYS.savedViews, JSON.stringify(updated));
    setIsSavingView(false);
    setNewViewName("");
  };

  // A view is the whole filter: facets it does not name are cleared, so
  // applying "Images" after a tag filter shows images, not tagged images.
  const applyView = (view: SavedView) => {
    setSearch(view.filters.search || "");
    setFacetSelection(viewFacetSelection(view.filters));
    setRecursive(view.filters.recursive ?? false);
    handleTagChange(view.filters.tag);
    handleMetaChange(view.filters.meta);
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
    <TableScope id={OBJECTS_TABLE_ID} sort={sort}>
      <div className="space-y-4">
        <ObjectInspector
          collection={selectedInspectorKey}
          parentCollection={collection}
          onClose={() => setSelectedInspectorKey(null)}
        />
        <BulkActionsToolbar
          selectedCount={selectedIds.size}
          onEditLabels={() => setIsBulkEditing(true)}
          onBulkDelete={handleBulkDelete}
          onBulkRestore={
            selectionTrashed
              ? () => confirmBulkRestore(selectionItems)
              : undefined
          }
          onBulkCopy={
            publicCollection ? undefined : () => setBulkCopyOpen(true)
          }
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

        {/* The collection is named in the row above (the collection
          layout); this one line says where its objects are stored and
          holds what acts on them, so the list starts right below. */}
        <div className="flex flex-wrap items-center justify-between gap-2">
          {collectionResource.bucket ? (
            <span
              className="min-w-0 truncate text-xs text-muted-foreground font-mono"
              title={collectionResource.bucket}
            >
              → {collectionResource.bucket}
            </span>
          ) : (
            <span />
          )}
          <div className="flex items-center gap-3">
            <p className="text-xs text-muted-foreground">
              Drop files on the list to upload them here.
            </p>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              aria-label={`Files to upload to ${collection}`}
              onChange={(e) => {
                uploadFiles(Array.from(e.target.files ?? []));
                e.target.value = "";
              }}
            />
            <LiveToggle
              state={liveState({ live })}
              onToggle={() => setLive((v) => !v)}
              intervalMs={OBJECTS_LIVE_POLL_MS}
            />
            <Button
              size="sm"
              onClick={() => fileInput.current?.click()}
              disabled={!!changesBlocked}
              title={changesBlocked ?? undefined}
            >
              <ArrowUpTrayIcon className="size-4" />
              Upload
            </Button>
          </div>
        </div>

        <UploadQueue queue={queue} onClear={clearQueue} />

        <div {...dropProps} data-drop-zone className="relative space-y-4">
          {dragging ? (
            <div
              aria-hidden
              className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-lg border-2 border-dashed border-primary bg-primary/5"
            >
              <span className="rounded-md bg-card px-3 py-1.5 text-sm font-medium shadow-raised">
                Drop to upload to {collection}
              </span>
            </div>
          ) : null}
          <ObjectsFilterBar
            filter={{
              search,
              tag: tagFilter,
              meta: metaFilter,
              recursive,
            }}
            facets={facetControls}
            onFilterChange={(patch) => {
              if ("search" in patch) handleSearchChange(patch.search!);
              if ("tag" in patch) handleTagChange(patch.tag);
              if ("meta" in patch) handleMetaChange(patch.meta);
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

          <FolderTrail
            collection={collection}
            prefix={prefix}
            onOpen={handlePrefixChange}
          />
          <ObjectsTable
            folders={
              delimiter
                ? {
                    prefix,
                    list: folders,
                    truncated: foldersTruncated,
                    onOpen: handlePrefixChange,
                  }
                : undefined
            }
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
            narrowed={
              activeFacets(facetSelection) > 0 || !!tagFilter || !!metaFilter
            }
            rowProps={{
              onToggleSelect: toggleSelect,
              onInspect: setSelectedInspectorKey,
              onStartInlineEdit: handleStartInlineEdit,
              onSaveInlineLabels: handleSaveInlineLabels,
              onCancelInlineEdit: () => setEditingLabelsId(null),
              onEditLabelsValueChange: setEditLabelsValue,
              onCopyToClipboard: copyToClipboard,
              onSoftDelete: publicCollection ? undefined : handleSoftDelete,
              onHardDelete: handleHardDelete,
              onCopy: publicCollection
                ? undefined
                : (o) =>
                    setCopyMove({
                      open: true,
                      type: "copy",
                      obj: o,
                      destKey: o.key + "-copy",
                    }),
              onMove: publicCollection
                ? undefined
                : (o) =>
                    setCopyMove({
                      open: true,
                      type: "move",
                      obj: o,
                      destKey: o.key,
                    }),
              onGenerateDownloadUrl: async (key, collectionOfRow) => {
                const o = objects.find(
                  (x) => x.key === key && x.collection === collectionOfRow,
                );
                if (!o) return undefined;
                const url = await generateDownloadUrl(o.name);
                return url ? { url: url.url } : undefined;
              },
            }}
          />
        </div>

        <div className="flex justify-center pb-4">
          <Badge variant="secondary" className="font-mono tabular-nums">
            {objects.length} objects
          </Badge>
        </div>

        <BulkCopyDialog
          open={bulkCopyOpen}
          count={selectionItems.length}
          sourceCollection={collection}
          onConfirm={handleBulkCopy}
          onClose={() => setBulkCopyOpen(false)}
        />

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

        {/* ─── Confirmation (trash, purge, restore) ────────────────────── */}
        <ConfirmModal
          isOpen={confirm.open}
          type={confirm.type}
          title={confirm.title}
          message={confirm.message}
          warning={confirm.warning}
          confirmText={confirm.confirmText}
          typeToConfirm={confirm.typeToConfirm}
          affected={confirm.affected}
          onConfirm={confirm.onConfirm}
          onClose={() => setConfirm((p) => ({ ...p, open: false }))}
        />
      </div>
    </TableScope>
  );
}

export default function CollectionObjectsPage() {
  return (
    <React.Suspense
      fallback={<div className="h-96 animate-pulse rounded-xl bg-card" />}
    >
      <CollectionObjectsContent />
    </React.Suspense>
  );
}

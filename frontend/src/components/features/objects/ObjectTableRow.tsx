"use client";

import React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import {
  DocumentIcon,
  TrashIcon,
  EyeIcon,
  ArrowDownTrayIcon,
  ClipboardIcon,
  ExclamationTriangleIcon,
  TagIcon,
  InformationCircleIcon,
  CheckIcon,
  XMarkIcon,
  DocumentDuplicateIcon,
  ArrowRightCircleIcon,
  EllipsisVerticalIcon,
} from "@heroicons/react/24/outline";
import { ObjectTagBadge } from "@/components/features/ObjectTagBadge";
import { Dropdown } from "@/components/ui/Dropdown";
import { Tooltip } from "@/components/ui/Tooltip";
import { cn, formatBytes, formatDate, timestampToDate } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

interface ObjectTableRowProps {
  obj: Object$;
  isSelected: boolean;
  isInspected: boolean;
  isEditingLabels: boolean;
  editLabelsValue: string;
  useRelativeTime: boolean;
  visibleColumns: Set<string>;
  onToggleSelect: (id: string, e?: React.MouseEvent, key?: string) => void;
  onInspect: (key: string) => void;
  onStartInlineEdit: (obj: {
    objectId: string;
    objectKey: string;
    key: string;
  }) => void;
  onSaveInlineLabels: (id: string) => void;
  onCancelInlineEdit: () => void;
  onEditLabelsValueChange: (value: string) => void;
  onCopyToClipboard: (
    textOrPromise: string | Promise<string>,
    label: string,
  ) => void;
  onSoftDelete: (obj: {
    objectId: string;
    objectKey: string;
    key: string;
  }) => void;
  onHardDelete: (obj: {
    objectId: string;
    objectKey: string;
    key: string;
  }) => void;
  onCopy: (obj: { key: string; objectKey: string }) => void;
  onMove: (obj: { key: string; objectKey: string }) => void;
  onGenerateDownloadUrl: (
    key: string,
    objectKey: string,
  ) => Promise<{ url: string } | undefined>;
}

// Aligned with the global theme tokens (chart-N + destructive) so the
// state pills match badges, switches, and the rest of the UI.
const STATUS_STYLES: Record<number, string> = {
  1: "bg-chart-5/15 text-chart-5 ring-1 ring-chart-5/30", // PENDING — info / sky
  2: "bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30", // AVAILABLE — success / emerald
  3: "bg-destructive/15 text-destructive ring-1 ring-destructive/30", // FAILED
  4: "bg-muted text-muted-foreground ring-1 ring-border", // DELETED — neutral, lives in trash
};

const STATUS_LABELS: Record<number, string> = {
  1: "PENDING",
  2: "AVAILABLE",
  3: "FAILED",
  4: "DELETED",
};

export const ObjectTableRow = React.memo(function ObjectTableRow({
  obj,
  isSelected,
  isInspected,
  isEditingLabels,
  editLabelsValue,
  useRelativeTime,
  visibleColumns,
  onToggleSelect,
  onInspect,
  onStartInlineEdit,
  onSaveInlineLabels,
  onCancelInlineEdit,
  onEditLabelsValueChange,
  onCopyToClipboard,
  onSoftDelete,
  onHardDelete,
  onCopy,
  onMove,
  onGenerateDownloadUrl,
}: ObjectTableRowProps) {
  // Detail link is `<current-pathname>/<key>` — the row is rendered
  // inside the OK Objects tab (/tenants/<id>/object-keys/<ok>/
  // objects) since Phase 5, so detail = same path + storage key.
  // Falls back to the storage key alone for any future host that
  // mounts the row outside the Objects tab; that won't 404 silently
  // — the parent route will reject the path unambiguously.
  const pathname = usePathname() || "";
  const detailHref = `${pathname}/${encodeURIComponent(obj.key)}`;

  return (
    <tr
      key={obj.objectId}
      className={cn(
        "hover:bg-indigo-500/[0.03] transition-colors group cursor-pointer relative",
        isSelected && "bg-indigo-500/[0.05]",
        isInspected && "bg-indigo-500/[0.08] shadow-inner",
      )}
      onClick={(e) => onToggleSelect(obj.objectId, e, obj.key)}
    >
      <td className="px-6 py-4" onClick={(e) => e.stopPropagation()}>
        <input
          type="checkbox"
          aria-label={`Select object ${obj.key}`}
          className="rounded border-white/10 bg-white/5 text-indigo-600 focus:ring-offset-0 focus:ring-indigo-600 focus:ring-opacity-50 cursor-pointer w-4 h-4"
          checked={isSelected}
          onChange={() => onToggleSelect(obj.objectId)}
        />
      </td>

      {/* Name / ID */}
      {visibleColumns.has("key") && (
        <td className="px-6 py-4">
          <div className="flex items-center gap-3">
            <div
              className={cn(
                "w-8 h-8 rounded-lg flex items-center justify-center transition-all shadow-inner relative",
                isSelected || isInspected
                  ? "bg-indigo-500 text-white"
                  : "bg-indigo-500/10 text-indigo-400 group-hover:scale-110",
              )}
            >
              <DocumentIcon className="w-4 h-4" />
              {obj.contentType?.startsWith("image/") && (
                <div className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-emerald-500 border border-[#0A0C10] shadow-[0_0_8px_rgba(16,185,129,0.5)]" />
              )}
            </div>
            <Tooltip
              content={
                <div className="p-2 space-y-1">
                  <div className="text-xs font-semibold text-primary uppercase tracking-wider">
                    Object Reference
                  </div>
                  <div className="text-xs font-mono text-foreground/80">
                    {obj.objectId}
                  </div>
                </div>
              }
              position="right"
            >
              <div>
                <div
                  className="font-medium text-white group-hover:text-indigo-400 transition-colors truncate max-w-[200px]"
                  title={obj.key}
                >
                  {obj.key.split("/").pop()}
                </div>
                <div className="text-xs text-slate-600 font-mono">
                  {obj.objectId}
                </div>
              </div>
            </Tooltip>
          </div>
        </td>
      )}

      {/* Object Key — namespace badge */}
      {visibleColumns.has("object_key") && (
        <td className="hidden md:table-cell px-6 py-4">
          <span
            className={cn(
              T.code,
              "inline-flex items-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30 px-2 py-0.5",
            )}
          >
            {obj.objectKey || "—"}
          </span>
        </td>
      )}

      {/* Object Tags — every key:value pair as a chip cluster.
          Click anywhere starts inline edit; Enter saves, Esc cancels. */}
      {visibleColumns.has("object_tag") && (
        <td className="px-6 py-4">
          {isEditingLabels ? (
            <div
              className="flex items-center gap-2 p-1.5 rounded-xl bg-indigo-500/10 border border-indigo-500/30 animate-scale-in"
              onClick={(e) => e.stopPropagation()}
            >
              <TagIcon className="w-3 h-3 text-indigo-400" />
              <input
                autoFocus
                className="bg-transparent border-none p-0 text-xs text-white font-mono focus:ring-0 w-full min-w-[150px] placeholder:text-white/20"
                value={editLabelsValue}
                placeholder="key:val, key2:val..."
                onChange={(e) => onEditLabelsValueChange(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") onSaveInlineLabels(obj.objectId);
                  if (e.key === "Escape") onCancelInlineEdit();
                }}
              />
              <div className="flex items-center gap-1 border-l border-white/10 pl-2 ml-1">
                <button
                  onClick={() => onSaveInlineLabels(obj.objectId)}
                  className="p-1 px-1.5 rounded-lg bg-emerald-500 text-white hover:bg-emerald-400 transition-all active:scale-90"
                  title="Synchronize"
                >
                  <CheckIcon className="w-3 h-3" />
                </button>
                <button
                  onClick={() => onCancelInlineEdit()}
                  className="p-1 px-1.5 rounded-lg bg-white/5 text-slate-400 hover:text-white transition-all active:scale-90"
                  title="Abort"
                >
                  <XMarkIcon className="w-3 h-3" />
                </button>
              </div>
            </div>
          ) : (
            <div
              onClick={(e) => {
                e.stopPropagation();
                onStartInlineEdit(obj);
              }}
              className="cursor-text group/labels flex flex-wrap items-center gap-1"
              title="Click to edit tags"
            >
              {(() => {
                const tagEntries = Object.entries(obj.tags || {});
                if (tagEntries.length === 0) {
                  return (
                    <span className="text-xs italic text-muted-foreground">
                      no tags
                    </span>
                  );
                }
                // Render the special `object_tag` first (it's the primary
                // classification), then the rest as small key:value chips.
                const objectTag = obj.tags?.object_tag;
                const others = tagEntries.filter(([k]) => k !== "object_tag");
                return (
                  <>
                    {objectTag && (
                      <ObjectTagBadge objectTag={objectTag} linked />
                    )}
                    {others.map(([k, v]) => (
                      <span
                        key={k}
                        className={cn(
                          T.codeSmall,
                          "inline-flex items-center gap-1 rounded-md bg-chart-5/15 text-chart-5 ring-1 ring-chart-5/30 px-1.5 py-0.5",
                        )}
                      >
                        <span className="opacity-80">{k}</span>
                        <span className="opacity-50">:</span>
                        <span>{String(v)}</span>
                      </span>
                    ))}
                  </>
                );
              })()}
            </div>
          )}
        </td>
      )}

      {/* MIME Type */}
      {visibleColumns.has("mime") && (
        <td className="px-6 py-4 text-xs font-mono text-slate-400">
          {obj.contentType || "binary/octet-stream"}
        </td>
      )}

      {/* Size */}
      {visibleColumns.has("size") && (
        <td className="hidden md:table-cell px-6 py-4 text-right text-slate-400 font-mono text-xs whitespace-nowrap">
          {formatBytes(obj.sizeBytes)}
        </td>
      )}

      {/* Status */}
      {visibleColumns.has("status") && (
        <td className="px-6 py-4 whitespace-nowrap">
          <span
            className={`text-xs font-semibold uppercase tracking-wider px-2 py-0.5 rounded-full ${
              STATUS_STYLES[obj.state] || "bg-slate-500/10 text-slate-500"
            }`}
          >
            {STATUS_LABELS[obj.state] || "UNKNOWN"}
          </span>
        </td>
      )}

      {/* Created */}
      {visibleColumns.has("created") && (
        <td className="hidden sm:table-cell px-6 py-4 text-right text-slate-400 font-mono text-xs whitespace-nowrap">
          {obj.createdAt
            ? useRelativeTime
              ? formatDate(timestampToDate(obj.createdAt))
              : timestampToDate(obj.createdAt).toLocaleString()
            : "N/A"}
        </td>
      )}

      {/* Actions */}
      {visibleColumns.has("actions") && (
        <td className="px-6 py-4" onClick={(e) => e.stopPropagation()}>
          <div className="flex justify-end items-center">
            <Dropdown align="right" width="w-56">
              <Dropdown.Trigger
                className={cn(
                  "p-1.5 rounded-lg transition-all flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-accent",
                )}
                activeClassName="bg-primary text-primary-foreground shadow-lg shadow-primary/30"
              >
                <EllipsisVerticalIcon className="w-5 h-5" />
              </Dropdown.Trigger>
              <Dropdown.Menu className="py-1">
                <Dropdown.Item className="p-0">
                  <Link
                    href={detailHref}
                    className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors"
                  >
                    <EyeIcon className="w-4 h-4 text-primary" />
                    View Details
                  </Link>
                </Dropdown.Item>
                <Dropdown.Item
                  className="p-0"
                  onClick={() => onInspect(obj.key)}
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors">
                    <InformationCircleIcon className="w-4 h-4 text-primary" />
                    Quick Inspect
                  </div>
                </Dropdown.Item>
                <Dropdown.Item
                  className="p-0"
                  onClick={() => onStartInlineEdit(obj)}
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors">
                    <TagIcon className="w-4 h-4 text-primary" />
                    Apply Labels
                  </div>
                </Dropdown.Item>
                <Dropdown.Item
                  className="p-0"
                  onClick={() => onCopyToClipboard(obj.objectId, "Object ID")}
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors">
                    <ClipboardIcon className="w-4 h-4 text-muted-foreground" />
                    Copy ID
                  </div>
                </Dropdown.Item>
                <div className="h-px bg-border my-0.5" />
                <Dropdown.Item
                  className="p-0"
                  onClick={() => {
                    const urlPromise = onGenerateDownloadUrl(
                      obj.key,
                      obj.objectKey,
                    ).then((url) => {
                      if (!url?.url) throw new Error("Link generation failed");
                      return url.url;
                    });
                    onCopyToClipboard(urlPromise, "Download Link");
                  }}
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors">
                    <ArrowDownTrayIcon className="w-4 h-4 text-emerald-400" />
                    Copy Download Link
                  </div>
                </Dropdown.Item>
                <div className="h-px bg-border my-0.5" />
                <Dropdown.Item
                  className="p-0"
                  onClick={() =>
                    onCopy({ key: obj.key, objectKey: obj.objectKey })
                  }
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors">
                    <DocumentDuplicateIcon className="w-4 h-4 text-fuchsia-400" />
                    Copy Object
                  </div>
                </Dropdown.Item>
                <Dropdown.Item
                  className="p-0"
                  onClick={() =>
                    onMove({ key: obj.key, objectKey: obj.objectKey })
                  }
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:bg-accent transition-colors">
                    <ArrowRightCircleIcon className="w-4 h-4 text-cyan-400" />
                    Move Object
                  </div>
                </Dropdown.Item>
                <div className="h-px bg-border my-0.5" />
                <Dropdown.Item
                  className="p-0"
                  onClick={() => onSoftDelete(obj)}
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-foreground hover:text-destructive hover:bg-destructive/10 transition-colors">
                    <TrashIcon className="w-4 h-4 text-destructive/60" />
                    Soft Delete
                  </div>
                </Dropdown.Item>
                <Dropdown.Item
                  className="p-0"
                  onClick={() => onHardDelete(obj)}
                >
                  <div className="w-full flex items-center gap-3 px-3 py-1.5 text-xs font-bold text-destructive hover:bg-destructive/20 transition-colors">
                    <ExclamationTriangleIcon className="w-4 h-4" />
                    Hard Delete
                  </div>
                </Dropdown.Item>
              </Dropdown.Menu>
            </Dropdown>
          </div>
        </td>
      )}
    </tr>
  );
});

"use client";

// FolderTree — derives a directory hierarchy from a flat list of S3
// keys (split on "/") and lets the operator click into a folder. The
// caller passes the active prefix back as a controlled value so URL
// state can drive navigation:
//
//   <FolderTree
//     keys={objects.map(o => o.key)}
//     activePrefix={pathParam}
//     onSelect={(prefix) => router.push(`?prefix=${prefix}`)}
//   />
//
// The component is purely a renderer over the keys it's given —
// "what folders exist" comes from the data already on screen. For
// huge object counts, the parent should server-side-paginate; the
// tree shows whatever's present in the current page.
//
// Multi-segment Collection paths land here too: a key like
// `2026/q1/january/report.pdf` produces three nested folders.

import React, { useMemo, useState } from "react";
import {
  ChevronDownIcon,
  ChevronRightIcon,
  FolderIcon,
  FolderOpenIcon,
  HomeIcon,
} from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";

interface TreeNode {
  name: string;
  path: string; // accumulated path from root, no leading slash
  fileCount: number;
  folderCount: number;
  children: TreeNode[];
}

function buildTree(keys: string[]): TreeNode {
  const root: TreeNode = {
    name: "",
    path: "",
    fileCount: 0,
    folderCount: 0,
    children: [],
  };
  for (const key of keys) {
    const segments = key.split("/").filter(Boolean);
    if (segments.length === 0) continue;
    let cursor = root;
    // All segments except the last are folder hops; the last is the
    // file name (counted on the parent folder).
    for (let i = 0; i < segments.length - 1; i++) {
      const seg = segments[i];
      let child = cursor.children.find((c) => c.name === seg);
      if (!child) {
        const path = cursor.path ? `${cursor.path}/${seg}` : seg;
        child = {
          name: seg,
          path,
          fileCount: 0,
          folderCount: 0,
          children: [],
        };
        cursor.children.push(child);
        cursor.folderCount++;
      }
      cursor = child;
    }
    cursor.fileCount++;
  }
  return root;
}

export interface FolderTreeProps {
  /** Flat list of collections (without bucket prefix). */
  keys: string[];
  /** Currently-selected folder path (no leading slash). Empty = root. */
  activePrefix?: string;
  /** Called when operator clicks a folder. Empty string for root. */
  onSelect?: (prefix: string) => void;
  className?: string;
}

export function FolderTree({
  keys,
  activePrefix = "",
  onSelect,
  className,
}: FolderTreeProps) {
  const root = useMemo(() => buildTree(keys), [keys]);
  return (
    <nav aria-label="Folders" className={cn("space-y-0.5 text-sm", className)}>
      <FolderRow
        node={root}
        depth={0}
        isRoot
        activePrefix={activePrefix}
        onSelect={onSelect}
      />
    </nav>
  );
}

function FolderRow({
  node,
  depth,
  isRoot = false,
  activePrefix = "",
  onSelect,
}: {
  node: TreeNode;
  depth: number;
  isRoot?: boolean;
  activePrefix?: string;
  onSelect?: (prefix: string) => void;
}) {
  const isActive =
    (isRoot && activePrefix === "") || node.path === activePrefix;
  // Expand by default when the active prefix passes through this node.
  const startExpanded =
    isRoot ||
    activePrefix === node.path ||
    (node.path && activePrefix.startsWith(node.path + "/"));
  const [expanded, setExpanded] = useState(startExpanded);

  const Caret = expanded ? ChevronDownIcon : ChevronRightIcon;
  const Icon = isRoot ? HomeIcon : expanded ? FolderOpenIcon : FolderIcon;
  const total = node.fileCount + node.folderCount;

  const handleClick = () => {
    onSelect?.(node.path);
    if (!isRoot) setExpanded((v) => !v);
  };

  return (
    <div>
      <button
        type="button"
        onClick={handleClick}
        className={cn(
          "group flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-left text-xs transition-colors",
          isActive
            ? "bg-primary/10 font-medium text-foreground"
            : "text-muted-foreground hover:bg-muted hover:text-foreground",
        )}
        style={{ paddingLeft: `${depth * 12 + 6}px` }}
      >
        {node.children.length > 0 ? (
          <Caret className="size-3 shrink-0 opacity-60" />
        ) : (
          <span className="size-3 shrink-0" />
        )}
        <Icon className="size-3.5 shrink-0 text-chart-2" />
        <span className="truncate">{isRoot ? "(root)" : node.name}</span>
        {total > 0 && (
          <span
            className={cn(
              "ml-auto rounded px-1 font-mono text-[10px] tabular-nums",
              isActive
                ? "bg-primary/20 text-primary"
                : "bg-muted text-muted-foreground group-hover:bg-background",
            )}
          >
            {total}
          </span>
        )}
      </button>
      {expanded && node.children.length > 0 && (
        <div>
          {node.children
            .slice()
            .sort((a, b) => a.name.localeCompare(b.name))
            .map((c) => (
              <FolderRow
                key={c.path}
                node={c}
                depth={depth + 1}
                activePrefix={activePrefix}
                onSelect={onSelect}
              />
            ))}
        </div>
      )}
    </div>
  );
}

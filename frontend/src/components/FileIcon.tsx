"use client";

// FileIcon — renders a tinted icon for a file based on its MIME
// type / filename. Image kinds get a lazy thumbnail when a presigned
// URL is provided (caller's responsibility to mint it — UI doesn't
// know which presign endpoint to hit). Falls back to the generic
// type icon when thumb fails to load or isn't supplied.

import React, { useState } from "react";

import { cn } from "@/lib/utils";
import { classifyFile, iconForKind, tintForKind } from "@/lib/files/types";

export interface FileIconProps {
  contentType?: string;
  filename?: string;
  /** Optional presigned GET URL to render as a thumbnail when the
   *  file is an image. Cheap-and-fast preview without download. */
  thumbUrl?: string;
  size?: "sm" | "md" | "lg";
  className?: string;
}

const SIZE_PX = { sm: 16, md: 24, lg: 40 };
const SIZE_CLASS = { sm: "size-4", md: "size-6", lg: "size-10" };

export function FileIcon({
  contentType,
  filename,
  thumbUrl,
  size = "sm",
  className,
}: FileIconProps) {
  const kind = classifyFile(contentType, filename);
  const [thumbFailed, setThumbFailed] = useState(false);

  if (kind === "image" && thumbUrl && !thumbFailed) {
    return (
      <img
        src={thumbUrl}
        alt={filename || "image"}
        width={SIZE_PX[size]}
        height={SIZE_PX[size]}
        loading="lazy"
        decoding="async"
        onError={() => setThumbFailed(true)}
        className={cn(
          "shrink-0 rounded object-cover ring-1 ring-border/40",
          SIZE_CLASS[size],
          className,
        )}
      />
    );
  }

  // Pre-bound icon refs avoid the "create component in render"
  // lint trap from dynamic component dispatch — the picker returns
  // a stable React.ElementType from the shared file-types map.
  const IconComponent = iconForKind(kind) as React.ComponentType<{
    className?: string;
  }>;
  return (
    <IconComponent
      className={cn(SIZE_CLASS[size], "shrink-0", tintForKind(kind), className)}
    />
  );
}

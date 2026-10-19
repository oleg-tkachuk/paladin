"use client";

import { useCallback, useRef, useState } from "react";
import type React from "react";

/** The type a drag of files from the desktop carries. */
const FILES_TYPE = "Files";

/** Whether a drag carries files, rather than text or a link from the page. */
export function carriesFiles(
  types: readonly string[] | DOMStringList,
): boolean {
  return Array.from(types).includes(FILES_TYPE);
}

/**
 * A region files can be dropped on. `dragging` is true while files are held
 * over it, counted across its children: a dragleave from the region into a
 * child of it is not leaving. A drag of anything but files is left alone, so
 * text and links still drag within the page. Disabled, the region refuses the
 * drop and says nothing.
 */
export function useFileDrop({
  disabled = false,
  onFiles,
}: {
  disabled?: boolean;
  onFiles: (files: File[]) => void;
}) {
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);

  const reset = useCallback(() => {
    depth.current = 0;
    setDragging(false);
  }, []);

  const dropProps = {
    onDragEnter: (e: React.DragEvent) => {
      if (disabled || !carriesFiles(e.dataTransfer.types)) return;
      e.preventDefault();
      depth.current += 1;
      setDragging(true);
    },
    onDragOver: (e: React.DragEvent) => {
      if (disabled || !carriesFiles(e.dataTransfer.types)) return;
      // Without this the browser opens the file instead of dropping it here.
      e.preventDefault();
    },
    onDragLeave: (e: React.DragEvent) => {
      if (disabled || !carriesFiles(e.dataTransfer.types)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setDragging(false);
    },
    onDrop: (e: React.DragEvent) => {
      if (disabled || !carriesFiles(e.dataTransfer.types)) return;
      e.preventDefault();
      reset();
      const files = Array.from(e.dataTransfer.files);
      if (files.length > 0) onFiles(files);
    },
  };

  return { dragging, dropProps };
}

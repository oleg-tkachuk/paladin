"use client";

// UploadDropzone — a transparent overlay that listens for window-level
// drag events and surfaces a visual drop target. The actual upload
// pipeline is the caller's concern (PUT to a presigned URL, multipart,
// etc.); this component is purely the visual + event affordance.
//
// Wraps its children — typically a whole list page. While the user is
// dragging a file from the OS into the browser, an overlay appears
// over the children with the destination path + accept/cancel hints.
// On drop, the caller's `onFiles` handler receives the FileList.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { ArrowUpTrayIcon, FolderIcon } from "@heroicons/react/24/outline";

import { cn } from "@/lib/utils";

export interface UploadDropzoneProps {
  /** Human-readable destination shown in the overlay banner — e.g.
   *  `tenants/acme/collections/invoices/q1/`. */
  destinationLabel: string;
  /** Called when the user releases the drag on the overlay. */
  onFiles: (files: File[]) => void;
  /** Optional className on the outer wrapper. */
  className?: string;
  children: React.ReactNode;
  /** Disable handling entirely (e.g. modal open, permission-denied). */
  disabled?: boolean;
}

export function UploadDropzone({
  destinationLabel,
  onFiles,
  className,
  children,
  disabled,
}: UploadDropzoneProps) {
  const [dragActive, setDragActive] = useState(false);
  // Drag enter/leave events fire on every child element the cursor
  // crosses, so a counter tracks "are we still inside?" — only when
  // every nested enter has been matched by a leave do we hide the
  // overlay.
  const counter = useRef(0);

  useEffect(() => {
    if (disabled) return;
    // We listen on document so the dropzone catches drags entering
    // *any* part of the page, not just the visible region.
    const onDragEnter = (e: DragEvent) => {
      if (!e.dataTransfer?.types?.includes("Files")) return;
      counter.current++;
      setDragActive(true);
    };
    const onDragLeave = () => {
      counter.current = Math.max(0, counter.current - 1);
      if (counter.current === 0) setDragActive(false);
    };
    const onDragOver = (e: DragEvent) => {
      if (e.dataTransfer?.types?.includes("Files")) {
        e.preventDefault();
        e.dataTransfer.dropEffect = "copy";
      }
    };
    const onDrop = (e: DragEvent) => {
      counter.current = 0;
      setDragActive(false);
      if (!e.dataTransfer?.files?.length) return;
      e.preventDefault();
      onFiles(Array.from(e.dataTransfer.files));
    };

    document.addEventListener("dragenter", onDragEnter);
    document.addEventListener("dragleave", onDragLeave);
    document.addEventListener("dragover", onDragOver);
    document.addEventListener("drop", onDrop);
    return () => {
      document.removeEventListener("dragenter", onDragEnter);
      document.removeEventListener("dragleave", onDragLeave);
      document.removeEventListener("dragover", onDragOver);
      document.removeEventListener("drop", onDrop);
    };
  }, [disabled, onFiles]);

  const handlePickFiles = useCallback(() => {
    if (disabled) return;
    const input = document.createElement("input");
    input.type = "file";
    input.multiple = true;
    input.onchange = () => {
      if (input.files?.length) onFiles(Array.from(input.files));
    };
    input.click();
  }, [disabled, onFiles]);

  return (
    <div className={cn("relative", className)}>
      {children}
      {dragActive && !disabled && (
        <div
          aria-hidden
          className="pointer-events-none fixed inset-0 z-50 flex items-center justify-center bg-primary/10 backdrop-blur-sm"
        >
          <div className="rounded-lg border-2 border-dashed border-primary bg-background/95 px-6 py-5 text-center shadow-lg">
            <ArrowUpTrayIcon className="mx-auto size-10 text-primary" />
            <p className="mt-2 text-sm font-semibold">Drop to upload</p>
            <p className="mt-1 flex items-center justify-center gap-1 text-xs text-muted-foreground">
              <FolderIcon className="size-3" />
              <span className="font-mono">{destinationLabel}</span>
            </p>
          </div>
        </div>
      )}
      {/* Hidden helper exposing the file-picker fallback for keyboard
          users — caller's "Upload" button can wire onClick={handlePickFiles}
          via a forwarded ref/callback if needed. */}
      <button
        type="button"
        onClick={handlePickFiles}
        className="sr-only"
        aria-label="Pick files to upload"
        disabled={disabled}
      />
    </div>
  );
}

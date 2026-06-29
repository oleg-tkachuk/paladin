"use client";

// FilePreview — MIME-driven in-browser viewer. Renders inline:
//   - image/* — <img> against a presigned GET URL
//   - application/pdf — <iframe>
//   - text/*, application/json, application/yaml — fetched ≤1MB
//     and rendered in a <pre>
//   - everything else — "Download to view" CTA with file-size hint
//
// Stays out of the data-plane entirely: presigned URLs are the
// caller's responsibility. This component is "given a URL + a
// content-type, paint something sensible".
//
// Text preview caps at 1MB to keep the UI responsive — operators
// peeking at a stray binary won't accidentally lock the browser
// downloading a 4GB blob.

import { useQuery } from "@tanstack/react-query";
import {
  ArrowDownTrayIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { classifyFile, formatBytes } from "@/lib/files/types";

const TEXT_PREVIEW_LIMIT = 1_048_576; // 1 MiB

export interface FilePreviewProps {
  /** Presigned GET URL. Required for image/pdf/text rendering. */
  presignedUrl?: string;
  /** MIME type — preferred classifier. */
  contentType?: string;
  /** Filename fallback when MIME is generic / missing. */
  filename?: string;
  /** Size in bytes — drives the "too large to preview inline" hint
   *  for text classes. */
  sizeBytes?: number;
  className?: string;
}

export function FilePreview({
  presignedUrl,
  contentType,
  filename,
  sizeBytes,
  className,
}: FilePreviewProps) {
  const kind = classifyFile(contentType, filename);

  // Only fetch text-class previews — image/pdf/etc. load
  // declaratively below.
  const isTextual = kind === "text" || kind === "json" || kind === "code";
  const tooLarge =
    typeof sizeBytes === "number" && sizeBytes > TEXT_PREVIEW_LIMIT;

  // Stream the text preview through TanStack — keyed on the presigned URL;
  // the signal aborts the in-flight fetch on unmount / URL change (replaces
  // the manual `cancelled` guard). Disabled for non-textual / oversized /
  // urlless previews, so `text` stays null there.
  const previewQuery = useQuery({
    queryKey: ["filePreview", presignedUrl, isTextual, tooLarge],
    enabled: isTextual && !tooLarge && !!presignedUrl,
    retry: false,
    queryFn: async ({ signal }) => {
      const res = await fetch(presignedUrl!, { signal });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const reader = res.body?.getReader();
      if (!reader) {
        const body = await res.text();
        return body.slice(0, TEXT_PREVIEW_LIMIT);
      }
      // Streamed read with a hard byte cap so a Content-Length mis-report
      // can't pull the whole file.
      const decoder = new TextDecoder();
      let acc = "";
      let received = 0;
      while (received < TEXT_PREVIEW_LIMIT) {
        const { done, value } = await reader.read();
        if (done) break;
        received += value.byteLength;
        acc += decoder.decode(value, { stream: true });
      }
      acc += decoder.decode();
      return acc.slice(0, TEXT_PREVIEW_LIMIT);
    },
  });
  const text = previewQuery.data ?? null;
  const textError = previewQuery.error
    ? (previewQuery.error as Error).message
    : null;
  const textLoading = previewQuery.isFetching;

  if (!presignedUrl) {
    return (
      <div
        className={cn(
          "flex h-full items-center justify-center rounded border border-dashed border-border/60 text-xs text-muted-foreground",
          className,
        )}
      >
        No presigned URL — generate one to preview
      </div>
    );
  }

  // ── image ────────────────────────────────────────────────────────
  if (kind === "image") {
    return (
      <div
        className={cn(
          "flex h-full items-center justify-center overflow-hidden rounded border border-border/60 bg-muted/20",
          className,
        )}
      >
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img
          src={presignedUrl}
          alt={filename || "image preview"}
          className="max-h-full max-w-full object-contain"
        />
      </div>
    );
  }

  // ── pdf ──────────────────────────────────────────────────────────
  if (kind === "pdf") {
    return (
      <iframe
        src={presignedUrl}
        title={filename || "PDF preview"}
        className={cn(
          "h-full w-full rounded border border-border/60",
          className,
        )}
      />
    );
  }

  // ── text-ish ────────────────────────────────────────────────────
  if (isTextual) {
    if (tooLarge) {
      return (
        <DownloadFallback
          presignedUrl={presignedUrl}
          filename={filename}
          sizeBytes={sizeBytes}
          reason="too large to preview inline (>1 MiB)"
          className={className}
        />
      );
    }
    if (textLoading) {
      return (
        <div className={cn("space-y-2 p-3", className)}>
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-3 w-full" />
          ))}
        </div>
      );
    }
    if (textError) {
      return (
        <div
          className={cn(
            "flex items-center gap-2 rounded border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive",
            className,
          )}
        >
          <ExclamationTriangleIcon className="size-4 shrink-0" />
          Preview failed: {textError}
        </div>
      );
    }
    return (
      <pre
        className={cn(
          "h-full overflow-auto rounded border border-border/60 bg-muted/20 p-3 font-mono text-[11px] leading-relaxed",
          className,
        )}
      >
        {text}
      </pre>
    );
  }

  // ── everything else ─────────────────────────────────────────────
  return (
    <DownloadFallback
      presignedUrl={presignedUrl}
      filename={filename}
      sizeBytes={sizeBytes}
      className={className}
    />
  );
}

function DownloadFallback({
  presignedUrl,
  filename,
  sizeBytes,
  reason,
  className,
}: {
  presignedUrl: string;
  filename?: string;
  sizeBytes?: number;
  reason?: string;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex h-full flex-col items-center justify-center gap-3 rounded border border-dashed border-border/60 p-6 text-center",
        className,
      )}
    >
      <ArrowDownTrayIcon className="size-10 text-muted-foreground/60" />
      <div className="space-y-1">
        <p className="text-sm font-medium">{filename || "Object"}</p>
        <p className="text-xs text-muted-foreground">
          {reason || "No inline preview for this file type"}
          {typeof sizeBytes === "number" && ` · ${formatBytes(sizeBytes)}`}
        </p>
      </div>
      <Button size="sm" asChild>
        <a href={presignedUrl} download={filename || true}>
          <ArrowDownTrayIcon className="size-4" />
          Download
        </a>
      </Button>
    </div>
  );
}

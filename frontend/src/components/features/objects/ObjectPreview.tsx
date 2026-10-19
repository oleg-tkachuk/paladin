"use client";

import { DocumentIcon } from "@heroicons/react/24/outline";

import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import { Skeleton } from "@/components/ui/Skeleton";
import { useObjectPreview } from "@/hooks/useObjectPreview";
import { T } from "@/lib/ui/typography";

/** What the preview says when it has nothing to show. */
export const PREVIEW_UNAVAILABLE = "Preview unavailable";

/**
 * An object shown in place: an image, a text object's contents, or a PDF.
 * Fills the box its parent gives it; anything else shows its content type.
 */
export function ObjectPreview({
  object,
  downloadUrl,
}: {
  object: Object$;
  downloadUrl: PresignedUrl | null;
}) {
  const fileName = object.key.split("/").pop() || object.key;
  const preview = useObjectPreview(object, downloadUrl);

  if (preview.loading) return <Skeleton className="size-full" />;
  if (preview.kind === "image" && preview.src) {
    return (
      // A presigned URL on the store's origin: next/image would proxy it.
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={preview.src}
        alt={`Preview of ${fileName}`}
        className="size-full object-contain"
      />
    );
  }
  if (preview.kind === "text" && preview.text !== undefined) {
    return (
      <pre
        aria-label={`Contents of ${fileName}`}
        className="size-full overflow-auto whitespace-pre-wrap break-words p-3 text-left font-mono text-xs"
      >
        {preview.text}
      </pre>
    );
  }
  if (preview.kind === "pdf" && preview.src) {
    return (
      <iframe
        src={preview.src}
        title={`Preview of ${fileName}`}
        className="size-full border-0"
      />
    );
  }
  return (
    <div className="flex flex-col items-center gap-2 text-muted-foreground">
      <DocumentIcon className="size-10" />
      <span className={T.hint}>{object.contentType || "binary"}</span>
      {preview.failed ? (
        <span className={T.hint}>{PREVIEW_UNAVAILABLE}</span>
      ) : null}
    </div>
  );
}

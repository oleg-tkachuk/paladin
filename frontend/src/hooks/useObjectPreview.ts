"use client";

import { useQuery } from "@tanstack/react-query";

import { presignClient } from "@/lib/connect/client";
import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";
import { ObjectState, type Object$ } from "@/gen/paladin/data/v1/types_pb";
import { inlineDisposition } from "@/lib/download";
import { previewKind, type PreviewKind } from "@/lib/objectPreview";
import { msUntilStale } from "@/lib/upload/retry";
import { QUERY_STALE_MS } from "@/constants";

/** How long a preview URL stays usable; undefined without one. */
const lifetime = (url: PresignedUrl | undefined) =>
  url ? msUntilStale(url) : undefined;

export interface ObjectPreviewState {
  kind: PreviewKind | null;
  /** The URL an image or a PDF is shown from. */
  src: string | null;
  /** The object's text, for a text preview. */
  text: string | undefined;
  loading: boolean;
  failed: boolean;
}

/**
 * What the console can show of an object in place. An image uses the
 * download URL the page already holds; a text object is read through it; a
 * PDF gets a URL of its own, signed inline, since the download URL is signed
 * as an attachment and a frame would save it instead.
 */
export function useObjectPreview(
  object: Object$ | undefined,
  downloadUrl: PresignedUrl | null,
): ObjectPreviewState {
  const kind =
    object?.state === ObjectState.AVAILABLE
      ? previewKind(object.contentType, object.sizeBytes)
      : null;
  // A new version is new bytes: the version is part of every key below.
  const version = [object?.name, object?.resourceVersion];

  const text = useQuery<string>({
    queryKey: ["objectPreview", "text", ...version],
    enabled: kind === "text" && !!downloadUrl,
    retry: false,
    queryFn: async ({ signal }) => {
      const res = await fetch(downloadUrl!.url, { signal });
      if (!res.ok) throw new Error(`the store answered ${res.status}`);
      return res.text();
    },
  });

  const pdf = useQuery<PresignedUrl>({
    queryKey: ["objectPreview", "pdf", ...version],
    enabled: kind === "pdf" && !!object,
    retry: false,
    staleTime: (q) => lifetime(q.state.data) ?? QUERY_STALE_MS,
    refetchInterval: (q) => lifetime(q.state.data) ?? false,
    queryFn: async ({ signal }) => {
      const res = await presignClient.presignDownload(
        {
          name: object!.name,
          contentDisposition: inlineDisposition(object!.key),
        },
        { signal },
      );
      if (!res.downloadUrl) throw new Error("no preview URL was issued");
      return res.downloadUrl;
    },
  });

  switch (kind) {
    case "image":
      return {
        kind,
        src: downloadUrl?.url ?? null,
        text: undefined,
        loading: false,
        failed: !downloadUrl,
      };
    case "text":
      return {
        kind,
        src: null,
        text: text.data,
        loading: text.isPending && !!downloadUrl,
        failed: text.isError || !downloadUrl,
      };
    case "pdf":
      return {
        kind,
        src: pdf.data?.url ?? null,
        text: undefined,
        loading: pdf.isPending,
        failed: pdf.isError,
      };
    default:
      return {
        kind: null,
        src: null,
        text: undefined,
        loading: false,
        failed: false,
      };
  }
}

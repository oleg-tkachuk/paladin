"use client";

import { ArrowUturnLeftIcon, TrashIcon } from "@heroicons/react/24/outline";
import { useQuery } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";

import { ObjectTagBadge } from "@/components/features/ObjectTagBadge";
import { Bytes } from "@/components/ui/Bytes";
import { DetailPanel } from "@/components/ui/DetailPanel";
import { RecordField, RecordFields } from "@/components/ui/RecordDialog";
import { Button } from "@/components/ui/button";
import { useRefreshSignal } from "@/context/RefreshContext";
import { ObjectState, type Object$ } from "@/gen/paladin/data/v1/types_pb";
import { objectClient } from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { formatDateTime } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { timestampToDate } from "@/lib/utils";

/** What an object with no content type is served as. */
export const DEFAULT_CONTENT_TYPE = "binary/octet-stream";

/** An object's resource name, as GetObject takes it. */
export const objectResourceName = (collectionName: string, objectId: string) =>
  `${collectionName}/objects/${objectId}`;

/** The last segment of an object's key, as its row names it. */
export const objectBaseName = (key: string) => key.split("/").pop() ?? key;

/**
 * The trashed object a link names, read by id when the page's list does not
 * hold it: the trash loads a page at a time, so after a reload the linked
 * object may be on a page not read yet. null is an object that is gone, or
 * no longer in the trash.
 */
export function useTrashedObject(
  collectionName: string,
  objectId: string | null,
  listed: Object$ | null,
) {
  // The shared signal in the key: a restore or purge reads it again.
  const refreshSignal = useRefreshSignal("objects");
  const name = objectId ? objectResourceName(collectionName, objectId) : "";
  const query = useQuery({
    queryKey: ["trashedObject", name, refreshSignal] as const,
    enabled: !!objectId && !!collectionName && !listed,
    retry: false, // NotFound is an answer; a retry will not change it.
    queryFn: async ({ signal }): Promise<Object$ | null> => {
      try {
        const obj = await objectClient.getObject(
          { name },
          notFoundIsAnswer({ signal }),
        );
        return obj.state === ObjectState.DELETED ? obj : null;
      } catch (err) {
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return null;
        }
        throw err;
      }
    },
  });
  return {
    object: listed ?? query.data ?? null,
    loading: !listed && query.isLoading,
    failed: !listed && query.isError,
  };
}

/**
 * One trashed object, in a panel beside the list, with its restore and purge
 * along the bottom; the page confirms both.
 */
export function TrashedObjectPanel({
  id,
  object,
  loading,
  failed,
  actionsDisabled,
  disabledReason,
  onClose,
  onRestore,
  onPurge,
}: {
  /** The object to show; null keeps the panel closed. */
  id: string | null;
  object: Object$ | null;
  loading: boolean;
  failed: boolean;
  /** Restore and purge are held: the tenant refuses changes, or one runs. */
  actionsDisabled: boolean;
  /** Why they are held, as the buttons' title. */
  disabledReason?: string;
  onClose: () => void;
  onRestore: (object: Object$) => void;
  onPurge: (object: Object$) => void;
}) {
  return (
    <DetailPanel
      open={!!id}
      onClose={onClose}
      rowId={id}
      title={object ? objectBaseName(object.key) || "Object" : "Object"}
      identifier={id ?? undefined}
      footer={
        object ? (
          <>
            <Button
              size="sm"
              variant="outline"
              disabled={actionsDisabled}
              title={disabledReason}
              onClick={() => onRestore(object)}
            >
              <ArrowUturnLeftIcon className="size-4" />
              Restore
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={actionsDisabled}
              title={disabledReason}
              onClick={() => onPurge(object)}
            >
              <TrashIcon className="size-4" />
              Purge
            </Button>
          </>
        ) : null
      }
    >
      {object ? (
        <RecordFields>
          <RecordField label="Key" mono>
            {object.key}
          </RecordField>
          <RecordField label="Object tag">
            <ObjectTagBadge
              objectTag={object.tags?.object_tag}
              linked={!!object.tags?.object_tag}
            />
          </RecordField>
          <RecordField label="Content type" mono>
            {object.contentType || DEFAULT_CONTENT_TYPE}
          </RecordField>
          <RecordField label="Size" mono>
            <Bytes value={object.sizeBytes} />
          </RecordField>
          <RecordField label="Deleted">
            {object.terminatedAt
              ? formatDateTime(timestampToDate(object.terminatedAt))
              : "—"}
          </RecordField>
          <RecordField label="Version" mono>
            {object.resourceVersion || "—"}
          </RecordField>
        </RecordFields>
      ) : (
        <p className={T.hint}>
          {loading
            ? "Reading the object…"
            : failed
              ? "The object could not be read."
              : "No object with this id is in the trash: it was purged or restored."}
        </p>
      )}
    </DetailPanel>
  );
}

"use client";

import React from "react";
import { useQuery } from "@tanstack/react-query";
import { ClockIcon } from "@heroicons/react/24/outline";

import { objectClient } from "@/lib/connect/client";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import { Card } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { shortId } from "./_versions";
import { VersionRow } from "./VersionRow";
import { VersionDetailsDialog } from "./VersionDetailsDialog";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { errorMessage } from "@/hooks/errorContract";

interface ObjectVersionsTabProps {
  /** Active when the parent tab is "versions" — drives lazy fetch. */
  active: boolean;
  /** The current Object — used for the parent name + OCC token. */
  object: Object$;
  /** Callback to refresh the Object after a successful restore. */
  onObjectChanged: () => void | Promise<unknown>;
}

export function ObjectVersionsTab({
  active,
  object,
  onObjectChanged,
}: ObjectVersionsTabProps) {
  const { showNotification } = useNotification();

  const [detailsVersion, setDetailsVersion] =
    React.useState<ObjectVersion | null>(null);
  const [restoreVersion, setRestoreVersion] =
    React.useState<ObjectVersion | null>(null);
  const [isRestoring, setIsRestoring] = React.useState(false);

  // Lazy: enabled:active fetches on first activation; staleTime:Infinity keeps
  // it from refetching on tab swap (the old `hasFetched` guard). refetch()
  // after a restore still forces a reload regardless of staleTime.
  const versionsQuery = useQuery({
    queryKey: ["objectVersions", object.name],
    enabled: active && !!object.name,
    staleTime: Infinity,
    retry: false, // queryFn toasts; a retry would double-toast.
    queryFn: async ({ signal }) => {
      try {
        const res = await objectClient.listObjectVersions(
          { parent: object.name },
          { signal },
        );
        // Newest first — defensive sort if the server already ordered.
        return [...res.versions].sort((a, b) => {
          const aSec = Number(a.createdAt?.seconds ?? 0n);
          const bSec = Number(b.createdAt?.seconds ?? 0n);
          return bSec - aSec;
        });
      } catch (err: unknown) {
        showNotification({
          type: "error",
          title: "Failed to load versions",
          message: (err as Error).message || "Could not load version history.",
        });
        throw err;
      }
    },
  });
  // null until the first fetch resolves — preserves the `!versions` skeleton gate.
  const versions = versionsQuery.data ?? null;
  const loading = versionsQuery.isFetching;
  const fetchVersions = () => versionsQuery.refetch();

  const handleConfirmRestore = async () => {
    if (!restoreVersion) return;
    setIsRestoring(true);
    try {
      await objectClient.restoreObjectVersion({
        name: restoreVersion.name,
        resourceVersion: object.resourceVersion,
      });
      showNotification({
        type: "success",
        title: "Version restored",
        message: `Restored version ${shortId(restoreVersion.versionId, 10)}.`,
      });
      setRestoreVersion(null);
      // Refresh both the parent object (current pointer + resourceVersion)
      // and the version list (is_current flags shift).
      await onObjectChanged();
      await fetchVersions();
    } catch (err: unknown) {
      const e = err as Error;
      showNotification({
        type: "error",
        title: "Restore failed",
        message: e.message || "Could not restore this version.",
      });
    } finally {
      setIsRestoring(false);
    }
  };

  // ─── Loading ─────────────────────────────────────────────────────
  if (loading && !versions) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-20 w-full" />
        ))}
      </div>
    );
  }

  // ─── Failed ──────────────────────────────────────────────────────
  // "No historical versions yet" for a failed read hides versions that exist.
  if (versionsQuery.isError && !versionsQuery.data) {
    return (
      <Card className="p-12">
        <ListLoadError
          what="Versions"
          reason={errorMessage(versionsQuery.error)}
          onRetry={() => void versionsQuery.refetch()}
        />
      </Card>
    );
  }

  // ─── Empty ───────────────────────────────────────────────────────
  const list = versions ?? [];
  const historyCount = list.filter((v) => !v.isCurrent).length;
  if (list.length === 0 || historyCount === 0) {
    return (
      <Card className="flex flex-col items-center gap-3 p-12 text-center">
        <ClockIcon className="size-10 text-muted-foreground" />
        <div className="text-sm font-medium">No historical versions yet</div>
        <p className={cn(T.helper, "max-w-md")}>
          Versioning records every successful upload when the parent Collection
          has versioning enabled.
        </p>
      </Card>
    );
  }

  // ─── Loaded ──────────────────────────────────────────────────────
  return (
    <>
      <div className="space-y-3">
        {list.map((v) => (
          <VersionRow
            key={v.name || v.versionId}
            version={v}
            onView={() => setDetailsVersion(v)}
            onRestore={() => setRestoreVersion(v)}
          />
        ))}
      </div>

      <VersionDetailsDialog
        version={detailsVersion}
        open={detailsVersion !== null}
        onOpenChange={(next) => {
          if (!next) setDetailsVersion(null);
        }}
      />

      <ConfirmModal
        isOpen={restoreVersion !== null}
        onClose={() => {
          if (!isRestoring) setRestoreVersion(null);
        }}
        onConfirm={handleConfirmRestore}
        title="Restore version?"
        message={
          restoreVersion
            ? `This version (${shortId(restoreVersion.versionId, 12)}) will become the new current. The current version will be kept as a non-current version. Object lock state is preserved.`
            : ""
        }
        type="warning"
        confirmText="Restore"
        loading={isRestoring}
      />
    </>
  );
}

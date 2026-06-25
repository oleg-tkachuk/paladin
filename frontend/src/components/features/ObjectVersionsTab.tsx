"use client";

import React from "react";
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

  const [versions, setVersions] = React.useState<ObjectVersion[] | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [hasFetched, setHasFetched] = React.useState(false);

  const [detailsVersion, setDetailsVersion] =
    React.useState<ObjectVersion | null>(null);
  const [restoreVersion, setRestoreVersion] =
    React.useState<ObjectVersion | null>(null);
  const [isRestoring, setIsRestoring] = React.useState(false);

  const fetchVersions = React.useCallback(async () => {
    if (!object.name) return;
    setLoading(true);
    try {
      const res = await objectClient.listObjectVersions({
        parent: object.name,
      });
      // Newest first — defensive sort if the server already ordered.
      const sorted = [...res.versions].sort((a, b) => {
        const aSec = Number(a.createdAt?.seconds ?? 0n);
        const bSec = Number(b.createdAt?.seconds ?? 0n);
        return bSec - aSec;
      });
      setVersions(sorted);
    } catch (err: unknown) {
      const e = err as Error;
      showNotification({
        type: "error",
        title: "Failed to load versions",
        message: e.message || "Could not load version history.",
      });
      setVersions([]);
    } finally {
      setLoading(false);
      setHasFetched(true);
    }
  }, [object.name, showNotification]);

  // Lazy: fetch only on first activation. Don't refetch on tab swap.
  React.useEffect(() => {
    if (active && !hasFetched && !loading) {
      void fetchVersions();
    }
  }, [active, hasFetched, loading, fetchVersions]);

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

  // ─── Empty ───────────────────────────────────────────────────────
  const list = versions ?? [];
  const historyCount = list.filter((v) => !v.isCurrent).length;
  if (list.length === 0 || historyCount === 0) {
    return (
      <Card className="flex flex-col items-center gap-3 p-12 text-center">
        <ClockIcon className="size-10 text-muted-foreground" />
        <div className="text-sm font-medium">No historical versions yet</div>
        <p className={cn(T.helper, "max-w-md")}>
          Versioning records every successful upload when the parent ObjectKey
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

"use client";

import React from "react";
import { ClockIcon, EyeIcon, ArrowPathIcon } from "@heroicons/react/24/outline";

import { objectClient } from "@/lib/connect/client";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { Separator } from "@/components/ui/separator";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useNotification } from "@/components/ui/Notification";
import { T } from "@/lib/ui/typography";
import { cn, formatBytes } from "@/lib/utils";

// ─── Helpers ─────────────────────────────────────────────────────────

function formatTimestampSeconds(seconds: bigint | undefined): string {
  if (!seconds) return "—";
  try {
    return new Date(Number(seconds) * 1000).toLocaleString();
  } catch {
    return "—";
  }
}

function shortId(value: string, head = 10): string {
  if (!value) return "";
  if (value.length <= head + 3) return value;
  return `${value.slice(0, head)}…`;
}

function checksumDisplay(
  cs: ObjectVersion["checksum"],
  truncate = true,
): string {
  if (!cs || !cs.algorithm) return "—";
  const algo = cs.algorithm.toLowerCase();
  const val = truncate ? shortId(cs.value, 16) : cs.value;
  return `${algo}:${val}`;
}

// ─── Detail dialog ───────────────────────────────────────────────────

function DlRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-0.5">
      <dt className={T.label}>{label}</dt>
      <dd className="flex min-w-0 items-start gap-2 break-all">{children}</dd>
    </div>
  );
}

function VersionDetailsDialog({
  version,
  open,
  onOpenChange,
}: {
  version: ObjectVersion | null;
  open: boolean;
  onOpenChange: (next: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Version details</DialogTitle>
          <DialogDescription>
            Read-only snapshot for this object version.
          </DialogDescription>
        </DialogHeader>
        {version ? (
          <dl className="space-y-3">
            <DlRow label="Version ID">
              <span className={T.codeSmall}>{version.versionId}</span>
              {version.isCurrent ? (
                <Badge variant="success">current</Badge>
              ) : null}
            </DlRow>
            <DlRow label="Resource Name">
              <span className={T.codeSmall}>{version.name}</span>
            </DlRow>
            <DlRow label="Object UUID">
              <span className={T.codeSmall}>{version.objectId}</span>
            </DlRow>
            <Separator />
            <DlRow label="Created">
              <span className={T.codeSmall}>
                {formatTimestampSeconds(version.createdAt?.seconds)}
              </span>
            </DlRow>
            <DlRow label="Size">
              <span className={T.codeSmall}>
                {formatBytes(version.sizeBytes)}
              </span>
            </DlRow>
            <DlRow label="Content Type">
              <span className={T.codeSmall}>{version.contentType || "—"}</span>
            </DlRow>
            <DlRow label="ETag">
              <span className={T.codeSmall}>{version.etag || "—"}</span>
            </DlRow>
            <DlRow label="Checksum">
              <span className={T.codeSmall}>
                {checksumDisplay(version.checksum, false)}
              </span>
            </DlRow>
            <DlRow label="S3 Key">
              <span className={T.codeSmall}>{version.s3Key || "—"}</span>
            </DlRow>
            <DlRow label="Delete Marker">
              <span className={T.codeSmall}>
                {version.isDeleteMarker ? "yes" : "no"}
              </span>
            </DlRow>
            <Separator />
            <DlRow label="Metadata">
              {Object.keys(version.metadata || {}).length === 0 ? (
                <span className={T.hint}>—</span>
              ) : (
                <div className="flex flex-col gap-1">
                  {Object.entries(version.metadata).map(([k, v]) => (
                    <span key={k} className={T.codeSmall}>
                      {k} = {v}
                    </span>
                  ))}
                </div>
              )}
            </DlRow>
            <DlRow label="Tags">
              {Object.keys(version.tags || {}).length === 0 ? (
                <span className={T.hint}>—</span>
              ) : (
                <div className="flex flex-col gap-1">
                  {Object.entries(version.tags).map(([k, v]) => (
                    <span key={k} className={T.codeSmall}>
                      {k} = {v}
                    </span>
                  ))}
                </div>
              )}
            </DlRow>
          </dl>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

// ─── Single timeline row ─────────────────────────────────────────────

function VersionRow({
  version,
  onView,
  onRestore,
}: {
  version: ObjectVersion;
  onView: () => void;
  onRestore: () => void;
}) {
  const dotClass = version.isCurrent ? "bg-chart-2" : "bg-muted-foreground/40";
  return (
    <Card className="p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-1 items-start gap-3">
          <span className={cn("mt-1.5", T.pillDot, dotClass)} />
          <div className="min-w-0 flex-1 space-y-1.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className={T.code}>
                {formatTimestampSeconds(version.createdAt?.seconds)}
              </span>
              {version.isCurrent ? (
                <Badge variant="success">current</Badge>
              ) : null}
              {version.isDeleteMarker ? (
                <Badge variant="warning">delete marker</Badge>
              ) : null}
            </div>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
              <span className={T.codeSmall}>
                <span className={T.label}>id </span>
                {shortId(version.versionId, 12)}
              </span>
              <span className={T.codeSmall}>
                <span className={T.label}>size </span>
                {formatBytes(version.sizeBytes)}
              </span>
            </div>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
              <span className={cn(T.codeSmall, "break-all")}>
                <span className={T.label}>etag </span>
                {shortId(version.etag, 14)}
              </span>
              <span className={cn(T.codeSmall, "break-all")}>
                <span className={T.label}>checksum </span>
                {checksumDisplay(version.checksum)}
              </span>
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2 sm:ml-3">
          <Button variant="outline" size="sm" onClick={onView}>
            <EyeIcon className="size-4" />
            View
          </Button>
          {!version.isCurrent && !version.isDeleteMarker ? (
            <Button variant="outline" size="sm" onClick={onRestore}>
              <ArrowPathIcon className="size-4" />
              Restore
            </Button>
          ) : null}
        </div>
      </div>
    </Card>
  );
}

// ─── Main tab component ──────────────────────────────────────────────

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

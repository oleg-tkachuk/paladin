import React from "react";

import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { T } from "@/lib/ui/typography";
import { formatBytes } from "@/lib/utils";

import { formatTimestampSeconds, checksumDisplay } from "./_versions";

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

/**
 * Read-only version details dialog, extracted from ObjectVersionsTab. Pure
 * presentation over a single ObjectVersion; the parent owns open state.
 */
export function VersionDetailsDialog({
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
              <span className={T.codeSmall}>{version.storagePath || "—"}</span>
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

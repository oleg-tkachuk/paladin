import { EyeIcon, ArrowPathIcon } from "@heroicons/react/24/outline";

import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { useTenantChangesBlocked } from "@/app/tenants/[id]/tenant-context";
import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";
import { cn, formatBytes } from "@/lib/utils";

import { formatTimestampSeconds, shortId, checksumDisplay } from "./_versions";

/**
 * One timeline row in the object version history, extracted from
 * ObjectVersionsTab. Presentational: the parent supplies onView / onRestore;
 * Restore is offered only for non-current, non-delete-marker versions.
 */
export function VersionRow({
  version,
  onView,
  onRestore,
}: {
  version: ObjectVersion;
  onView: () => void;
  onRestore: () => void;
}) {
  const changesBlocked = useTenantChangesBlocked();
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
            <Button
              variant="outline"
              size="sm"
              disabled={Boolean(changesBlocked)}
              title={changesBlocked ?? undefined}
              onClick={onRestore}
            >
              <ArrowPathIcon className="size-4" />
              Restore
            </Button>
          ) : null}
        </div>
      </div>
    </Card>
  );
}

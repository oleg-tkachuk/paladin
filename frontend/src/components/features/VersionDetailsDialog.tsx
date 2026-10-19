import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import { Badge } from "@/components/ui/badge";
import { RecordDialog, RecordField } from "@/components/ui/RecordDialog";
import { T } from "@/lib/ui/typography";

import { formatTimestampSeconds, checksumDisplay } from "./_versions";
import { Bytes } from "@/components/ui/Bytes";

/** A map's entries one per line, or a dash when it has none. */
function Pairs({ pairs }: { pairs: Record<string, string> | undefined }) {
  const entries = Object.entries(pairs || {});
  if (entries.length === 0) return <span className={T.hint}>—</span>;
  return (
    <div className="flex flex-col gap-1">
      {entries.map(([k, v]) => (
        <span key={k}>
          {k} = {v}
        </span>
      ))}
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
    <RecordDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Version details"
      subtitle={version?.versionId}
    >
      {version ? (
        <>
          <RecordField label="Version ID" mono>
            {version.versionId}
            {version.isCurrent ? (
              <Badge variant="success" className="ml-2">
                current
              </Badge>
            ) : null}
          </RecordField>
          <RecordField label="Resource name" mono>
            {version.name}
          </RecordField>
          <RecordField label="Object UUID" mono>
            {version.objectId}
          </RecordField>
          <RecordField label="Created" mono>
            {formatTimestampSeconds(version.createdAt?.seconds)}
          </RecordField>
          <RecordField label="Size" mono>
            <Bytes value={version.sizeBytes} />
          </RecordField>
          <RecordField label="Content type" mono>
            {version.contentType || "—"}
          </RecordField>
          <RecordField label="ETag" mono>
            {version.etag || "—"}
          </RecordField>
          <RecordField label="Checksum" mono>
            {checksumDisplay(version.checksum, false)}
          </RecordField>
          <RecordField label="S3 key" mono>
            {version.storagePath || "—"}
          </RecordField>
          <RecordField label="Delete marker">
            {version.isDeleteMarker ? "yes" : "no"}
          </RecordField>
          <RecordField label="Metadata" mono>
            <Pairs pairs={version.metadata} />
          </RecordField>
          <RecordField label="Tags" mono>
            <Pairs pairs={version.tags} />
          </RecordField>
        </>
      ) : null}
    </RecordDialog>
  );
}

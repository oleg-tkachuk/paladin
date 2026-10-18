"use client";

// AuditEntryDetailDialog — the single-entry drill-down for an audit row.
// The list (ListAuditLog) already returns most columns, but the heavy
// before/after JSON snapshots are only worth fetching on demand — so a row
// click opens this dialog, which re-reads the canonical record via
// GetAuditLogEntry (by entry id) and renders the full field set, including
// the sanitized before/after state diffs the table doesn't show.

import { useQuery } from "@tanstack/react-query";

import { auditClient } from "@/lib/connect/client";
import {
  RecordBlock,
  RecordDialog,
  RecordField,
} from "@/components/ui/RecordDialog";
import { Skeleton } from "@/components/ui/Skeleton";
import { ActorName } from "@/components/features/audit/ActorName";
import { Timestamp } from "@/components/Timestamp";
import { errorMessage } from "@/hooks/errorContract";
import { isUuid } from "@/lib/resources/tenant-resolve";
import { T } from "@/lib/ui/typography";

// Decode a bytes state-snapshot to a display string. The backend stores
// sanitized JSON; pretty-print it when it parses, else show the raw text.
// Empty → null so the section is skipped entirely.
function decodeSnapshot(bytes: Uint8Array | undefined): string | null {
  if (!bytes || bytes.length === 0) return null;
  const raw = new TextDecoder().decode(bytes);
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

export function AuditEntryDetailDialog({
  entryId,
  onOpenChange,
}: {
  entryId: string | null;
  onOpenChange: (open: boolean) => void;
}) {
  const open = entryId !== null;
  const { data, isLoading, error } = useQuery({
    queryKey: ["auditEntry", entryId],
    queryFn: () => auditClient.getAuditLogEntry({ entryId: entryId as string }),
    enabled: open,
  });

  const before = decodeSnapshot(data?.beforeJson);
  const after = decodeSnapshot(data?.afterJson);

  return (
    <RecordDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Audit entry"
      subtitle={entryId ?? undefined}
    >
      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-6 w-full" />
          <Skeleton className="h-6 w-3/4" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : error ? (
        <p className="text-destructive">
          {errorMessage(error, "Failed to load")}
        </p>
      ) : data ? (
        <>
          <RecordField label="When" mono>
            <Timestamp ts={data.at} />
          </RecordField>
          <RecordField label="Action" mono>
            {data.action || "—"}
          </RecordField>
          <RecordField label="Actor">
            <ActorName
              subject={data.actorSubject}
              tenantId={data.actorTenantId}
              fallback="system"
            />
          </RecordField>
          {/* Only a user's id resolves to a name; anything else — a
              system actor, a token — is shown as recorded above already. */}
          {isUuid(data.actorSubject) && (
            <RecordField label="Actor id" mono>
              {data.actorSubject}
            </RecordField>
          )}
          {data.actorTenantId && (
            <RecordField label="Tenant" mono>
              {data.actorTenantId}
            </RecordField>
          )}
          {data.actorAudience && (
            <RecordField label="Audience" mono>
              {data.actorAudience}
            </RecordField>
          )}
          <RecordField label="Resource" mono>
            {data.resourceName || "—"}
          </RecordField>
          {data.requestId && (
            <RecordField label="Request" mono>
              {data.requestId}
            </RecordField>
          )}
          {data.sourceIp && (
            <RecordField label="Source IP" mono>
              {data.sourceIp}
            </RecordField>
          )}
          {data.capabilityId && (
            <RecordField label="Capability" mono>
              {data.capabilityId}
            </RecordField>
          )}
          {data.relayedBy && (
            <RecordField label="Relayed by" mono>
              {data.relayedBy}
            </RecordField>
          )}
          {data.errorMessage && (
            <RecordField label="Error">
              <span className="text-destructive">{data.errorMessage}</span>
            </RecordField>
          )}
          {before && <RecordBlock label="Before">{before}</RecordBlock>}
          {after && <RecordBlock label="After">{after}</RecordBlock>}
          {!before && !after && (
            <p className={T.hint}>
              No state snapshot recorded for this action.
            </p>
          )}
        </>
      ) : null}
    </RecordDialog>
  );
}

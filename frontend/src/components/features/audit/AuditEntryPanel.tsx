"use client";

// The single-entry drill-down for an audit row, beside the log. The list
// (ListAuditLog) already returns most columns, but the heavy before/after
// JSON snapshots are only worth fetching on demand — so a row opens this
// panel, which re-reads the canonical record via GetAuditLogEntry (by entry
// id) and shows the full field set, what the action changed as a diff of the
// sanitised snapshots, and the snapshots themselves.

import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";

import { auditClient } from "@/lib/connect/client";
import { DetailPanel } from "@/components/ui/DetailPanel";
import { DiffView } from "@/components/ui/DiffView";
import {
  RecordBlock,
  RecordField,
  RecordFields,
} from "@/components/ui/RecordDialog";
import { Skeleton } from "@/components/ui/Skeleton";
import { ActorName } from "@/components/features/audit/ActorName";
import { Timestamp } from "@/components/Timestamp";
import { errorMessage } from "@/hooks/errorContract";
import { isUuid } from "@/lib/resources/tenant-resolve";
import { T } from "@/lib/ui/typography";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";

/** The panel's tabs, in order; the first opens by default. */
export const AUDIT_ENTRY_TABS = ["summary", "changes", "snapshots"] as const;
export type AuditEntryTab = (typeof AUDIT_ENTRY_TABS)[number];

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

const NO_SNAPSHOT = "No state snapshot recorded for this action.";

export function AuditEntryPanel({
  entryId,
  tab,
  onTabChange,
  onClose,
}: {
  /** The entry to show; null keeps the panel closed. */
  entryId: string | null;
  tab: AuditEntryTab;
  onTabChange: (tab: AuditEntryTab) => void;
  onClose: () => void;
}) {
  const open = entryId !== null;
  const { data, isLoading, error } = useQuery({
    queryKey: ["auditEntry", entryId],
    queryFn: () => auditClient.getAuditLogEntry({ entryId: entryId as string }),
    enabled: open,
  });

  const before = decodeSnapshot(data?.beforeJson);
  const after = decodeSnapshot(data?.afterJson);

  const body = (content: (entry: AuditLogEntry) => ReactNode) =>
    isLoading ? (
      <div className="space-y-2">
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-3/4" />
        <Skeleton className="h-24 w-full" />
      </div>
    ) : error ? (
      <p className="text-sm text-destructive">
        {errorMessage(error, "Failed to load")}
      </p>
    ) : data ? (
      content(data)
    ) : null;

  return (
    <DetailPanel
      open={open}
      onClose={onClose}
      rowId={entryId}
      title="Audit entry"
      identifier={entryId ?? undefined}
      tab={tab}
      onTabChange={(t) => onTabChange(t as AuditEntryTab)}
      tabs={[
        {
          value: "summary",
          label: "Summary",
          content: body((entry) => <Summary entry={entry} />),
        },
        {
          value: "changes",
          label: "Changes",
          content: body(() =>
            before || after ? (
              // A create has no before and a delete no after: the diff reads
              // as all added, or all removed.
              <DiffView before={before ?? ""} after={after ?? ""} />
            ) : (
              <p className={T.hint}>{NO_SNAPSHOT}</p>
            ),
          ),
        },
        {
          value: "snapshots",
          label: "Snapshots",
          content: body(() => (
            <RecordFields>
              {before && <RecordBlock label="Before">{before}</RecordBlock>}
              {after && <RecordBlock label="After">{after}</RecordBlock>}
              {!before && !after && <p className={T.hint}>{NO_SNAPSHOT}</p>}
            </RecordFields>
          )),
        },
      ]}
    />
  );
}

function Summary({ entry: data }: { entry: AuditLogEntry }) {
  return (
    <RecordFields>
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
      {/* Only a user's id resolves to a name; anything else — a system
          actor, a token — is shown as recorded above already. */}
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
    </RecordFields>
  );
}

"use client";

// The single-entry drill-down for an audit row, beside the log. The list
// (ListAuditLog) already returns most columns, but the request an action was
// made with is only worth fetching on demand — so a row opens this panel,
// which re-reads the canonical record via GetAuditLogEntry (by entry id) and
// shows the full field set and that request.

import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";

import { auditClient } from "@/lib/connect/client";
import { DetailPanel } from "@/components/ui/DetailPanel";
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
export const AUDIT_ENTRY_TABS = ["summary", "request"] as const;
export type AuditEntryTab = (typeof AUDIT_ENTRY_TABS)[number];

/**
 * The request as the server stored it: JSON with secrets redacted,
 * pretty-printed when it parses and shown as it is otherwise; null for none.
 */
export function requestText(bytes: Uint8Array | undefined): string | null {
  if (!bytes || bytes.length === 0) return null;
  const raw = new TextDecoder().decode(bytes);
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

// What the request is, so a reader does not take it for the resource's state.
const REQUEST_HINT =
  "What the action was asked to do, with secrets redacted — not the resource's state before or after it.";
const NO_REQUEST = "No request recorded for this action.";

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
          value: "request",
          label: "Request",
          content: body((entry) => <Request entry={entry} />),
        },
      ]}
    />
  );
}

function Request({ entry }: { entry: AuditLogEntry }) {
  const text = requestText(entry.requestJson);
  if (text === null) return <p className={T.hint}>{NO_REQUEST}</p>;
  return (
    <RecordFields>
      <p className={T.hint}>{REQUEST_HINT}</p>
      <RecordBlock label="Request">{text}</RecordBlock>
    </RecordFields>
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
        <RecordField label="Request id" mono>
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
      {data.userAgent && (
        <RecordField label="Client" mono>
          {data.userAgent}
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

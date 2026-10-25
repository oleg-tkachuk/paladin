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
import { stateChange } from "@/components/features/audit/stateChanges";

/** The panel's tabs, in order; the first opens by default. */
export const AUDIT_ENTRY_TABS = ["summary", "changes", "request"] as const;
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

// What the two states are, so a reader knows where they come from.
const STATE_HINT =
  "The resource as its own Get read it just before and just after the action, with secrets redacted. The reads are not part of the action, so a change made between them by someone else shows here too.";
const NO_STATE =
  "No state recorded for this action: it changes no single resource, or the resource could not be read.";
const NO_CHANGE = "The action left the resource as it was.";

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
          value: "changes",
          label: "Changes",
          content: body((entry) => <Changes entry={entry} />),
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

const pretty = (v: unknown) => JSON.stringify(v, null, 2);

function Changes({ entry }: { entry: AuditLogEntry }) {
  const change = stateChange(entry.stateBeforeJson, entry.stateAfterJson);
  switch (change.kind) {
    case "none":
      return <p className={T.hint}>{NO_STATE}</p>;
    case "created":
      return (
        <RecordFields>
          <p className={T.hint}>{STATE_HINT}</p>
          <RecordBlock label="Created">{pretty(change.after)}</RecordBlock>
        </RecordFields>
      );
    case "removed":
      return (
        <RecordFields>
          <p className={T.hint}>{STATE_HINT}</p>
          <RecordBlock label="Removed">{pretty(change.before)}</RecordBlock>
        </RecordFields>
      );
    case "changed":
      return (
        <RecordFields>
          <p className={T.hint}>{STATE_HINT}</p>
          {change.fields.length === 0 ? (
            <p className={T.hint}>{NO_CHANGE}</p>
          ) : (
            <table className="w-full table-fixed text-xs">
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="w-1/3 pb-1 font-medium">Field</th>
                  <th className="pb-1 font-medium">Before</th>
                  <th className="pb-1 font-medium">After</th>
                </tr>
              </thead>
              <tbody className="font-mono">
                {change.fields.map((f) => (
                  <tr key={f.path} className="align-top">
                    <td className="break-all py-1 pr-2">{f.path}</td>
                    <td className="break-all py-1 pr-2 text-destructive">
                      {f.before ?? "—"}
                    </td>
                    <td className="break-all py-1 text-success">
                      {f.after ?? "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </RecordFields>
      );
  }
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

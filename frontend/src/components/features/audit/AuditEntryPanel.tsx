"use client";

// The single-entry drill-down for an audit row, beside the log. The list
// (ListAuditLog) already returns most columns, but the heavy before/after
// JSON snapshots are only worth fetching on demand — so a row opens this
// panel, which re-reads the canonical record via GetAuditLogEntry (by entry
// id) and shows the full field set, the fields the action changed between the
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
import { Badge } from "@/components/ui/badge";
import { ActorName } from "@/components/features/audit/ActorName";
import { Timestamp } from "@/components/Timestamp";
import { errorMessage } from "@/hooks/errorContract";
import { isUuid } from "@/lib/resources/tenant-resolve";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { fieldChanges, fieldValue, type FieldChange } from "@/lib/fieldChanges";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";

/** The panel's tabs, in order; the first opens by default. */
export const AUDIT_ENTRY_TABS = ["summary", "changes", "snapshots"] as const;
export type AuditEntryTab = (typeof AUDIT_ENTRY_TABS)[number];

type Snapshot = { text: string; json?: { value: unknown } };

// Decode a bytes state-snapshot. The backend stores sanitized JSON; keep the
// parsed value and pretty-print it when it parses, else show the raw text.
// Empty → null so the section is skipped entirely.
function decodeSnapshot(bytes: Uint8Array | undefined): Snapshot | null {
  if (!bytes || bytes.length === 0) return null;
  const raw = new TextDecoder().decode(bytes);
  try {
    const value: unknown = JSON.parse(raw);
    return { text: JSON.stringify(value, null, 2), json: { value } };
  } catch {
    return { text: raw };
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
  // A change needs both sides: a create has no before and a delete no after,
  // and their one snapshot is the Snapshots tab's to show. Offered while the
  // entry loads, so a link to the tab does not land on Summary first.
  const comparable = isLoading || (before !== null && after !== null);

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
        ...(comparable
          ? [
              {
                value: "changes",
                label: "Changes",
                content: body(() =>
                  before?.json && after?.json ? (
                    <FieldChanges
                      changes={fieldChanges(
                        before.json.value,
                        after.json.value,
                      )}
                    />
                  ) : (
                    // Not JSON: only the text can be compared.
                    <DiffView
                      before={before?.text ?? ""}
                      after={after?.text ?? ""}
                    />
                  ),
                ),
              },
            ]
          : []),
        {
          value: "snapshots",
          label: "Snapshots",
          content: body(() => (
            <RecordFields>
              {before && (
                <RecordBlock label="Before">{before.text}</RecordBlock>
              )}
              {after && <RecordBlock label="After">{after.text}</RecordBlock>}
              {!before && !after && <p className={T.hint}>{NO_SNAPSHOT}</p>}
            </RecordFields>
          )),
        },
      ]}
    />
  );
}

const CHANGE_BADGE = {
  added: "success",
  removed: "destructive",
  changed: "warning",
} as const;

/** Each field the action changed: its path, and its value before and after. */
function FieldChanges({ changes }: { changes: FieldChange[] }) {
  if (changes.length === 0) {
    return <p className={T.hint}>No field changed: the snapshots match.</p>;
  }
  return (
    <div className="space-y-2">
      <p className={T.hint}>
        {changes.length} {changes.length === 1 ? "field" : "fields"} changed
      </p>
      <ul
        aria-label="Fields changed"
        className="divide-y divide-border rounded-lg ring-1 ring-border"
      >
        {changes.map((c) => (
          <li key={c.path} data-change={c.kind} className="space-y-1 px-3 py-2">
            <div className="flex items-start justify-between gap-2">
              <span className={cn(T.code, "break-all text-foreground")}>
                {c.path || "the whole snapshot"}
              </span>
              <Badge variant={CHANGE_BADGE[c.kind]}>{c.kind}</Badge>
            </div>
            <div className={cn(T.code, "break-all text-xs")}>
              {c.kind !== "added" && (
                <span data-side="before" className="text-destructive">
                  {fieldValue(c.before)}
                </span>
              )}
              {c.kind === "changed" && (
                <span className="text-muted-foreground"> → </span>
              )}
              {c.kind !== "removed" && (
                <span data-side="after" className="text-success">
                  {fieldValue(c.after)}
                </span>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
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

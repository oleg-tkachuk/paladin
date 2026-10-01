"use client";

// AuditEntryDetailDialog — the single-entry drill-down for an audit row.
// The list (ListAuditLog) already returns most columns, but the heavy
// before/after JSON snapshots are only worth fetching on demand — so a row
// click opens this dialog, which re-reads the canonical record via
// GetAuditLogEntry (by entry id) and renders the full field set, including
// the sanitized before/after state diffs the table doesn't show.

import { useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { auditClient } from "@/lib/connect/client";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { ActorName } from "@/components/features/audit/ActorName";
import { formatTimestampUTC } from "@/lib/format/timestamp";

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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Audit entry</DialogTitle>
          <DialogDescription className={cn(T.code, "text-muted-foreground")}>
            {entryId}
          </DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-3/4" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : error ? (
          <p className="text-sm text-destructive">
            {error instanceof ConnectError
              ? error.rawMessage
              : "Failed to load"}
          </p>
        ) : data ? (
          <div className="max-h-[70vh] space-y-3 overflow-y-auto text-xs">
            <Field k="when" v={formatTimestampUTC(data.at)} mono />
            <Field k="action" v={data.action || "—"}>
              <Badge variant="outline" className={T.code}>
                {data.action || "—"}
              </Badge>
            </Field>
            <Field k="actor" v={data.actorSubject || "system"}>
              <ActorName
                subject={data.actorSubject}
                tenantId={data.actorTenantId}
                fallback="system"
              />
            </Field>
            {data.actorSubject && (
              <Field k="actor id" v={data.actorSubject} mono />
            )}
            {data.actorTenantId && (
              <Field k="tenant" v={data.actorTenantId} mono />
            )}
            {data.actorAudience && (
              <Field k="audience" v={data.actorAudience} mono />
            )}
            <Field k="resource" v={data.resourceName || "—"} mono />
            {data.requestId && <Field k="request" v={data.requestId} mono />}
            {data.sourceIp && <Field k="source ip" v={data.sourceIp} mono />}
            {data.capabilityId && (
              <Field k="capability" v={data.capabilityId} mono />
            )}
            {data.errorMessage && (
              <Field k="error">
                <span className="text-destructive">{data.errorMessage}</span>
              </Field>
            )}
            {before && <Snapshot label="before" json={before} />}
            {after && <Snapshot label="after" json={after} />}
            {!before && !after && (
              <p className="text-muted-foreground">
                No state snapshot recorded for this action.
              </p>
            )}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function Field({
  k,
  v,
  mono,
  children,
}: {
  k: string;
  v?: string;
  mono?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-3">
      <span className="w-24 shrink-0 uppercase tracking-wider text-muted-foreground">
        {k}
      </span>
      <span className={cn("min-w-0 flex-1 break-words", mono && T.code)}>
        {children ?? v}
      </span>
    </div>
  );
}

function Snapshot({ label, json }: { label: string; json: string }) {
  return (
    <div className="space-y-1">
      <p className="uppercase tracking-wider text-muted-foreground">{label}</p>
      <pre className="max-h-64 overflow-auto rounded-md border border-border bg-muted/40 p-2 font-mono text-[11px] leading-relaxed">
        {json}
      </pre>
    </div>
  );
}

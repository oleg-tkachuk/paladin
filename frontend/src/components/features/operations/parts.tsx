"use client";

// The pieces every view of an operation shares — the drawer in the top bar
// and the operations page: its collection as a link, its state's icon, and
// its whole record.

import Link from "next/link";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClockIcon,
  NoSymbolIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import {
  RecordBlock,
  RecordDialog,
  RecordField,
} from "@/components/ui/RecordDialog";
import { Timestamp } from "@/components/Timestamp";
import { ActorName } from "@/components/features/audit/ActorName";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { formatDuration } from "@/lib/format/duration";
import { collectionHref } from "@/lib/navigation/hrefs";
import {
  OP_STATUS_BADGE,
  isActive,
  objectCountLabel,
  operationElapsedMs,
  operationError,
  operationLabel,
  operationOutcome,
  operationProgress,
  operationStatus,
  parseCollectionName,
  type OpStatus,
} from "@/lib/operations";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

export const STATUS_ICON = {
  pending: ClockIcon,
  running: ArrowPathIcon,
  succeeded: CheckCircleIcon,
  failed: XCircleIcon,
  cancelled: NoSymbolIcon,
} as const;
export const STATUS_TINT: Record<OpStatus, string> = {
  pending: "text-muted-foreground",
  running: "text-warning",
  succeeded: "text-success",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
};

/** The collection an operation acts on, as a link that closes the drawer. */
export function CollectionLink({
  name,
  onNavigate,
}: {
  name: string;
  onNavigate?: () => void;
}) {
  const c = parseCollectionName(name);
  if (!c) return <span className={T.code}>{name}</span>;
  return (
    <Link
      href={collectionHref(c.tenant, c.collection)}
      onClick={onNavigate}
      className={cn(T.code, "break-all hover:text-primary hover:underline")}
      title={name}
    >
      {c.collection}
    </Link>
  );
}

/** An operation's whole record: what it was asked, how it went, why not. */
export function OperationRecord({
  op,
  onClose,
}: {
  op: Operation | null;
  onClose: () => void;
}) {
  const status = op ? operationStatus(op) : null;
  const outcome = op ? operationOutcome(op) : null;
  const progress = op ? operationProgress(op) : null;
  const elapsed = op ? operationElapsedMs(op, new Date()) : null;
  return (
    <RecordDialog
      open={op !== null}
      onOpenChange={(o) => !o && onClose()}
      title={op ? operationLabel(op) : "Operation"}
      subtitle={op?.name}
    >
      {op && status ? (
        <>
          <RecordField label="State">
            <Badge variant={OP_STATUS_BADGE[status].variant}>
              {OP_STATUS_BADGE[status].label}
            </Badge>
          </RecordField>
          <RecordField label="Type" mono>
            {op.type}
          </RecordField>
          {op.target ? (
            <>
              <RecordField label="Collection">
                <CollectionLink name={op.target.collection} />
              </RecordField>
              {op.target.destination ? (
                <RecordField label="Copied into">
                  <CollectionLink name={op.target.destination} />
                </RecordField>
              ) : null}
              <RecordField label="Objects">
                {objectCountLabel(op.target.objectCount)}
              </RecordField>
            </>
          ) : null}
          {op.initiatorSubject ? (
            <RecordField label="Started by">
              <ActorName
                subject={op.initiatorSubject}
                tenantId={op.initiatorTenantId}
              />
            </RecordField>
          ) : null}
          <RecordField label="Started">
            {op.createdAt ? <Timestamp ts={op.createdAt} /> : "—"}
          </RecordField>
          {op.doneAt ? (
            <RecordField label="Finished">
              <Timestamp ts={op.doneAt} />
            </RecordField>
          ) : null}
          {elapsed !== null ? (
            <RecordField label={isActive(status) ? "Running for" : "Took"}>
              {formatDuration(elapsed)}
            </RecordField>
          ) : null}
          {progress ? (
            <RecordField label="Progress">
              {progress.processed} of {progress.total}
            </RecordField>
          ) : null}
          {outcome ? (
            <RecordField label="Result">
              {outcome.succeeded} done
              {outcome.alreadyApplied > 0
                ? `, ${outcome.alreadyApplied} already so`
                : ""}
              {outcome.failed > 0 ? `, ${outcome.failed} failed` : ""} of{" "}
              {outcome.total}
            </RecordField>
          ) : null}
          {operationError(op) ? (
            <RecordField label="Error">
              <span className="text-destructive">{operationError(op)}</span>
            </RecordField>
          ) : null}
          {outcome && outcome.failures.length > 0 ? (
            <RecordBlock label="Objects that failed">
              {outcome.failures
                .map((f) => `${f.objectId}  ${f.reason}`)
                .join("\n")}
            </RecordBlock>
          ) : null}
        </>
      ) : null}
    </RecordDialog>
  );
}
